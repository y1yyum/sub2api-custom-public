ALTER TABLE bridge.dispatch_observation
    ADD COLUMN IF NOT EXISTS failure_class TEXT NOT NULL DEFAULT ''
    CHECK (failure_class = '' OR
        (failure_class = 'quota_exhausted' AND outcome = 'upstream_rate_limit') OR
        (failure_class IN ('credential_banned', 'capability_denied') AND outcome = 'upstream_auth'));
