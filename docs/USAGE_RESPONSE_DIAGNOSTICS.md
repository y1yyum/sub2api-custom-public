# 管理端调用日志响应诊断

更新时间：2026-11-08。记录的是上游声明，不独立测量真实 token，也不改变计费口径。

## 展示与权限

管理端在原 token 单元格显示缩放、非零输入变化率、结束原因和完整性小标记；“诊断”弹窗展示初始／最终用量及消息 ID、思考 token、服务档位、区域、停止序列、工具声明、推理轮次、事件和内容块计数、错误类型。不增加独立列表列。

缺失值显示“未返回”，历史及未支持路径显示“未记录”，明确返回的零保留为零。初始值也可能已经缩放。

只在管理员 DTO 返回 `response_diagnostics`；用户 DTO 不包含该字段，服务模型直接 JSON 序列化也排除它。共享 `UsageTable` 的 `showResponseDiagnostics` 默认关闭，仅管理端页面显式开启。未来用户端字段配置页本次不实现，后续应同时控制 API 输出和界面可见性。

## 采集契约

- 复用原始响应模型观察器，每次转发尝试独立。当前支持 Anthropic SSE、OpenAI Responses SSE 和 Chat Completions SSE；其他协议、非流式响应不推断数据。
- Anthropic 初始用量取 `message_start.message.usage`；最终状态按 `message_delta.usage` 覆盖已声明字段，未重报的字段沿用此前声明，绝不逐事件相加。零可以覆盖旧值，完全未出现的字段保持空。
- 总缩放标记表示至少一个事件声明 `_sub2api_display_scaled=true`；阶段快照分别保留该事件的声明，缺失保持空。
- 输入变化率为 `(最终输入总量 - 初始输入总量) / 初始输入总量`，只在两端总量已知且初始值大于零时计算。输入总量含普通输入、缓存读取及写入，不含输出。
- OpenAI 输入总量采用其包含缓存的原生计数，不再叠加缓存；只返回最终用量时，初始和变化率为空。
- Anthropic 完整性要求观测到 `message_stop`、内容块闭合且无流内错误；Responses 使用终止事件；Chat 使用 `finish_reason`。这些证据不证明下游已完整接收。达到输出上限不等于流中断。
- 工具只记录类型、名称和声明次数，不证明客户端执行成功。轮次保留总数和最多前 32 轮的类型、模型及用量，不重复计入最终用量。

## 开销与数据

不新增上游请求、本地 tokenizer、全文 SSE 缓存或独立数据库写入。文本、思考、签名和工具参数分片只走事件类型快路径；不持久化其正文、参数、签名或错误正文。只保留有限摘要，随原用量 INSERT／批处理写入可空 JSONB。

开发机 Linux/amd64 容器中，2 KiB 文本事件新增观察器的局部微基准为 `97.11 ns/op`、`24 B/op`、`1 allocs/op`。这不代表生产首字或端到端延迟。

## 迁移与验证

新增 `9010_usage_response_diagnostics.sql`：仅增加可空 JSONB 列，无默认值、回填、新索引。旧行保留 NULL，旧版显式列 INSERT 可继续运行，旧镜像可忽略此列。DDL 仍需短暂表锁；生产迁移审查为 `pending`，本次未连接或修改生产数据库。

临时 PostgreSQL 18.1 已验证旧行 NULL、旧写入兼容与 JSONB 读写。仓储单元验证了完整列顺序、NULL、损坏 JSON 的失败语义。局部验证不替代完整迁移门禁或生产升级验收。

相关后端单元：
```sh
go test -tags=unit ./internal/service ./internal/handler/dto ./internal/repository -run 'Test.*(ResponseDiagnostics|UpstreamResponseModel|GatewayServiceRecordUsage|UsageLog|ForwardAs|NativeAnthropic|ParseSSEUsage)' -count=1
go test -tags=unit ./internal/service -run '^$' -bench BenchmarkResponseDiagnosticsTextDelta -benchmem
```

前端验证包含诊断组件、共享表格、管理端及用户端日志页面、国际化、ESLint、类型检查和生产构建。Playwright 使用真实管理端表格预览检查列表、详情、窄屏、深色模式与浏览器错误。

源码入口：[采集摘要](../backend/internal/service/response_diagnostics.go)、[写入](../backend/internal/repository/usage_log_repo_insert.go)、[管理员映射](../backend/internal/handler/dto/mappers.go)、[诊断组件](../frontend/src/components/admin/usage/UsageDiagnostics.vue)。
