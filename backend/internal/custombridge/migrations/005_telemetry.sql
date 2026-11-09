CREATE SCHEMA IF NOT EXISTS bridge;

CREATE TABLE IF NOT EXISTS bridge.dispatch_telemetry_source (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    core_instance TEXT NOT NULL CHECK (core_instance ~ '^[A-Za-z0-9_.:-]{1,128}$'),
    config_revision BIGINT NOT NULL CHECK (config_revision > 0),
    config_digest CHAR(64) NOT NULL CHECK (config_digest ~ '^[0-9a-f]{64}$'),
    slot_ttl_seconds INTEGER NOT NULL CHECK (slot_ttl_seconds BETWEEN 1 AND 86400),
    activated_at TIMESTAMPTZ NOT NULL,
    producer_generation UUID NOT NULL
);

CREATE TABLE IF NOT EXISTS bridge.dispatch_observation (
    event_id UUID PRIMARY KEY,
    core_instance TEXT NOT NULL CHECK (length(core_instance) BETWEEN 1 AND 128),
    target_ref TEXT NOT NULL CHECK (length(target_ref) BETWEEN 1 AND 128),
    config_revision BIGINT NOT NULL CHECK (config_revision > 0),
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    credential_ref TEXT NOT NULL CHECK (length(credential_ref) BETWEEN 1 AND 128),
    credential_revision BIGINT NOT NULL CHECK (credential_revision > 0),
    provider_ref TEXT NOT NULL CHECK (length(provider_ref) BETWEEN 1 AND 128),
    pool_ref TEXT NOT NULL CHECK (length(pool_ref) BETWEEN 1 AND 128),
    policy_revision TEXT NOT NULL CHECK (length(policy_revision) BETWEEN 1 AND 128),
    model TEXT NOT NULL CHECK (length(model) BETWEEN 1 AND 255),
    region TEXT NOT NULL CHECK (length(region) BETWEEN 1 AND 128),
    outcome TEXT NOT NULL CHECK (outcome IN ('success', 'upstream_auth', 'upstream_rate_limit', 'upstream_failure', 'unknown', 'local_rejection', 'client_canceled')),
    provider_evidence BOOLEAN NOT NULL,
    retry_after_seconds INTEGER NOT NULL CHECK (retry_after_seconds BETWEEN 0 AND 600),
    latency_millis BIGINT NOT NULL CHECK (latency_millis BETWEEN 0 AND 900000),
    observed_at TIMESTAMPTZ NOT NULL,
    internal_key_id BIGINT NOT NULL CHECK (internal_key_id > 0),
    attempt_id TEXT NOT NULL CHECK (length(attempt_id) BETWEEN 1 AND 128),
    core_request_id TEXT NOT NULL CHECK (length(core_request_id) BETWEEN 1 AND 255),
    producer_generation UUID NOT NULL,
    UNIQUE (internal_key_id, attempt_id),
    CHECK (provider_evidence = (outcome IN ('success', 'upstream_auth', 'upstream_rate_limit', 'upstream_failure'))),
    CHECK (outcome = 'upstream_rate_limit' OR retry_after_seconds = 0)
);

CREATE INDEX IF NOT EXISTS dispatch_observation_recent_idx
    ON bridge.dispatch_observation (observed_at, event_id);
