CREATE SCHEMA IF NOT EXISTS bridge;

CREATE TABLE IF NOT EXISTS bridge.receipt_outbox (
    receipt_id UUID PRIMARY KEY,
    schema_version SMALLINT NOT NULL CHECK (schema_version = 1),
    reservation_id TEXT NOT NULL CHECK (btrim(reservation_id) <> ''),
    attempt_id TEXT NOT NULL CHECK (btrim(attempt_id) <> ''),
    core_request_id VARCHAR(255) NOT NULL CHECK (btrim(core_request_id) <> ''),
    internal_key_id BIGINT NOT NULL CHECK (internal_key_id > 0),
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    user_id BIGINT NOT NULL CHECK (user_id > 0),
    request_fingerprint VARCHAR(64) NOT NULL CHECK (btrim(request_fingerprint) <> ''),
    raw_usage JSONB NOT NULL CHECK (jsonb_typeof(raw_usage) = 'object'),
    billing JSONB NOT NULL CHECK (jsonb_typeof(billing) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (core_request_id, internal_key_id),
    UNIQUE (reservation_id, attempt_id)
);
