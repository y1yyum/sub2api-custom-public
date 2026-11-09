CREATE TABLE bridge.relay_source_identity (
    singleton BOOLEAN PRIMARY KEY CHECK (singleton),
    source_id TEXT NOT NULL CHECK (btrim(source_id) <> '')
);

CREATE TABLE bridge.receipt_consumers (
    consumer_id TEXT PRIMARY KEY CHECK (btrim(consumer_id) <> ''),
    source_id TEXT NOT NULL,
    sink_id UUID NOT NULL,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE bridge.receipt_delivery (
    consumer_id TEXT NOT NULL REFERENCES bridge.receipt_consumers(consumer_id),
    receipt_id UUID NOT NULL REFERENCES bridge.receipt_outbox(receipt_id),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'leased', 'acked', 'dead_letter')),
    lease_token UUID,
    lease_generation BIGINT NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    lease_until TIMESTAMPTZ,
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    total_attempts BIGINT NOT NULL DEFAULT 0 CHECK (total_attempts >= 0),
    replay_count INTEGER NOT NULL DEFAULT 0 CHECK (replay_count >= 0),
    last_error_code TEXT NOT NULL DEFAULT '',
    dead_letter_at TIMESTAMPTZ,
    acked_at TIMESTAMPTZ,
    PRIMARY KEY (consumer_id, receipt_id),
    CHECK ((state = 'leased') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK ((state = 'acked') = (acked_at IS NOT NULL)),
    CHECK ((state = 'dead_letter') = (dead_letter_at IS NOT NULL))
);

CREATE INDEX receipt_delivery_ready_idx ON bridge.receipt_delivery
    (consumer_id, next_retry_at, receipt_id) WHERE state IN ('pending', 'leased');

CREATE TABLE bridge.receipt_delivery_audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    consumer_id TEXT NOT NULL,
    receipt_id UUID NOT NULL,
    lease_generation BIGINT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('claimed', 'acked', 'retry', 'dead_letter', 'replay')),
    reason TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (consumer_id, receipt_id) REFERENCES bridge.receipt_delivery(consumer_id, receipt_id)
);
