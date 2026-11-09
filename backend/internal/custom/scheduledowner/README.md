# 旧任务 owner 协议

默认关闭。只有显式启用协调的 Core 才调用本包；`ApplySchema(ctx, db)` 单独创建同一个
CoreDB 的 `scheduled_owner` schema、校验 `001` checksum。它不改旧迁移，不自动导入。
schema 内的随机 `database_id` 为库身份；克隆数据库后需隔离调度配置，不得让两库
作为同一执行目标。管理 CLI 与 dispatchd 必须先比较此身份，再以
`NewStore(db).ForDatabase(id)` 固定事务内校验。

## API 与所有权

`Import` 保留原计划、关联题库和全部结果，仅新增快照和全量结果水位。重复导入不刷新
已接管状态。`Status` 仅输出摘要；未导入但有协调运行记录的计划也输出 epoch 0。
批量导入以计划为单位提交；后项失败不撤销先前已导入项，应通过 Status 核对。
`Freeze(plan, epoch)` 立即关闭新准入并增 epoch；在途 Run 保持原 epoch 在相同 owner
的 draining 状态下完成。`Activate(plan, epoch, owner)` 必须得到同一计划 session 锁、
没有 running，且计划及题库静态指纹未变，才能增 epoch 并切换 legacy/dispatch。
`Review(plan, epoch, reason)` 仅允许在排空的 draining 状态显式接受已审查的计划/题库
变更并增 epoch；`Activate` 不会隐式接受编辑。

导入与激活均取得 per-plan 专用 `sql.Conn` session advisory 锁。启用协调后，未导入
的 legacy 计划也持同一锁到 Core 完成全部写入。`Import` 绝不越过活跃旧执行。
**无法自动证明未修补的旧镜像已停用。** 激活的外部前提是所有旧 Core 已启用协调，
或者操作者已停止旧进程并确认没有未知出站；该外部证明在本包中为 `Unknown`。

`Schedule([]Binding{PlanID, Epoch})` 排序去重，最多 1000 个绑定；冲突 epoch 拒绝。
单个计划最多一个 queued/running；全库 dispatch queued/running 最多 16。调度事务使用
ReadCommitted，在全局事务锁内重新读取 owner、epoch 与计数；冻结和激活也取得该锁，
避免等待锁时沿用旧快照。队列计数独立于主动探针预算，不代表金额预算。
Core 应让两种 owner 共用其 10 worker 上限，并为原业务查询保留数据库连接。
dispatchd 只入队，Core 才 `Acquire` 后重新读取计划、`Validate` 并执行原 kind/grade。
每次结果/副作用/进度写前应再 Validate；原服务掌握题库轮转、凭据、授权与业务状态。

`Run.Finish(ctx, resultID, err)` 必须在 Core 保存结果、应用副作用并更新进度之后调用；
无题跳过且进度成功时允许 `resultID=0, err=nil`。非空错误只保存通用分类，不保存错误原文，
保持 running 待显式处理。`Close` 只释放 session 锁，绝不因超时、断线或重启重放。
running 崩溃后必须 Freeze、确认外部出站已排空，再带原因 `AckUnknown`；未导入的
epoch 0 同样可确认。数据库锁释放并不能证明网络请求已取消。

slot 固定为原 `next_run_at` 的 UTC 分钟；唯一键为 `(plan_id, slot)`，不含 owner/epoch。
Freeze 取消未启动队列但保留 cancelled 墓碑；Unknown/完成/取消槽位均不自动重排。
若崩溃或取消前原 Core 未推进进度，旧 due 会继续阻断，必须通过原 Core 计划编辑入口
显式排期到新的合法 due，再在排空状态 Review/Activate；不能换成当前分钟绕过墓碑。
计划 cron 保持原五字段约定，每个逻辑分钟最多一次。
回退为 legacy 后仍须保持 Core 协调开关开启；关闭开关会恢复原始无 owner 栅栏及
Prune 路径，不能视为安全回滚。未修补旧镜像的直接重启不在本协议保证范围内。

## 快照与历史

静态指纹包含计划身份、模型、kind、policy_version、failure_action、题目次序、启用标志、
题干及完整判题规则；仅排除 `updated_at/last_run_at/next_run_at/rotation_cursor/
last_quiz_outcome_at` 这些 Core 正常运行进度。队列与 Run 都验证静态指纹，不能使用
过期计划缓存。执行中编辑不能撤销已经发送的外部请求；应先冻结排空，再编辑和审查。

原始导入快照单独保留；当前水位保存结果 count、maxID、最新完成时间及全量 digest。
SQL 在传到应用前逐行 SHA-256；应用流式哈希，单行上限 8 MiB、总量 256 MiB、100 万行，
任一超限拒绝，不截断为成功。maxID 仅摘要，**不是提交游标**；冻结排空后激活会重新
扫描全部已提交结果，包含晚提交的低 ID。导入时水位不代表后续最终水位。正常 Finish
同样重扫；权限或读取错误不伪装为空。新表不复制响应，只有原计划/题库快照与结果哈希。
managed 结果必须由 Core 保留历史，禁止继续 Prune。旧结果的原始表仍为唯一业务来源。

## SQL 角色

schema 应由独立迁移角色拥有。管理角色可读旧计划/题库/结果，写 owner 协议表；Core
执行角色同时保留原业务写权限。dispatchd 使用独立无 owner/超级/继承高权限的角色：

- `USAGE ON SCHEMA public, scheduled_owner`。
- `SELECT ON scheduled_owner.database_identity, scheduled_owner.schema_migrations,
  scheduled_owner.plans, scheduled_owner.runs`。
- 仅 `INSERT(plan_id,slot,epoch,owner,state,plan_digest) ON scheduled_owner.queue`。
- 为读取快照提供旧计划全部列、关联表及题库列的 SELECT；**不读取 accounts**。
- 不授予 owner 基表 INSERT/UPDATE/DELETE、schema CREATE 或旧表写权限。

`queue` 是自动更新视图，带 `security_barrier` 和 `CASCADED CHECK OPTION`，仅允许
无 result_id、无执行时间与 outcome 的 dispatch queued 行。执行器仍重新校验 epoch、
当前 owner、due、指纹和唯一 slot。最小权限由连接源初始化检查与真实 PG 测试验收；
本包不读账号凭据，不导入 Core service，也不提供部署或生产迁移授权。
