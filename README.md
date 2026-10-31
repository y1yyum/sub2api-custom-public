# Sub2API Custom Public

Sub2API Custom Public 是基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的二开版本，提供统一的 AI API 网关、账号管理、渠道调度、用量统计和管理后台。

本仓库面向实机 Docker 部署。当前公开基线版本为 **v0.3.1**；本仓库不携带生产凭据，CI 只构建、测试和发布镜像，不连接任何服务器。

## 上游稳定版同步（分步审查中）

本轮以用户确认的上游 `v0.2.16`（`01842576b3d5c1e2545b5a99ad555d36e28ed306`）为内容比较基点，向固定的 `v0.2.18`（`ac1b8f1271be0d674fa6f074403c1410f30a3a81`）移植；二开审查基线为 `357a6f0f5bd6cadc96d1caec19aeb8da52eeb55b`。公开仓库是源码快照，这些上游提交不是二开的真实 Git 祖先，不能直接用上游目录覆盖二开。

本阶段引入网关转换、模型目录与价格、服务账号配额及共享调度缓存依赖组，保留运行时 OAuth 凭据注入、统一状态页、自有界面、迁移和发行链。支付退款与存量分组迁移在后续依赖 PR 中审核；本阶段不表示整套升级已经完成或可直接部署。特别是 Gemini/Antigravity 生图开关需要与新增迁移及既有禁用策略一起验收。

不包含上游 `main` 在 `v0.2.18` 之后的变化；不发布镜像、不移动标签，也不将二开 `VERSION` 改为上游版本。本轮仍保留 `0.3.1`，新发行版本号与部署放行另行确认。

## Docker Compose 部署

要求：Docker Engine、Docker Compose 插件和可用的 Linux 主机目录权限。

```bash
git clone https://github.com/y1yyum/sub2api-custom-public.git
cd sub2api-custom-public
cp deploy/.env.example deploy/.env
```

编辑 `deploy/.env`，至少设置数据库密码、固定的 `JWT_SECRET` 和 `TOTP_ENCRYPTION_KEY`。不要将 `deploy/.env` 提交到 Git。

```bash
docker compose -f deploy/docker-compose.local.yml config
docker compose -f deploy/docker-compose.local.yml up -d --build
curl http://127.0.0.1:8080/health
```

默认应用地址为 `http://127.0.0.1:8080`，容器监听 `0.0.0.0:8080`。数据写入 `deploy/data/`、`deploy/postgres_data/` 和 `deploy/redis_data/`，这些目录需要纳入备份范围。

### 使用预构建镜像

每个版本都会发布 `linux/amd64` 和 `linux/arm64` 镜像到 GHCR。在 `deploy/.env` 中设置 `SUB2API_IMAGE`，即可跳过本地构建：

```bash
SUB2API_IMAGE=ghcr.io/y1yyum/sub2api-custom-public:0.3.1
```

```bash
docker compose -f deploy/docker-compose.local.yml pull sub2api
docker compose -f deploy/docker-compose.local.yml up -d --no-build
```

生产环境建议按 Release 说明中的 `@sha256:` Digest 固定镜像。镜像使用 Sigstore 无密钥签名，并附带构建来源证明和 SBOM，验证命令见对应 Release 说明。无法访问 GHCR 时，可下载 Release 附件中的镜像包，用 `docker load` 导入。

## 升级与回滚

升级前先备份 `deploy/` 下的数据目录和 `.env`。源码部署使用以下流程：

```bash
git pull --ff-only origin main
docker compose -f deploy/docker-compose.local.yml up -d --build
docker compose -f deploy/docker-compose.local.yml ps
```

应用启动时会执行向前数据库迁移。不要删除数据目录来解决迁移问题；出现异常时保留日志和备份，并按对应版本的发布说明处理。

使用预构建镜像时，把 `SUB2API_IMAGE` 改为新版本，再执行 `pull` 和 `up -d --no-build`。回滚前必须单独确认旧镜像、数据库和配置兼容；不要无条件恢复数据库备份。

