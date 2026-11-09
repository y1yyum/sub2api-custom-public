<div align="center">

# Sub2API Custom Public

[![Release](https://img.shields.io/badge/Release-v0.6.0-blue.svg)](https://github.com/y1yyum/sub2api-custom-public/releases)
[![Go Version](https://img.shields.io/badge/Go-1.27.3-00ADD8.svg)](https://golang.org/)
[![Vue Version](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![Docker Multi-Arch](https://img.shields.io/badge/Docker-amd64%20%7C%20arm64-2496ED.svg)](https://github.com/y1yyum/sub2api-custom-public/pkgs/container/sub2api-custom-public)
[![License: LGPL v3](https://img.shields.io/badge/License-LGPL%20v3-blue.svg)](LICENSE)

**面向高可用实机生产环境的 AI API 聚合网关与订阅分发平台（基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 二开强化版）**

[快速部署](#-快速实机部署) • [v0.6.0 核心特性](#-v060-核心特性与二开增强) • [数据库迁移](#-数据库迁移规范与向前兼容) • [升级与运维](#-升级与运维指南) • [供应链安全](#-供应链安全与可验证交付)

</div>

---

## 📖 项目简介

**Sub2API Custom Public** 是专为实机 Docker 生产环境打造的不可变 AI API 网关与配额管理平台。当前最新公开发行版本为 **v0.6.0**。

在官方 Sub2API 核心能力的基础上，本项目已深度合并上游至 `25608d216`（涵盖 v0.2.18 与 v0.2.19 核心生态兼容与安全加固），解决了高并发流式计费丢失、客户端断连漏扣、OAuth 凭据刷新悬挂、SSRF 探测逃逸及顶层模型混淆注入等关键生产缺陷。

本项目提供专属的莱茵生命（Rhine Lab）主题门户与极客像素交互；支持管理 MCP 与 MCP OAuth 授权码体系（PKCE）；内置定时智力测试（quiz）题库本地图片渲染与多模态原生图片探测，配合自动下线与隔离策略保障上游账号质量；在管理端调用日志中提供透明的流式响应诊断（用量缩放、输入变化率、流完整性）；并在 `v0.6.0` 全面重构用户门户与管理端集中检测，引入默认关闭的受信网关桥接与独立调度试点。

本仓库为完全开源的公开交付版本，剥离了内部私有直充与运维专有探针模块，**不包含任何生产凭据或私有环境依赖**，所有构建产物均通过公开 CI 门禁验证。

---

## 🚀 v0.6.0 核心特性与二开增强

### 1. 🎨 莱茵生命主题门户与交互重构 (Custom Portal & UI Refresh)
- **全新极客像素风格**：基于 `frontend/src/custom-portal/` 重构的 Rhine Lab 风格视觉体系，内嵌像素艺术图标系统（Pixel Art Icons）、定制线框与高对比度状态面板。
- **用户门户交互体验全面改版 (v0.6.0)**：
  - **登录与认证**：优化登录/注册布局与响应式体验，支持注册邮箱完整地址黑名单拦截。
  - **概览大屏**：实时指标感知（Metric Cards）、轻量级用量火花线（Usage Sparkline）、调用热力图（Heatmap）及高频快捷操作。
  - **API Key 管理**：支持批量启停/删除操作、并发上限配置，并提供多语言/主流 SDK 的集成示例代码一键复制。
  - **调用用量分析**：提供用量摘要与细项详情弹窗、Token 组成透视（输入/输出/缓存/思考），支持 CSV 分页导出并在遇到 429 限流时自动按 `Retry-After` 退避重试。
  - **钱包与账单**：重构充值与资金流水列表，直观呈现余额变动与订单状态。
  - **模型与渠道（Models & Channels）**：提供公开的模型目录、分组卡片、实时通道状态与费率看板。
- **无障碍访问性（A11y）**：Dock 导航栏全面适配键盘焦点捕捉与辅助导航。

### 2. 🛡️ 网关流式计费与异常断连零漂移保障 (Stream & Billing Hardening)
- **客户端断连单归属结算（Settle-Once）**：解决客户端过早中断（Late Disconnect）与写时网络中断下的账单归属冲突，强制执行最终 Authoritative Usage 的 Drain 与落库，杜绝 Token 用量丢失或重复扣款。
- **裸错误流（Bare SSE Errors）终止保护**：在上游返回裸错误或连接异常中止时，确保最终用量准确落库与配额结算。
- **顶层模型字段唯一性强校验（P0 Model Uniqueness Guard）**：网关在路由和计费前置校验，严格拒绝在请求体中混入重复顶层 `model` 字段的畸形 JSON，从协议层杜绝参数混淆与计费穿透漏洞。
- **报文防解压炸弹与截断过滤**：严格限制解压后请求体物理尺寸，杜绝超大载荷或损坏格式消耗网关资源。
- **端点预算前置裁决**：在载荷反序列化与模型预读前，率先执行租户与端点限额预算检查。

### 3. ⚡ 服务调度、安全隔离与 OAuth 边界管控 (Scheduling & Token Management)
- **OAuth 凭据刷新截止期管控（Refresh Deadline Boundaries）**：
  - 刷新逻辑严格约束于调用方截止期，防止父上下文超时时依然盲目等待上游，避免采纳过期凭据或迟到 Token 覆盖有效缓存。
  - 明确隔离内部超时与供应商故障，持久化已成功的凭据始终安全同步至内存缓存。
- **SSRF 探针硬化（SSRF-Safe Account Probes）**：
  - 账户可用性探测严格绑定经校验的解析目标，对私网 IP、链路本地及本地回环地址进行安全阻断，仅在显式受信配置下放行。
- **账户用量窗口化批量查询（Account Window Batching）**：
  - 优化高并发下基于时间窗口的账户用量与限流统计 SQL，大幅减轻高频调用下的 PostgreSQL 事务锁竞争。

### 4. 🧠 定时智力测试与集中管理平台 (Scheduled Intelligence Quiz & Auto-Isolation)
- **管理端集中检测中心 (v0.6.0)**：
  - 管理端可在专属的「智力检测」页面集中管理计划与题库，支持按指定账号快速筛选关联合约与测试记录。
- **题库白底黑字图片化 (v0.5.2)**：
  - 新建或编辑题目保存时，管理端本地自动渲染为黑白 PNG 图片（Data URL 存储），不调用外部生图服务。
  - 执行测试时直接以多模态图片格式发送，不携带明文题干或标准作答格式，彻底杜绝提示词泄露与上游文本提示词污染；缺图时安全拒绝测试，不降级为文本发送。
- **多模态协议原生接入 (v0.5.2)**：
  - 原生适配 Anthropic、OpenAI Responses、Chat Completions、Bedrock、Gemini、Grok 以及 Antigravity 图片格式。
  - 对别名映射到生图/生视频路由的场景执行安全拒绝，避免误判为答错或无谓消耗生图额度。
- **自动化三级故障隔离 (v0.5.0)**：
  - 支持「仅记录（默认）」、「自动禁用对应模型」与「自动禁用所属账号」三种处置策略。
  - 隔离状态按计划独立记录，复测通过仅恢复当前计划产生的限制，不干扰管理员手动禁用与其它计划。
  - 限制生效时对存活 WebSocket 会话发送 1013 指令触发重连。

### 5. 🔍 管理端调用日志流式响应诊断 (Stream Response Diagnostics)
- **原位状态直观透视 (v0.5.1)**：
  - 管理端调用日志在原 Token 单元格实时呈现用量缩放（`scaled`）、输入变化率（`input delta`）、结束原因（`finish_reason`）及流完整性标记，无需下钻即可识别上游异动。
- **详细诊断快照弹窗 (v0.5.1)**：
  - 提供初始与最终上游声明用量、思考 Token、服务档位（Service Tier）、停止序列、工具声明及推理轮次摘要等有限摘要，详见 [调用日志响应诊断说明](docs/USAGE_RESPONSE_DIAGNOSTICS.md)。
- **纯被动无感采集**：
  - 复用转发模型观察器，不增加上游请求，不持久化正文、参数或原始 SSE。

### 6. 🔌 管理 MCP 与 MCP OAuth 授权集成 (Management MCP & PKCE Auth)
- **模型上下文协议集成 (v0.5.0)**：
  - 引入官方 `modelcontextprotocol/go-sdk`（v1.4.1，已包含安全修复），提供管理端与用户端 MCP 交互能力。
- **PKCE 授权码流程 (v0.5.0)**：
  - 支持标准 MCP OAuth 2.0 公共客户端授权码授权页面；默认保持关闭，通过环境变量 `MCP_PUBLIC_URL` 安全受控开启。

### 7. 🧩 细粒度受限管理员 API 密钥 (Scoped Admin API Keys)
- **按需精准授权**：
  - 支持按操作资源、分组、账户类型、IP/CIDR 掩码及过期时间配置的受限运维 Token，密钥只哈希存盘且支持二次验证（Step-up Auth），详见 [受限管理员密钥指南](docs/admin-api-keys.md)。
- **全权限模式与用量统计 (v0.5.0)**：
  - 完善全权限模式与用量统计接口，修正权限判定边界。

### 8. 🌐 可选受信网关桥接与独立调度试点 (Optional Gateway Bridge & Dispatcher) (v0.6.0)
- **核心边界与 Core 最终否决权**：
  - 常规 Docker 镜像与官方归档仅运行 Core 主程序 `sub2api`。网关桥接与独立调度工具（`bridge-edge`、`bridge-relay`、`dispatchd`、`scheduled-owner`）作为源码级试点提供，**默认关闭，不会随镜像自动拉起**。Core 侧的原生资格检查仍为最终否决凭据。
- **受信桥接与用量回执**：
  - Core 增加受信桥接入口与固定账号转发，支持用量回执（outbox/delivery）可靠交付与端到端遥测（TTFT、超时、失败原因）。
- **多维调度算法与健康保持**：
  - 调度支持加权轮询（Weighted Round-Robin）、在途负载均衡（In-flight Load Tracking）、范围故障保持（Failure Holds）、质量评分与拥堵延期、探针预算上限、快照订阅与硬失效通知。
- **定时任务执行方解耦**：
  - 提供定时任务执行方迁移工具，支持后台定时任务的独立接管与平滑交还。
- **渠道与路由微调**：
  - OpenAI 生图请求支持按 1K / 2K / 4K 图像分辨率定向选择可用账号；Kimi 渠道并发 403 限流触发短期自愈冷却。

---

## 🗄️ 数据库迁移规范与向前兼容

系统迁移引擎基于**完整文件名**与**文件校验和（Checksum）**排序，**所有迁移均严格遵循向前兼容原则（只增不删）**。应用启动时会自动检测并执行尚未应用的迁移项。

### 完整迁移清单（至 v0.6.0）

| 版本 | 迁移文件 | 说明 | 兼容性与数据影响 |
| --- | --- | --- | --- |
| `v0.4.0` | `246_enable_gemini_image_generation_groups.sql` | 启用 Gemini/Antigravity 分组生图 | 修改已有分组布尔开关 |
| `v0.4.0` | `247_payment_audit_refund_retry.sql` | 退款审计日志重试索引重构 | 重建唯一索引，不修改行数据 |
| `v0.4.0` | `247_scoped_admin_api_keys.sql` | Scoped 管理员密钥表及索引 | 新增表与索引 |
| `v0.4.0` | `248_group_image_count_billing.sql` | 分组按出图张数计费开关 | 新增列（默认 `false`） |
| `v0.4.0` | `9008_scheduled_test_kinds.sql` | 智力测试题库与题目关联表 | 新增表与计划配置列 |
| `v0.5.0` | `249_mcp_oauth.sql` | MCP OAuth 授权记录、授权码与凭据表 | 新增表，外键引用 `users` |
| `v0.5.0` | `9009_scheduled_test_failure_action.sql` | 定时测试计划新增失败处理动作与策略版本 | 新增列（默认 `none` / `0`） |
| `v0.5.1` | `9010_usage_response_diagnostics.sql` | `usage_logs` 新增 `response_diagnostics JSONB` | 新增可空列，旧行保持 `NULL` |
| `v0.5.2` | `9011_intelligence_question_images.sql` | `intelligence_questions` 新增 `prompt_image TEXT` | 新增列（默认 `''`），旧题在保存时生成图片 |
| **`v0.6.0`** | **常规 Core 升级：无新增自动 SQL 迁移** | 沿用 v0.5.2 既有迁移，应用启动无需新 DDL | 零数据库改动，无需停机迁移 |

> [!IMPORTANT]
> **常规升级与网关扩展 SQL 边界**：
> - **常规升级**：`v0.6.0` 常规 Core 升级无需执行任何新迁移，原样保留 `v0.5.2` 数据库即可平滑启动。
> - **可选网关扩展 SQL**：桥接扩展迁移位于 `backend/internal/custombridge/migrations/`（001–009）；调度扩展迁移位于 `backend/internal/custom/dispatch/migrations/`（001–006）；定时任务执行方表结构位于 `backend/internal/custom/scheduledowner/schema.sql`。**严禁将扩展 SQL 统一盲目施加于 Core 主库**，仅在显式部署相应组件时于对应数据库应用。

---

## 📦 架构与技术栈

| 模块 | 技术选型 | 版本/说明 |
| --- | --- | --- |
| **Backend** | Go, Gin, Ent ORM | Go 1.27.3（修复依赖 CVE，静态编译） |
| **Frontend** | Vue 3, Vite, TailwindCSS | Vue 3.4+, 集成 Rhine Lab 像素交互与 Vue-I18n 11.4+ |
| **Protocols** | Model Context Protocol | `modelcontextprotocol/go-sdk` v1.4.1 (含安全加固) |
| **Database** | PostgreSQL | 支持 PG 15+（容器内集成向前迁移引擎，经 PG 18.1 集成验证） |
| **Cache** | Redis | Redis 7+ / 8.4（用于高并发流控、OAuth 缓存与黏性会话调度） |
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

# 可选配置：若需启用管理 MCP 与 MCP OAuth，配置固定的对外 HTTPS 根地址（末尾不加斜杠）
# MCP_PUBLIC_URL=https://example.com/api/v1/mcp
```

### 2. 使用官方预构建镜像运行（推荐）

直接在 `deploy/.env` 中指定 `v0.6.0` 镜像版本即可跳过本地耗时编译：

```ini
SUB2API_IMAGE=ghcr.io/y1yyum/sub2api-custom-public:0.6.0
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

### 标准升级流程（升级至 v0.6.0）

升级前请务必完整备份 `deploy/` 下的持久化数据目录及 `.env` 配置文件：

```bash
# 1. 备份数据与配置
tar -czvf sub2api-backup-$(date +%F).tar.gz deploy/postgres_data deploy/data deploy/.env

# 2. 拉取最新主分支代码
git pull --ff-only origin main

# 3. 更新或配置 deploy/.env 中的镜像版本（存在则替换，缺失则追加）
grep -q '^SUB2API_IMAGE=' deploy/.env && sed -i 's|^SUB2API_IMAGE=.*|SUB2API_IMAGE=ghcr.io/y1yyum/sub2api-custom-public:0.6.0|' deploy/.env || echo 'SUB2API_IMAGE=ghcr.io/y1yyum/sub2api-custom-public:0.6.0' >> deploy/.env

# 4. 更新镜像并启动（常规 Core 升级无新增自动 SQL 迁移）
docker compose -f deploy/docker-compose.local.yml pull sub2api
docker compose -f deploy/docker-compose.local.yml up -d --no-build
docker compose -f deploy/docker-compose.local.yml ps
```

### 关键注意事项与回滚防线

1. **存量智力题目图片生成说明**：
   - 存量题目若未包含黑白图片数据（`prompt_image` 为空），执行测试时会主动报错而不会降级为发送明文文本。
   - 建议在管理端「智力检测」题库中编辑存量题目并点击保存一次，系统将自动生成黑白 PNG 并持久化。
2. **迁移向前兼容与回滚安全约束**：
   - **数据库迁移只增不删**：换回旧镜像不会破坏已有新增表与列（例如 `response_diagnostics`、`prompt_image` 及 MCP 表会被旧镜像静默忽略）。
   - **支付与退款状态机约束**：切勿盲目回退旧版本镜像。`v0.4.0+` 的退款挂起事务具备严格状态机，挂起退款保持扣减保留状态（`DeductionHeld`）。若在存在挂起订单或退款重试时盲目切换至旧版本 Worker，可能导致旧进程对订单重复扣款；回滚前必须暂停支付/退款 Worker 并核对订单对账状态，详见 [支付状态机手册](docs/upgrades/stable-0.2.18-payment.md)。
   - **业务逻辑与鉴权依赖**：旧镜像不具备智力测试自动化限制逻辑，若回滚至旧版，被隔离的账号会重新参与调度；若存量环境中存在已启用的 Scoped Admin API Keys，旧版代码不具备受限鉴权逻辑，禁止在存活受限密钥的情况下回退至旧版本 Auth 模块。
3. **可选网关试点与定时任务回滚流程 (v0.6.0)**：
   - 未启用网关试点时，可直接安全回退旧版本镜像；
   - 若已启用网关试点，回退前须先停止新请求准入，排空在途请求与待交付回执；
   - 已迁移的定时任务执行方须严格按「冻结任务 → 排空在途 → 移交回原执行方」的顺序回退，保留账本与审计数据。
4. **禁止删除数据目录**：
   - 绝不要试图通过删除 PostgreSQL 数据目录来「解决」迁移报错。如遇异常，先暂停 Worker 任务，并参考升级专项说明。

---

## 🔐 供应链安全与可验证交付

为保障生产环境交付的可靠性与透明度，本仓库严格执行不可变交付契约：

- **自动化 CI 门禁**：每个 PR 必须通过包含 Go 单元测试、集成测试、前端 Lint & 关键 Vitest、golangci-lint 及变更集密钥扫描的完整门禁闭包。
- **不可变 OCI 多架构镜像**：
  - 镜像地址：`ghcr.io/y1yyum/sub2api-custom-public:0.6.0`（支持 `linux/amd64` 与 `linux/arm64`）。
  - 生产环境建议通过不可变 Digest 进行精准部署。
- **官方二进制归档与校验和**：
  - 每个正式 Release 均由 GitHub Actions 官方流水线通过 GoReleaser 自动构建多平台二进制归档包，并同步提供 `checksums.txt`（SHA256）校验清单。

---

## 📚 延伸文档导航

- 支付渠道集成与费率配置：[PAYMENT.md](docs/PAYMENT.md) | [PAYMENT_CN.md](docs/PAYMENT_CN.md)
- 受限管理员密钥规范：[admin-api-keys.md](docs/admin-api-keys.md)
- 调用日志响应诊断说明：[USAGE_RESPONSE_DIAGNOSTICS.md](docs/USAGE_RESPONSE_DIAGNOSTICS.md)
- 公开源码同步历史：[UPSTREAM_SYNC.md](docs/UPSTREAM_SYNC.md)
- 合规承诺书：[管理员合规承诺 (中文)](docs/legal/admin-compliance.zh.md) | [Compliance Statement (EN)](docs/legal/admin-compliance.en.md)
- 发行版本说明：
  - [v0.6.0 发行说明](docs/releases/v0.6.0.md)
  - [v0.5.1 发行说明](docs/releases/v0.5.1.md)
  - [v0.4.0-rc.1 发行准备说明](docs/releases/v0.4.0-rc.1.md)
- 升级兼容专项指南：
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
