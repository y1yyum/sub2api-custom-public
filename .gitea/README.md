# 本地 Gitea CI

`.gitea/workflows/` 是本仓库唯一的 CI 入口。工作流由本机 Gitea 和
WSL rootless `act_runner` 执行，结果用于本地 Issue、分支、PR 和 main
保护规则。

Runner 标签为 `sub2api-local`，使用 Gitea Runner 4.0.0 的
`builtin:checkout`，不下载 GitHub Action。工作流不得使用生产凭据、
宿主机 Docker socket 或特权容器。

作业镜像固定工具链；作业通过 `DOCKER_HOST` 使用隔离的 `ci-dind` daemon。
工作流移植了 `custom-ci.yml` 与 `custom-security.yml` 的全部阻断检查。
PR 走快速通道：`ci-scope.py` 按 Git 差异只跑受影响的作业，Go 测试只跑改动文件同名的测试与
`critical-tests.txt` 关键清单；安全扫描和必需 ci-gate 总是运行。发布 tag、手工触发、未知范围、
作业镜像与 Go 模块改动执行全量测试。
全量 unit 由 `unit-full.sh` 执行：`slow-unit-tests.txt` 中的慢测试与包内其余测试分进程并行，不跳过任何测试。
验收状态以流程文档 §8 为准；未验收前不得自动合并。GitHub 远端 Actions 已关闭；该设置在服务端，不由本地文件控制。

完整流程见 `docs/LOCAL_CI_WORKFLOW.md`。部署和重启检查见
`deploy/local-gitea/README.md`。