**支付退款不能只换旧镜像或回灌旧数据库。** v0.2.18 同步后的 pending 退款会保留余额/订阅扣减，旧 worker 可能再次扣减；数据库恢复也不能撤销外部已发生的支付或退款。先停止相关处理和新旧 worker 混跑，保留审计并逐单对账，再决定前向修复或协调恢复。见[支付放行与回滚手册](docs/upgrades/stable-0.2.18-payment.md)及[图片分组禁用状态保留](docs/upgrades/stable-0.2.18-image-groups.md)。

更新器会查询 [公开仓库 Releases](https://github.com/y1yyum/sub2api-custom-public/releases)，但 Release 只提供容器镜像，不提供可在线替换的二进制包，因此管理后台的在线更新不可用，请按上面的镜像流程升级。

## CI 与发布

- `.github/workflows/ci.yml`：普通 PR 按差异选择关键回归，保留常驻安全门禁；手动运行执行完整测试。合入 main 不重复运行。
  后端继续禁外网隔离执行，完整单元测试使用慢用例分片；前端快速通道为 Node 24，完整通道覆盖 Node 20/24。
  发布标签通过 Release 调用完整 CI 并执行镜像冒烟，统一门禁名为 `CI gate`。范围、缓存及验证方式见[CI 说明](docs/CI_WORKFLOW.md)。
- `.github/workflows/release.yml`：推送 `vX.Y.Z` 标签时运行，流程如下：
  1. 校验标签与 `backend/cmd/server/VERSION` 一致，且标签指向的提交在 `main` 上。
  2. 跑完整 CI。
  3. 构建双架构镜像，先按 Digest 推送到 GHCR。
  4. 对该 Digest 跑冒烟测试，通过后才打 `X.Y.Z`、`X.Y` 和 `latest` 标签。
  5. 签名镜像，生成构建来源证明和 SBOM，创建 GitHub Release。

  带预发布后缀的标签（如 `v0.4.0-rc.1`）发布为 Pre-release，不更新 `X.Y` 和 `latest`。也可以手动触发该工作流，为已存在的标签补发镜像；已存在的 Release 不会被覆盖。

发布新版本：

```bash
# 先在 main 上把 backend/cmd/server/VERSION 改为新版本并合并
git tag v0.3.1 origin/main
git push origin v0.3.1
```

## 版本号规则

项目使用纯数字 Semantic Versioning，Git 标签格式为 `vMAJOR.MINOR.PATCH`。

| 变更类型 | 版本变化 |
| --- | --- |
| 向后兼容的缺陷或安全修复 | `PATCH`，例如 `0.3.0` → `0.3.1` |
| 向后兼容的新功能、配置项或数据库迁移 | `MINOR`，例如 `0.3.1` → `0.4.0` |
| 不兼容的 API、配置、数据迁移或运行行为 | `MAJOR`，例如 `0.4.0` → `1.0.0` |

首个公开版本为 `v0.3.0`。每个版本标签只创建一次，不复用或移动已发布标签。发布构建、二进制版本、Release 资产和校验和必须来自同一个提交。无 Git 标签的源码构建使用 `backend/cmd/server/VERSION` 作为版本回退值。

## 相关文件

- [公开源码同步记录](docs/UPSTREAM_SYNC.md)
- [Docker Compose 配置](deploy/docker-compose.local.yml)
- [环境变量示例](deploy/.env.example)
- [完整配置示例](deploy/config.example.yaml)
- [支付配置说明](docs/PAYMENT.md)
- [中文支付配置说明](docs/PAYMENT_CN.md)
- [管理员合规承诺](docs/legal/admin-compliance.zh.md)
- [许可证声明](NOTICE.md)

## 许可证与归属

项目主许可证为 [GNU LGPL v3.0](LICENSE)。`COPYING` 保留 LGPL v3 所引用的 GNU GPL v3 文本；`NOTICE.md` 和前端 `public/NOTICE-*.txt` 保留上游及第三方归属声明。第三方依赖、图标、页面参考实现和其他素材仍受各自许可证和权利范围约束。

本项目是上游项目的二开版本，不是上游官方发行。上游 Go module/import 路径保持为 `github.com/Wei-Shaw/sub2api`，用于维护源码兼容性。

## 安全

不要在 Issue、Pull Request、日志或仓库中公开生产凭据、用户 Token、OAuth secret、数据库密码或 `.env` 文件。发现安全问题时，请使用仓库的私密漏洞报告入口；公开部署前还应自行审查主机、防火墙、反向代理和备份策略。
