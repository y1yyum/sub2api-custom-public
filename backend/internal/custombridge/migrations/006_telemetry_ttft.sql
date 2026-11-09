ALTER TABLE bridge.dispatch_observation
    ADD COLUMN IF NOT EXISTS ttft_millis BIGINT NOT NULL DEFAULT 0
    CHECK (ttft_millis BETWEEN 0 AND 900000);
