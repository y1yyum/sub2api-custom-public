-- 用 psql -X -v ON_ERROR_STOP=1 经 docker exec -i 的 stdin 执行。
-- 前置：真实 093、131 已应用；旧索引下必须因 23505 失败。
-- 应用 247 并同步 PostgreSQL 返利冲突目标后，完整回归必须通过。
-- 只操作专用事务中的审计记录；连接必须是全新隔离测试容器。
BEGIN;

-- 与现有 payment_fulfillment.go 的 PostgreSQL 占位 SQL 保持一致。
-- 先验证 131/247 都必须兼容的首次占位、重放、已有 SKIPPED 三条路径。
PREPARE affiliate_claim(text, text) AS
INSERT INTO payment_audit_logs (order_id, action, detail, operator, created_at)
SELECT $1::text, 'AFFILIATE_REBATE_APPLIED', $2::text, 'system', NOW()
WHERE NOT EXISTS (
    SELECT 1 FROM payment_audit_logs
    WHERE order_id = $1::text
      AND action IN ('AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED')
)
ON CONFLICT (order_id, action)
WHERE action NOT IN ('REFUND_PENDING', 'REFUND_FAILED')
DO NOTHING
RETURNING id;

EXECUTE affiliate_claim('refund-retry-claim-sql', '{}');
EXECUTE affiliate_claim('refund-retry-claim-sql', '{}');
INSERT INTO payment_audit_logs (order_id, action)
VALUES ('refund-retry-skipped-sql', 'AFFILIATE_REBATE_SKIPPED');
EXECUTE affiliate_claim('refund-retry-skipped-sql', '{}');

DO $$ BEGIN
    IF (SELECT COUNT(*) FROM payment_audit_logs WHERE order_id = 'refund-retry-claim-sql') <> 1
       OR (SELECT COUNT(*) FROM payment_audit_logs WHERE order_id = 'refund-retry-skipped-sql') <> 1 THEN
        RAISE EXCEPTION '返利占位或跳过不再幂等';
    END IF;
END $$;

INSERT INTO payment_audit_logs (order_id, action, detail) VALUES
    ('refund-retry-sql', 'REFUND_PENDING', 'attempt 1 pending'),
    ('refund-retry-sql', 'REFUND_FAILED', 'attempt 1 failed');

-- RED 的失败点：失败后再次发起退款，网关再次返回 pending。
INSERT INTO payment_audit_logs (order_id, action, detail) VALUES
    ('refund-retry-sql', 'REFUND_PENDING', 'attempt 2 pending'),
    ('refund-retry-sql', 'REFUND_FAILED', 'attempt 2 failed'),
    ('refund-retry-sql', 'REFUND_PENDING', 'attempt 3 pending');

DO $$
DECLARE
    protected_action text;
    violated_index text;
BEGIN
    IF (SELECT COUNT(*) FROM payment_audit_logs WHERE order_id = 'refund-retry-sql') <> 5
       OR (SELECT COUNT(DISTINCT detail) FROM payment_audit_logs WHERE order_id = 'refund-retry-sql') <> 5 THEN
        RAISE EXCEPTION '退款重试审计记录丢失';
    END IF;

    FOREACH protected_action IN ARRAY ARRAY[
        'AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED',
        'REFUND_ROLLBACK_FAILED', 'REFUND_SUCCESS', 'REFUND_REQUESTED',
        'REFUND_QUERY_PENDING', 'COMPLETED', 'FUTURE_ACTION'
    ] LOOP
        INSERT INTO payment_audit_logs (order_id, action)
        VALUES ('refund-retry-protected-sql', protected_action);
        BEGIN
            INSERT INTO payment_audit_logs (order_id, action)
            VALUES ('refund-retry-protected-sql', protected_action);
            RAISE EXCEPTION '本应阻断重复动作：%', protected_action;
        EXCEPTION WHEN unique_violation THEN
            GET STACKED DIAGNOSTICS violated_index = CONSTRAINT_NAME;
            IF violated_index <> 'idx_payment_audit_logs_order_action_uniq' THEN
                RAISE EXCEPTION '非预期唯一索引：%', violated_index;
            END IF;
        END;
        INSERT INTO payment_audit_logs (order_id, action)
        VALUES ('refund-retry-other-order-sql', protected_action);
    END LOOP;
END $$;

ROLLBACK;
