CREATE TABLE bridge_shadow.identity (
    singleton BOOLEAN PRIMARY KEY CHECK (singleton),
    sink_id UUID NOT NULL UNIQUE DEFAULT gen_random_uuid()
);
INSERT INTO bridge_shadow.identity(singleton) VALUES (true);

CREATE TABLE bridge_shadow.inbox (
    source TEXT NOT NULL CHECK (btrim(source) <> ''),
    event_id UUID NOT NULL,
    payload_digest CHAR(64) NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (source, event_id)
);

CREATE TABLE bridge_shadow.shadow_receipt_ledger (
    source TEXT NOT NULL,
    event_id UUID NOT NULL,
    payload_digest CHAR(64) NOT NULL,
    receipt_raw TEXT NOT NULL,
    receipt JSONB NOT NULL CHECK (jsonb_typeof(receipt) = 'object'),
    committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (source, event_id),
    FOREIGN KEY (source, event_id) REFERENCES bridge_shadow.inbox(source, event_id),
    CHECK (receipt = receipt_raw::jsonb)
);

CREATE TABLE bridge_shadow.inbox_conflicts (
    source TEXT NOT NULL,
    event_id UUID NOT NULL,
    existing_digest CHAR(64) NOT NULL,
    received_digest CHAR(64) NOT NULL,
    receipt_raw TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    occurrences BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (source, event_id, received_digest),
    FOREIGN KEY (source, event_id) REFERENCES bridge_shadow.inbox(source, event_id)
);
