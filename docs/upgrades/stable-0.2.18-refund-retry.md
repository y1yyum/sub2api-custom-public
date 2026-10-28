# 退款重试迁移 247 与验证记录

2026-10-28 完成源码修复及隔离 SQL 验证，等待独立审查和候选真实 CI。
247 仅放宽两个退款审计动作；生产 Go 仅在 PostgreSQL 返利占位冲突目标补上
相同 predicate。保留冲突列及 `DO NOTHING`，SQLite 分支保持原 SQL。
本记录不替代 CI、独立批准或部署授权。

## 缺陷分类与可达路径

当前证据支持**功能正确性及退款恢复缺陷**，未发现未授权利用路径。
`routes/payment.go` 的退款执行、查询入口受 `adminAuth` 保护；管理员页面和
`PrepareRefund` 均允许 `REFUND_FAILED` 再次退款。这个结论不等于全面安全审计。
当前没有发现需要额外私密安全披露流程的证据；若后续发现未授权路径，应停止公开细节。

原有链路：首次 pending 写入审计 → 查询失败并归还持有扣减 → 状态变为
`REFUND_FAILED` → 管理员重试 → 网关再次 pending。`ExecuteRefund` 先提交
`REFUNDING` 和扣减，`markRefundPending` 再以事务写入 pending 状态及审计。
131 对所有 `(order_id, action)` 建立唯一索引，第二条 `REFUND_PENDING` 返回
`23505`，后一个事务回滚，订单仍停在此前提交的 `REFUNDING`。再次失败也需要
第二条 `REFUND_FAILED`，因此必须同时覆盖这两个动作。

## 最小修复及返利兼容性

[247](../../backend/migrations/247_payment_audit_refund_retry.sql) 仅将
`REFUND_PENDING`、`REFUND_FAILED` 排除在原名唯一索引之外；其他动作和未来新动作
继续唯一，不删除审计、不改历史迁移。迁移由 `ApplyMigrations` 的外层事务包裹。

不放宽 `REFUND_ROLLBACK_FAILED`：现有 `ExecuteRefund` 遇到该标记会跳过再次扣减，
并把计划扣减量置零；尚无必须重复该标记的可达证据。负向回归继续约束它。

仅替换索引时，原返利 `ON CONFLICT (order_id, action) DO NOTHING` 无法推断
partial unique index，首次占位报 `42P10`。普通 INSERT 拒绝重复不能证明 upsert
可用。必要联动位于 `backend/internal/service/payment_fulfillment.go:853`，仅将
PostgreSQL 冲突目标改为：

```sql
ON CONFLICT (order_id, action)
WHERE action NOT IN ('REFUND_PENDING', 'REFUND_FAILED')
DO NOTHING
```

该 predicate 与 247 完全一致；131 原全局索引也能匹配新目标。
SQLite 单元测试仍自建全局唯一索引，继续使用原无 predicate 的冲突目标。

| 源码与索引组合 | 返利首次 / 重放 / 已有 SKIPPED | 退款重试 |
| --- | --- | --- |
| 原源码 + 131 | 原有行为 | 原缺陷，`23505` |
| 新源码 + 131 | 实测 1 / 0 / 0 行 | 原索引仍阻断 |
| 新源码 + 247 | 实测 1 / 0 / 0 行 | 允许重复 pending/failed |
| 原源码 + 247 | 实测首次即 `42P10`，禁止使用 | 不得以此组合运行 |

## TDD 与实测结果

先新增 repository 集成回归及纯 SQL 回归，再创建 247；发现返利兼容问题后，
先新增构建器/SQLite 单测并复现原构建器 SQL 的 `42P10`，再修改 PostgreSQL 分支。
全新容器使用本机已有 `postgres:18.1-alpine3.23`，镜像 ID：
`sha256:aa6eb304ddb6dd26df23d05db4e5cb05af8951cda3e0dc57731b771e0ef4ab29`。
容器 `--network none`、非 root、只读根文件系统、移除全部 capabilities；
仅容器内 tmpfs 可写，无主机文件、socket、数据卷挂载或端口发布。
PostgreSQL `listen_addresses=''`，仅容器内 Unix socket；使用一次性测试角色，
不使用真实凭据。全部 SQL 经 `docker exec -i` 的 stdin 输入。

为执行真实 093、131 文件，仅预建其依赖的空 `settings`、`users`、`payment_orders`
最小表；这项 SQL 试验不替代完整迁移链验证。迁移前另保存 3 条审计的逐列快照。

