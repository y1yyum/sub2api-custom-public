# v0.2.18 图片开关迁移门禁（不是部署授权）

目标固定为 `ac1b8f1271be0d674fa6f074403c1410f30a3a81`。本迁移必须与核心图片 gate、前端配置同批合入。
正式操作需另行授权，先在脱敏现有二开数据库上演练。停止所有应用实例，冻结流量及分组管理写入，
验证数据库备份可恢复后才开始；本文命令未在真实数据库上执行。

### 246 的风险与禁用状态保留

新增 `246_enable_gemini_image_generation_groups.sql` 按原名、原内容引入。
它会把 Gemini/Antigravity **所有 false 改成 true，包括主动禁用和软删除行**；
composite 不变。它与二开 `246_random_user_id_allocation.sql` 名称不同；runner
按完整文件名及 checksum 记录，已执行 9003 不会跳过新增的低编号迁移。
不要重命名旧迁移、改写旧 SQL、删迁移记录或手动伪造 checksum。

默认放行策略应保留此前禁用意图。若希望重新开启，需由管理员明确逐组批准。
以下为 **psql 手工演练模板**，不是自动执行脚本。工作目录必须为权限 0700 的
专用备份目录，文件不得覆盖上次记录；先 `umask 077`。连接到正确的目标数据库，
在所有相关写入已冻结后捕获（保留软删除行，不加 deleted_at 条件）：

```sql
\set ON_ERROR_STOP on
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
\copy (SELECT id, platform, allow_image_generation FROM groups WHERE platform IN ('gemini', 'antigravity') AND allow_image_generation = false ORDER BY id) TO 'disabled-image-groups-before.csv' WITH (FORMAT csv, HEADER true)
\copy (SELECT filename, checksum, applied_at FROM schema_migrations ORDER BY filename) TO 'schema-migrations-before.csv' WITH (FORMAT csv, HEADER true)
COMMIT;
```

同时记录总组数及导出行数、文件 SHA256、数据库身份、时间和操作人。文件为空
必须确认是确实没有禁用组，而不是连接错库或遗漏权限。保存完整分组备份以供
独立核验。迁移后、仍未开放流量时，若策略是保留所有旧禁用状态，可采用下述
事务模板（先在副本验证，文件必须是本次同库捕获，期间不可改组/复用 ID）：

```sql
\set ON_ERROR_STOP on
BEGIN;
CREATE TEMP TABLE disabled_image_groups_before (
  id bigint PRIMARY KEY,
  platform text NOT NULL CHECK (platform IN ('gemini', 'antigravity')),
  allow_image_generation boolean NOT NULL CHECK (allow_image_generation = false)
) ON COMMIT DROP;
\copy disabled_image_groups_before FROM 'disabled-image-groups-before.csv' WITH (FORMAT csv, HEADER true)
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM disabled_image_groups_before b
    LEFT JOIN groups g ON g.id = b.id AND g.platform = b.platform
    WHERE g.id IS NULL
  ) THEN RAISE EXCEPTION 'Group identity changed; stop and reconcile'; END IF;
END $$;
UPDATE groups g SET allow_image_generation = false
FROM disabled_image_groups_before b
WHERE g.id = b.id AND g.platform = b.platform;
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM disabled_image_groups_before b JOIN groups g ON g.id = b.id
    WHERE g.allow_image_generation IS DISTINCT FROM false
  ) THEN RAISE EXCEPTION 'Disabled group restoration failed'; END IF;
END $$;
COMMIT;
```

按迁移前快照验证：所有原 false 组仍 false、原 true 不变、composite 不变；
新 246 记录恰好一次，旧迁移 filename/checksum 完全一致，随机用户 ID 触发器、
池统计和已删除 relay-monitor 状态保持。再次启动确认不会重跑 246。
直接 SQL 不触发应用缓存失效：开放前必须通过受支持的管理更新/失效流程刷新
受影响组的鉴权缓存、调度快照及各实例本地缓存，并在每个实例验证禁用 gate。
不要随意 FLUSHALL。不能证明缓存已刷新就保持入口关闭。


未完成旧库演练、禁用策略确认及每实例缓存验证不得放流量；本 PR 不宣称完成这些环境门禁。
