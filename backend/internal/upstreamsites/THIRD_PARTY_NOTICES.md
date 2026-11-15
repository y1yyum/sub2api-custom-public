# 来源与第三方许可

本模块及 `frontend/src/views/admin/UpstreamSitesView.vue` 移植自 [y1yyum/tokenrouter-upstream-sites](https://github.com/y1yyum/tokenrouter-upstream-sites/tree/acd9244d24a41d9527efd8c0236ed75027f95d69)，固定提交 `acd9244d24a41d9527efd8c0236ed75027f95d69`，版本 1.1.1，核对日期 2026-11-15。

插件以 LGPL-3.0-or-later 发布，许可证见 `LICENSE`；该许可证引用的 GNU GPL v3 完整文本见 `COPYING`。

账户余额解析、金额模型、统计及加密等部分代码沿用此前基于 TokenRouter 的定制实现。New API 账户余额行为对照 TokenRouter v0.7.1 的 `backend/internal/upstream/usageprovider/adapters.go` 实现，插件只采用账户余额，并使用十进制与精确整数校验。

本次改为 Sub2API 内置模块，增加 PostgreSQL 事务持久化、宿主加密与邮件服务、账号调度限制、管理员路由和二次验证、删除与凭据清除、中英文页面。

未引入 TokenRouter 插件 SDK、vendor 副本或数据库控制桥。依赖使用宿主 `backend/go.mod` 与前端现有锁文件。
