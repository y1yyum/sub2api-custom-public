-- 智力测试隔离独立于管理员开关；旧计划默认仅记录。
ALTER TABLE scheduled_test_plans
    ADD COLUMN failure_action VARCHAR(32) NOT NULL DEFAULT 'none',
    ADD COLUMN policy_version BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN last_quiz_outcome_at TIMESTAMPTZ,
    ADD CONSTRAINT scheduled_test_failure_action_check
        CHECK (failure_action IN ('none', 'disable_model', 'disable_account')
            AND (kind = 'quiz' OR failure_action = 'none'));
