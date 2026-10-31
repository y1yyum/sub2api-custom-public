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
安全、lint 和门禁检查继续执行，测试资料缺失或运行失败会使 CI 失败。

## 发行衔接

GitHub 的 `ci-gate` 在原检查成功后上传源码与范围收据，作为平台元数据扩展，
不改变 Gitea 的选测和执行规则。`publish.yml` 仅在标签全量 Local CI 成功后
消费该证据，构建、冒烟并签名交付镜像，不再启动第二轮 CI。
PR 快检收据不能用于发行。权限、回执、手动重试和幂等行为见[发行衔接说明](PUBLISHING.md)。

## 复现

工具版本见 `backend/go.mod`、`.gitea/node-shasums.txt` 和
`.github/actions/setup-ci/action.yml`。安装对应工具及前端锁定依赖后：

```sh
make test-frontend
cd backend
bash ../.gitea/unit-full.sh
go test -tags=integration ./...
golangci-lint run --timeout=30m ./...
```

集成测试需要 Docker，并自动创建和销毁专用 PostgreSQL 容器；不读取运行环境中的业务数据库连接串。
测试辅助代码使用合成凭据。测试结果仅代表公开仓库保留的覆盖范围。

`upgrade/`、`isolation/` 及旧准备脚本保留为历史资料，不是当前 CI 的执行入口或验收要求。
`.github/scripts/test-smoke.sh` 继续用于实际发行镜像的冒烟验证。
