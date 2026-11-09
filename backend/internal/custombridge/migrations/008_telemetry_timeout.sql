ALTER TABLE bridge.dispatch_observation
    ADD COLUMN IF NOT EXISTS timed_out BOOLEAN NOT NULL DEFAULT FALSE
    CHECK (NOT timed_out OR (outcome = 'upstream_failure' AND provider_evidence));
