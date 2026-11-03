-- 9008: Scheduled test kinds (probe / char_art / quiz) and an admin-managed
-- quiz question bank. Existing plans stay probes with unchanged behavior; no
-- questions are seeded.

CREATE TABLE IF NOT EXISTS intelligence_questions (
    id               BIGSERIAL PRIMARY KEY,
    prompt           TEXT             NOT NULL,
    answer_rule      VARCHAR(20)      NOT NULL,
    expected_answers JSONB            NOT NULL,
    tolerance        DOUBLE PRECISION NOT NULL DEFAULT 0,
    enabled          BOOLEAN          NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS scheduled_test_plan_questions (
    plan_id     BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    question_id BIGINT NOT NULL REFERENCES intelligence_questions(id) ON DELETE CASCADE,
    position    INT    NOT NULL DEFAULT 0,
    PRIMARY KEY (plan_id, question_id)
);
CREATE INDEX IF NOT EXISTS idx_stpq_question_id ON scheduled_test_plan_questions(question_id);

ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS kind            VARCHAR(20) NOT NULL DEFAULT 'probe',
    ADD COLUMN IF NOT EXISTS prompt          TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS pick_mode       VARCHAR(10) NOT NULL DEFAULT 'rotate',
    ADD COLUMN IF NOT EXISTS rotation_cursor INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS show_in_catalog BOOLEAN     NOT NULL DEFAULT false;

ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS kind            VARCHAR(20)  NOT NULL DEFAULT 'probe',
    ADD COLUMN IF NOT EXISTS model_id        VARCHAR(100) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS question_id     BIGINT REFERENCES intelligence_questions(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS prompt          TEXT         NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS expected_answer TEXT         NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS verdict         VARCHAR(10)  NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_str_kind_plan_created
    ON scheduled_test_results(kind, plan_id, created_at DESC)
    WHERE kind <> 'probe';
