CREATE SCHEMA IF NOT EXISTS bridge;

CREATE TABLE IF NOT EXISTS bridge.forward_attempt (
    internal_key_id BIGINT NOT NULL CHECK (internal_key_id > 0),
    attempt_id TEXT NOT NULL,
    reservation_id TEXT NOT NULL,
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    core_request_id TEXT NOT NULL,
    request_digest CHAR(64) NOT NULL,
    envelope JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'received' CHECK (state IN ('received', 'send_intent', 'rejected')),
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    send_intent_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    PRIMARY KEY (internal_key_id, attempt_id),
    UNIQUE (reservation_id, attempt_id),
    UNIQUE (core_request_id, internal_key_id)
);
