-- 失败退款可以重试，每次 pending/failed 都必须保留独立审计记录。
-- 其余动作继续使用 131 的订单/动作唯一约束，尤其是返利幂等标记。
-- 由 ApplyMigrations 的外层事务原子替换索引，不删除任何审计数据。
DROP INDEX IF EXISTS idx_payment_audit_logs_order_action_uniq;

CREATE UNIQUE INDEX idx_payment_audit_logs_order_action_uniq
ON payment_audit_logs(order_id, action)
WHERE action NOT IN ('REFUND_PENDING', 'REFUND_FAILED');
