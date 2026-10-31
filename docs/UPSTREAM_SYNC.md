# 公开源码同步记录

## 2026-10-31：门户、依赖与 OAuth 刷新

关联：[Issue #15](https://github.com/y1yyum/sub2api-custom-public/issues/15)。
公开基准为 `2207f7425da2776caa0c02f3277036fc30f26331`；
开发源快照为 `ab32c6bad563030c6645ba9798ec7fa689700f4b`。
以公开仓库历史为父提交，按文件移植，不合并开发仓库历史。

### 行为与兼容性

- 门户沿用已有路由和 API，更新像素图标、默认品牌、线框、定价占位图及概览数值可读性，保留 Dock 的键盘焦点提示。
- `vue-i18n` 与消息编译器更新为 `11.4.14`；锁文件将 `follow-redirects` 更新为 `1.16.2`、`decode-uri-component` 更新为 `0.5.1`，保留其他依赖约束。
- OAuth 刷新原先只读取 `ctx.Err()`。当截止时间已到、取消通知尚未送达时，现在也根据截止时间结束刷新，不接受迟到凭据；已有取消原因优先。
- 父刷新周期结束时不再重试，也不将父周期结束记为供应商失败。若凭据已经持久化，则仍同步缓存状态；仅内部尝试超时且持久化已成功时继续保留成功语义。
- 同步网关条件表达式、Gemini 常量排版、用量 SQL 构建的等价 lint 修正，移除未使用的请求体解压包装函数；实际请求体读取仍使用有大小限制的入口。

本次不带入 Relay Tower、Quefa 直充的实现、接线、迁移或测试变化。
公开版本仍为 `0.3.1`；部署配置、镜像来源、更新源和隔离 CI 沿用公开仓库。
不新增或修改 SQL 迁移，不改变监听地址、健康检查、持久化路径或环境变量。

### 已知冲突热点

| 路径 | 修改原因 | 后续同步处理 |
| --- | --- | --- |
| `backend/internal/service/{oauth_refresh_api,token_refresh_service}.go` | 同步已到期刷新处理；新增 `refresh_deadline.go` 与确定性回归测试 | 保留父周期与单次尝试的错误归属、凭据持久化及缓存同步语义 |
| `backend/internal/handler/openai_gateway_handler.go`、`backend/internal/pkg/geminicli/constants.go`、`backend/internal/pkg/httputil/body.go`、`backend/internal/repository/usage_log_repo_stats.go` | 同步最小等价 lint 修正 | 保留请求体大小限制、断连计费和现有 SQL 行为 |
| `frontend/package.json`、`frontend/pnpm-lock.yaml` | 同步依赖版本 | 一起更新，使用固定 pnpm 版本验证冻结安装、类型与构建 |
| `frontend/src/custom-portal/` | 同步门户视觉与对应回归测试 | 保留既有路由、品牌自定义优先级和键盘可访问性 |

### 验证与回滚

使用公开仓库现有的固定上游测试闭包与隔离执行入口；新增回归测试与实际源码一起运行，不启用外网测试降级。
本地检查与目标 PR 的 CI 结果记录在 PR；源码检查不能代替完整运行验收。
本次没有数据库或配置迁移，回滚可撤销对应同步提交；若另行部署，仍须按公开仓库升级说明评估镜像与数据兼容性。
