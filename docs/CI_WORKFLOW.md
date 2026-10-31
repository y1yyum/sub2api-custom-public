# 快速检查与发布完整测试

本次把开发主线 `ab32c6bad563030c6645ba9798ec7fa689700f4b` 的范围选择、关键测试、
慢测试分片和发布触发策略移植到 GitHub Actions。关联 Issue #17；截至 2026-10-31。

## 触发与覆盖

| 场景 | 后端安全、构建及漏洞检查 | 应用回归 | 镜像冒烟 |
| --- | --- | --- | --- |
| 普通 PR | 常驻 | 受改动影响的包或命名测试，加资金、认证等关键回归；前端 Node 24 的关键及对应测试 | 不运行 |
| 仅文档 PR | 常驻 | 范围计划不选择重测试 | 不运行 |
| 合并到 main | 不重复启动 CI | 不重复运行 | 不重复运行 |
| 发布标签 | Release 调用同一 CI，并显式设置完整模式 | 全部单元、集成与 Node 20/24 前端测试 | 运行 |
| 手动运行 CI | 常驻 | 完整模式 | 运行 |

依赖修补 PR 仍走快速通道，不会因为修改 go.mod 或锁文件就自动执行全部后端测试。
后端编译、go vet、govulncheck、密钥扫描、依赖审计、审计例外有效期、开发依赖棘轮、
v-html 汇聚点和部署契约检查保持阻断。Go 代码还运行 golangci-lint/gosec。
没有使用 continue-on-error 把失败变成成功；审计命令先保存 JSON，再由检查器核对错误、结论及例外。

范围由 `.github/scripts/ci-plan.py` 基于 PR 的真实 base 与受检提交生成。
缺少有效 base、关键夹具或计划来源不匹配时直接失败，不猜测为空改动。
计划与实际测试事件一起作为 artifact 保存；快速检查明确记录未选中的测试数量，不宣称完成全量覆盖。
新增或修改的认证、支付、迁移边界仍需相应审查；发布完整测试不能代替生产迁移或部署验收。

## 隔离执行与缓存

测试始终运行在既有的禁外网、非 root、只读根、无宿主挂载和 Docker socket 的容器内。
依赖下载与应用执行分离，编译步骤也禁外网，保留 race、固定工具链及源码/二进制摘要。
快速通道只编译计划选择的测试包；完整通道仍发现全部包。

标准库 race 缓存放在应用源码之前的独立镜像层。BuildKit 本地缓存通过 GitHub Cache 复用，
同一 PR 的多轮修补可恢复上一轮缓存。缓存命中与实际冷启动耗时以运行记录为准，
不能保证所有 PR 都复用到缓存，也不承诺固定分钟数。
main 不再通过 push 重跑；需要预热默认分支缓存时可手动完整运行。

完整单元测试沿用开发主线的慢测试清单，将慢用例分为四组，并与剩余用例一起按最多两个子进程并行运行。
每个包只编译一次；源码未变时复用同一个经校验的 race 二进制。
时区和进程级分配断言保持独立进程。分片调度在首条结果前固定，失败不会触发重试。
每个子进程使用独立可写缓存，避免一个分片清理另一个分片的文件。
已改名的慢用例自然回到剩余集合，不会被跳过；计划内用例必须实际运行且只运行一次。

## 合并与发布状态

统一状态名为 `CI gate`。它始终等待计划、常驻安全检查及计划要求的任务：
应运行却被跳过、失败、取消或缺少结果都会阻断；只有范围外的任务允许 skipped。
GitHub 的 needs 默认会传播失败和跳过，因此聚合任务使用 always()。
规则配置需要绑定这个稳定状态，而不是绑定仅在完整模式出现的动态任务名。
本 PR 不修改服务端分支保护，不执行合并、标签、发布或部署。

Release 继续保留既有版本、提交、镜像冒烟、签名、来源证明及 SBOM 链路。
发布标签只通过 Release 调用完整 CI，CI 自身不再响应 main push 或重复响应同一个发布标签。
完整运行任一测试失败时，发布依赖仍无法继续。

## 同步边界与验证入口

公开仓库使用 GitHub hosted runner、固定摘要的 Action 和工具下载，
不移植开发机的 runner 标签、网络地址、Docker socket、凭据或 Gitea 服务端角色脚本。
保留公开仓库的固定上游夹具和已有隔离断言；Relay Tower 与 Quefa 直充仍不在本次同步范围。

```sh
python3 .github/scripts/fetch-upgrade-fixture-inputs.py
python3 -m unittest discover -s .github/isolation -p 'test_*.py'
python3 .github/scripts/test-upgrade-preservation.py
python3 -m unittest discover -s scripts -p 'test_scan_secrets*.py'
python3 scripts/audit-gate.py --selftest
python3 scripts/check-v-html-sinks.py --selftest
```

以上为静态及工具回归。实际隔离运行结果、完整与快速覆盖、耗时和未执行项记录在目标 PR。
不能用这些静态检查宣称完整应用测试已经通过。

依据：[工作流输入类型](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax)、
[needs 与 always()](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-jobs)。
