<div align="center">

# Sub2API Custom Public

[![Release](https://img.shields.io/badge/Release-v0.4.0--rc.1-blue.svg)](https://github.com/y1yyum/sub2api-custom-public/releases)
[![Go Version](https://img.shields.io/badge/Go-1.27.3-00ADD8.svg)](https://golang.org/)
[![Vue Version](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![Docker Multi-Arch](https://img.shields.io/badge/Docker-amd64%20%7C%20arm64-2496ED.svg)](https://github.com/y1yyum/sub2api-custom-public/pkgs/container/sub2api-custom-public)
[![Sigstore Cosign](https://img.shields.io/badge/Signature-Sigstore%20Signed-green.svg)](https://github.com/sigstore/cosign)
[![License: LGPL v3](https://img.shields.io/badge/License-LGPL%20v3-blue.svg)](LICENSE)

**面向高可用实机生产环境的 AI API 聚合网关与订阅分发平台（基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 二开强化版）**

[快速部署](#-快速实机部署) • [v0.4.0 核心特性](#-v040-核心特性与二开增强) • [数据库迁移](#-数据库迁移规范与双-247-兼容) • [升级与运维](#-升级与运维指南) • [供应链安全](#-供应链安全与可验证交付)

</div>

---

## 📖 项目简介

**Sub2API Custom Public** 是专为实机 Docker 生产环境打造的不可变 AI API 网关与配额管理平台。当前公开预发布版本为 **v0.4.0-rc.1**；正式版放行与存量库验收另行确认。

在官方 Sub2API 核心能力的基础上，`v0.4.0` 完整吸收并审查了上游 `v0.2.18` 与 `v0.2.19` 的核心特性，深入解决了高并发流式计费丢失、OAuth 凭据刷新悬挂、SSRF 探测逃逸及顶层模型混淆注入等关键生产缺陷，并提供专属的莱茵实验室风格（Rhine Lab）主题门户与像素艺术（Pixel Art）界面。

本仓库为完全开源的公开交付版本，剥离了内部私有直充与运维专有探针模块，**不包含任何生产凭据或私有环境依赖**，所有构建产物均支持无外网依赖的端到端单体验证。

---

## 🚀 v0.4.0 核心特性与二开增强

### 1. 🎨 莱茵生命主题门户与前端交互增强 (Custom Portal)
- **全新极客像素风格**：基于 `frontend/src/custom-portal/` 重构的 Rhine Lab 风格视觉体系，内嵌像素艺术图标系统（Pixel Art Icons）、定制线框与高对比度状态面板。
- **实时用量与健康监控大屏**：
  - **Metric Cards**：实时请求量、Token 吞吐、错误率与通道延迟状态码感知。
  - **Usage Sparkline**：轻量级实时火花线趋势图，零冗余图表依赖。
  - **Top 模型排行榜**：一览集群热门模型调用与计费分布。
  - **模型与渠道（Models & Channels）**：提供公开的模型目录、状态与实时费率速查看板。
- **无障碍访问性（A11y）**：Dock 导航栏全面适配键盘焦点捕捉与辅助导航。

### 2. 🛡️ 网关流式计费与异常断连零漂移保障 (Stream & Billing Hardening)
- **客户端断连单归属结算（Settle-Once）**：解决客户端过早中断（Late Disconnect）与写时网络中断下的账单归属冲突，杜绝 Token 用量丢失或重复扣款。
- **裸错误流（Bare SSE Errors）终止保护**：在上游返回裸错误或连接异常中止时，强制执行最终 Authoritative Usage 的 Drain 与落库，保障用量数据完整。
- **顶层模型字段唯一性强校验（P0 Model Uniqueness Guard）**：网关在路由和计费前置校验，严格拒绝在请求体中混入重复顶层 `model` 字段的畸形 JSON，从协议层杜绝参数混淆与计费穿透漏洞。
- **报文防解压炸弹与截断过滤**：严格限制解压后请求体物理尺寸，杜绝超大载荷或损坏格式消耗网关资源。
- **端点预算前置裁决**：在载荷反序列化与模型预读前，率先执行租户与端点限额预算检查。

### 3. ⚡ 服务调度与安全隔离优化 (Scheduling & Token Management)
- **OAuth 凭据刷新截止期管控（Refresh Deadline Boundaries）**：
  - 刷新逻辑严格约束于调用方截止期，防止父上下文超时时依然盲目等待上游，避免采纳过期凭据或迟到 Token 覆盖有效缓存。
  - 明确隔离内部超时与供应商故障，持久化已成功的凭据始终安全同步至内存缓存。
- **SSRF 探针硬化（SSRF-Safe Account Probes）**：
  - 账户可用性探测严格绑定经校验的解析目标，对私网 IP、链路本地及本地回环地址进行安全阻断，仅在显式受信配置下放行。
- **账户用量窗口化批量查询（Account Window Batching）**：
  - 优化高并发下基于时间窗口的账户用量与限流统计 SQL，大幅减轻高频调用下的 PostgreSQL 事务锁竞争。

### 4. 🧩 上游最新稳定功能对齐 (Upstream v0.2.18 / v0.2.19 Alignment)
- **细粒度受限管理员 API 密钥（Scoped Admin API Keys）**：
  - 支持按操作资源、分组、账户类型、IP/CIDR 掩码及过期时间配置的受限运维 Token，密钥只哈希存盘且支持二次验证（Step-up Auth），详见 [受限管理员密钥指南](docs/admin-api-keys.md)。
- **图像与绘图多模态计费**：
  - 支持 OpenAI / 复合分组专有生图端点的按图计费开关（默认关闭，按需启用），修复 Gemini fileData 图像计费核算。
- **全模型生态兼容**：
  - 补齐 DeepSeek 缓存命中定价与 Developer Role 适配；完善 Grok 状态诊断及 Gemini 同账号重试调度策略。
- **安全对齐的支付退款状态机**：
  - 适配上游退款审计幂等重试机制，有效规避退款异常重试引发的余额多扣。

---

## 🗄️ 数据库迁移规范与双 247 兼容

`v0.4.0` 相对 `v0.3.1` 引入了以下数据库向前迁移：
- `246_enable_gemini_image_generation_groups.sql`
- `247_payment_audit_refund_retry.sql`（二开退款重试部分唯一索引）
- `247_scoped_admin_api_keys.sql`（上游 Scoped 管理员密钥）
- `248_group_image_count_billing.sql`（生图计费分组）

> [!IMPORTANT]
> **双 247 迁移兼容性保障**：系统迁移引擎基于**完整文件名**与**文件校验和（Checksum）**排序，同编号不同文件名属于完全不同的迁移项。**严禁重命名或手动修改已执行的迁移文件**。迁移账本工具已覆盖测试库中的兼容性回归，存量库升级前仍须独立演练并验证可恢复备份。

---

## 📦 架构与技术栈

| 模块 | 技术选型 | 版本/说明 |
| --- | --- | --- |
| **Backend** | Go, Gin, Ent ORM | Go 1.27.3（修复依赖 CVE，静态编译） |
| **Frontend** | Vue 3, Vite, TailwindCSS | Vue 3.4+, 集成自定义 Rhine 像素交互与 Vue-I18n 11 |
| **Database** | PostgreSQL | 支持 PG 15+（容器内集成向前迁移引擎与双 247 兼容校验） |
| **Cache** | Redis | Redis 7+（用于高并发流控、OAuth 缓存与黏性会话调度） |
| **Deployment**| Docker, Docker Compose | 无 root 容器（UID 1000）、不可变 OCI 多架构镜像 |

---

## 🚢 快速实机部署

本仓库专为实机 Docker 环境优化，部署前请确保宿主机已安装 **Docker Engine (24.0+)** 与 **Docker Compose 插件**。

### 1. 获取源码与初始化环境

```bash
git clone https://github.com/y1yyum/sub2api-custom-public.git
cd sub2api-custom-public

# 复制环境变量模板
cp deploy/.env.example deploy/.env
```

编辑 `deploy/.env`，设置安全的密码与加密盐（**切勿将 `.env` 提交到公开仓库**）：
```ini
# 必须配置项
POSTGRES_PASSWORD=your_strong_db_password
JWT_SECRET=generate_a_random_jwt_secret_at_least_32_chars
TOTP_ENCRYPTION_KEY=generate_a_random_32_byte_base64_string
```

### 2. 使用官方预构建镜像运行（推荐）

直接在 `deploy/.env` 中指定当前预发布 `v0.4.0-rc.1` 镜像即可跳过本地编译：

```ini
SUB2API_IMAGE=ghcr.io/y1yyum/sub2api-custom-public:0.4.0-rc.1
```

执行容器拉取与启动：
```bash
# 验证配置并启动服务
docker compose -f deploy/docker-compose.local.yml pull sub2api
docker compose -f deploy/docker-compose.local.yml up -d --no-build

# 检查健康检查状态（正常应返回 200 HTTP OK）
curl -f http://127.0.0.1:8080/health
```

服务默认监听 `0.0.0.0:8080`，可通过反向代理（Nginx/Caddy）配置 SSL 域名访问。所有业务持久化数据安全存放于 `deploy/data/`、`deploy/postgres_data/` 与 `deploy/redis_data/`。

### 3. 本地构建并运行

如需自行基于源码构建镜像：
```bash
docker compose -f deploy/docker-compose.local.yml config
docker compose -f deploy/docker-compose.local.yml up -d --build
```

---

## 🔄 升级与运维指南

### 标准升级流程（从 v0.3.1 升至 v0.4.0）

升级前请务必完整备份 `deploy/` 下的持久化数据目录及 `.env` 配置文件：

```bash
# 1. 备份数据与配置
tar -czvf sub2api-backup-$(date +%F).tar.gz deploy/postgres_data deploy/data deploy/.env

# 2. 拉取最新主分支代码
git pull --ff-only origin main

# 3. 更新镜像并启动（应用会自动安全执行向前迁移）
docker compose -f deploy/docker-compose.local.yml pull sub2api
docker compose -f deploy/docker-compose.local.yml up -d --no-build
docker compose -f deploy/docker-compose.local.yml ps
```

### 回滚防线与注意事项

1. **切勿单方面覆盖旧数据库或直接回退旧镜像**：
   - `v0.4.0` 的退款挂起事务具备严格状态机。若盲目回退到旧版本 Worker，可能导致旧进程对挂起订单重复扣款。
   - 若系统中已创建并启用了 Scoped Admin API Keys，旧版代码不具备受限鉴权逻辑，不得在存活受限密钥的情况下回退至旧版本 Auth 模块。
2. **禁止删除数据目录**：绝不要试图通过删除 PostgreSQL 数据目录来「解决」迁移报错。如遇异常，先暂停 Worker 任务，并参考 [升级与退款放行说明](docs/upgrades/stable-0.2.18-payment.md)。

---

## 🔐 供应链安全与可验证交付

为保障生产环境交付的不可篡改性，本仓库严格执行供应链安全透明化与隔离验证：

- **自动化 CI 门禁**：PR 按改动范围快速检查，标签与手动运行检查当前公开快照的全量保留测试；安全扫描持续阻断。范围与复现见[公开仓库 CI](.github/README.md)。
- **事件驱动发行**：标签全量 Local CI 成功后，`publish.yml` 校验同一 SHA 的九项真实作业和完整收据，再刷新漏洞数据、构建实际镜像、冒烟和签名，不启动另一轮 CI。权限、源码绑定及重试规则见[发行衔接说明](.github/PUBLISHING.md)。
- **Sigstore Cosign 无密钥镜像签名**：发布镜像与 GitHub Actions 强绑定。您可在生产部署前核验镜像签名：
  ```bash
  cosign verify ghcr.io/y1yyum/sub2api-custom-public:0.4.0-rc.1 \
    --certificate-identity-regexp "https://github.com/y1yyum/sub2api-custom-public" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com"
  ```
- **SBOM 与构建来源证明**：每个正式 Release 均同步输出 CycloneDX SBOM 清单与构建来源证明文件，所有组件依赖透明可追溯。无法直连 GHCR 时，可从 Release 下载离线镜像包使用 `docker load` 导入。

---

## 📚 延伸文档导航

- 支付渠道集成与费率配置：[PAYMENT.md](docs/PAYMENT.md) | [PAYMENT_CN.md](docs/PAYMENT_CN.md)
- 受限管理员密钥规范：[admin-api-keys.md](docs/admin-api-keys.md)
- 公开源码同步历史：[UPSTREAM_SYNC.md](docs/UPSTREAM_SYNC.md)
- 合规承诺书：[管理员合规承诺 (中文)](docs/legal/admin-compliance.zh.md) | [Compliance Statement (EN)](docs/legal/admin-compliance.en.md)
- v0.4.0 升级专项指南：
  - [v0.4.0-rc.1 发行准备说明](docs/releases/v0.4.0-rc.1.md)
  - [v0.2.19 内容兼容规范](docs/upgrades/stable-0.2.19.md)
  - [图片分组策略与禁用保留](docs/upgrades/stable-0.2.18-image-groups.md)
  - [支付状态机与回滚安全手册](docs/upgrades/stable-0.2.18-payment.md)

---

## ⚖️ 许可证与免责声明

1. **开源许可证**：本项目核心沿用 [GNU LGPL v3.0](LICENSE)；第三方组件及依赖项权利归各自作者所有，详见 [NOTICE.md](NOTICE.md)。
2. **免责与合规声明**：
   - 本项目为个人技术研究与接口中继实验项目，非 upstream 官方商业产品。
   - 使用本项目需严格遵守各 AI 上游服务商（Anthropic、OpenAI、Google 等）的用户服务条款及所在地区的法律法规。
   - 请勿在 Issue、PR、日志或公开代码中提交或泄露生产凭据、数据库密码或 API 密钥。发现安全隐患请通过 GitHub 提供的 [Private Vulnerability Reporting](https://github.com/y1yyum/sub2api-custom-public/security/advisories) 入口私密提交。
