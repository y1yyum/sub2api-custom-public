# Sub2API Custom Public

Sub2API Custom Public 是基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的二开版本，提供统一的 AI API 网关、账号管理、渠道调度、用量统计和管理后台。

本仓库面向实机 Docker 部署。当前公开基线版本为 **v0.3.0**；本仓库不携带生产凭据，也不通过 CI 连接生产服务器。

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

如果已有经过审查的镜像，可通过 `SUB2API_IMAGE` 覆盖默认的本地构建镜像；公开仓库当前不承诺已发布预构建镜像。

## 升级与回滚

升级前先备份 `deploy/` 下的数据目录和 `.env`。源码部署使用以下流程：

```bash
git pull --ff-only origin main
docker compose -f deploy/docker-compose.local.yml up -d --build
docker compose -f deploy/docker-compose.local.yml ps
```

应用启动时会执行向前数据库迁移。不要删除数据目录来解决迁移问题；出现异常时保留日志和备份，并按对应版本的发布说明处理。

在线更新只面向带 GitHub Release 资产的正式构建，更新器查询 [公开仓库 Releases](https://github.com/y1yyum/sub2api-custom-public/releases)。源码构建没有可下载的 Release 资产时，应按上面的 Git 拉取和镜像重建流程升级。

## 版本号规则

项目使用纯数字 Semantic Versioning，Git 标签格式为 `vMAJOR.MINOR.PATCH`。

| 变更类型 | 版本变化 |
| --- | --- |
| 向后兼容的缺陷或安全修复 | `PATCH`，例如 `0.3.0` → `0.3.1` |
| 向后兼容的新功能、配置项或数据库迁移 | `MINOR`，例如 `0.3.1` → `0.4.0` |
| 不兼容的 API、配置、数据迁移或运行行为 | `MAJOR`，例如 `0.4.0` → `1.0.0` |

首个公开版本为 `v0.3.0`。每个版本标签只创建一次，不复用或移动已发布标签。发布构建、二进制版本、Release 资产和校验和必须来自同一个提交。无 Git 标签的源码构建使用 `backend/cmd/server/VERSION` 作为版本回退值。

## 相关文件

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