| 验证 | 实测结果 |
| --- | --- |
| 原始 093、131 + 退款重试回归 | RED：第二条 pending 命中原索引，`23505`，psql 退出 3 |
| 事务应用 247 + 退款重试/普通重复约束回归 | GREEN：5 条重试事件保留，8 类受保护动作拒绝重复；不同订单允许相同动作 |
| 迁移前后审计全列双向 EXCEPT | GREEN：原 3 条记录完全保留 |
| 247 + 原构建器占位 SQL | RED：首次返利占位 `42P10`，psql 退出 3 |
| 新构建器 SQL + 131 原索引 | GREEN：返利三路径 1/0/0；之后退款重试仍按预期报 `23505` |
| 新构建器 SQL + 247 的完整 SQL 回归 | GREEN：返利三路径 1/0/0，退款重试及 8 类保护动作全部通过；退出 0 |
| SQLite 3.50.4 + 自建全局唯一索引 + 实际 SQLite SQL 模板 | GREEN：首次 1 行、重放 0 行、已有 SKIPPED 时 0 行，最终仅 2 条标记 |

[完整 SQL 回归](../../scripts/verify-refund-retry.sql) 的返利片段、repository 回归片段
均与实际 PostgreSQL 构建器模板逐词比对，时间表达式按构建器取 `NOW()`。
SQLite 实验执行从实际分支提取、取 `CURRENT_TIMESTAMP` 的 SQL 模板；这里没有
把 Python 执行 SQL 模板冒称为执行 Go 构建器单测。旧索引复验仅在事务内临时重建
真实 131 索引，连接退出时回滚 DDL；全程未访问其他数据库。

## Go 回归质量与验证边界

[新增集成测试](../../backend/internal/repository/payment_refund_retry_integration_test.go)
使用 `integration` build tag、真实 `integrationDB` 和 TestMain 的 `ApplyMigrations`；
不自行建表、不用 ent 自动 schema 或 SQLite 替代迁移。公开验证流程需按固定 manifest
恢复已有 TestMain；隔离适配只替换数据库/Redis 启动方式，仍执行真实迁移。

覆盖重复 pending/failed 的完整事件保留、返利实际占位 SQL 首次/重复/SKIPPED 三条路径，
以及 8 类动作的唯一约束。拒绝重复时必须收到 `pq.Error`、`23505` 及原索引名；
用 savepoint 恢复事务，不将任意数据库错误视为幂等成功。每个子测试事务均回滚。
另有[构建器与 SQLite 单测](../../backend/internal/service/payment_affiliate_claim_query_test.go)：
校验 PostgreSQL 冲突目标及参数，实际调用 SQLite 的占位构建器/执行路径，覆盖首次、重放、
已有 SKIPPED 及最终标记数。自建 SQLite schema 保留原全局唯一索引。
已检查 Go 语法、gofmt 及返利 SQL 与生产生成器的形状一致性。

**未运行 Go unit/integration，也未运行完整后端 suite。** 当前不能宣称 Go 测试或 CI 通过。
源码修复及独立审查完成后，应由真实隔离 CI 执行完整迁移链和此新增回归；只跑该回归时
匹配 `^TestPaymentAuditLogRefundRetryConstraints$`，必须保留真实 TestMain。

## 复验方式及部署限制

新建同等隔离容器并实际应用 093、131 后，将文件原样从 stdin 输入：

```powershell
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)
[System.IO.File]::ReadAllText((Join-Path $PWD 'scripts/verify-refund-retry.sql')) |
  docker exec -i <仅本次创建的隔离容器名> psql -X -U refund_fixture -d refund_retry -v ON_ERROR_STOP=1 -v VERBOSITY=verbose
```

原索引下返利三路径应通过，随后退款重试预期 `23505`。通过 stdin 在显式事务中执行
247 后，同一完整脚本应通过。构建器单测可匹配
`^(TestBuildAffiliateClaimQueryPostgresPartialIndex|TestAffiliateClaimSQLiteFullUniqueIndex)$`；
这些定向 Go 测试也必须在隔离 runner 内执行，不用裸后端 suite 替代。
测试脚本成功时主动回滚；失败时 psql 连接退出也回滚，不保留测试审计记录。

即使源码门禁未来通过，本次也不授权部署或生产数据库操作。
**247 后禁止旧镜像回滚和新旧支付 worker 混跑**；旧返利 SQL 与新索引不兼容，
且旧退款代码也未必兼容持有扣减语义。新镜像在旧索引上通过返利验收，并不撤销这条限制。
索引重建会扫描并锁定审计表，
必须沿用[支付门禁](stable-0.2.18-payment.md)冻结写入、停止旧 worker、备份与逐单对账，
同时满足迁移 246 的[图片分组门禁](stable-0.2.18-image-groups.md)。
247 不修复已卡住的订单；应先核对支付机构结果，再人工处置，不得盲目重发退款。
产生重复退款审计后，旧全局唯一索引无法直接恢复；禁止去重或删除审计来强行回退。
