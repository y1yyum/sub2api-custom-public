# 公开仓库 CI

自 2026-10-31 起，公开仓库直接测试当前检出的裁剪版本。入口为
[`workflows/local-ci.yml`](workflows/local-ci.yml)，执行策略来自 Gitea；
GitHub 平台的工具准备集中在 [`actions/setup-ci`](actions/setup-ci/action.yml)。

## 测试范围

- 后端使用仓库内保留的 Go 测试，只补充这些测试必需的辅助对象和临时数据库入口。
- 前端关键测试从 Git 跟踪的 `*.spec.ts`、`*.test.ts` 自动生成，覆盖当前保留的全部测试文件。
- `.gitea/critical-tests.txt` 和 `.gitea/slow-unit-tests.txt` 仅列出现存测试。
- 不下载或恢复上游完整测试闭包，不执行历史 `prepare-upgrade-validation.py` 准备流程。

PR 沿用 Gitea 的改动范围选择；模块、工具镜像或公共工作流配置变更按原规则回退到全量。
这里的“全量”指当前公开仓库保留的全部测试。发布标签和手动运行也使用同一裁剪版本。
全量集成测试在三个 runner 上并行：`owner` 只跑 `TestScheduledOwnerTopology`，
`repository-rest` 跑仓储包除该用例外的测试，`other-packages` 跑全部非仓储包。
这三个分片在 GitHub 上由 Go 按 runner 环境决定并行度；局部 PR 使用一个
`scoped` 作业，保留原来的测试选择和双核限制。任一分片失败不会取消其余分片。

| 作业 | 运行条件；不符合时跳过 |
| --- | --- |
| `changes` | 每次运行，计算改动范围和应运行的作业 |
| `unit` | 有整包或按名选择的单元测试 |
| `integration` | 全部包用三个分片；局部包或用例只用 `scoped`；两者皆空则跳过 |
| `golangci-lint` | 范围包含需要静态检查的 Go 包 |
| `frontend` | 前端、Node 工具链或对应工作流作业受影响 |
| `shell` | 部署、资源、构建入口或对应工作流作业受影响 |
| `release-helpers` | 发布辅助工具或对应工作流作业受影响 |
| `security` | 范围计算成功后必跑，包括纯文档 PR |
| `ci-gate` | 始终运行；范围计算、安全和所有应运行作业必须成功，其余作业必须为 `skipped` |

发布标签、手动全量或范围不明时，上述六个可选作业全部运行；纯文档改动通常只需范围计算、安全与门禁。

## 复现

工具版本见 `backend/go.mod`、`.gitea/node-shasums.txt` 和
`.github/actions/setup-ci/action.yml`。安装对应工具及前端锁定依赖后：

```sh
make test-frontend
cd backend
bash ../.gitea/unit-full.sh
python3 ../.gitea/integration.py owner
python3 ../.gitea/integration.py repository-rest
python3 ../.gitea/integration.py other-packages
golangci-lint run --timeout=30m ./...
```

集成测试需要 Docker，并自动创建和销毁专用 PostgreSQL 容器；不读取运行环境中的业务数据库连接串。
测试辅助代码使用合成凭据。测试结果仅代表公开仓库保留的覆盖范围。

`upgrade/`、`isolation/` 及旧准备脚本保留为历史资料，不是当前 CI 的执行入口或验收要求。
