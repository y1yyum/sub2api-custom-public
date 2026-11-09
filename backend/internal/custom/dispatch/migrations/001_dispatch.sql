CREATE SEQUENCE dispatch.snapshot_revision AS BIGINT;

CREATE TABLE dispatch.cores (
    core_instance TEXT PRIMARY KEY,
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    config_revision BIGINT NOT NULL DEFAULT 0 CHECK (config_revision >= 0),
    config_digest TEXT NOT NULL DEFAULT '',
    probe_limits JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE dispatch.targets (
    core_instance TEXT NOT NULL REFERENCES dispatch.cores(core_instance),
    target_ref TEXT NOT NULL,
    metadata JSONB NOT NULL,
    health JSONB NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    PRIMARY KEY (core_instance, target_ref)
);

CREATE TABLE dispatch.observations (
    core_instance TEXT NOT NULL REFERENCES dispatch.cores(core_instance),
    event_id TEXT NOT NULL,
    payload JSONB NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (core_instance, event_id)
);

CREATE TABLE dispatch.probes (
    token TEXT PRIMARY KEY,
    core_instance TEXT NOT NULL,
    target_ref TEXT NOT NULL,
    target JSONB NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    owner TEXT NOT NULL,
    account_id BIGINT NOT NULL,
    model TEXT NOT NULL,
    region TEXT NOT NULL,
    provider_ref TEXT NOT NULL,
    slot BIGINT NOT NULL,
    attempt_id TEXT NOT NULL UNIQUE,
    reservation_id TEXT NOT NULL UNIQUE,
    reserved_at TIMESTAMPTZ NOT NULL,
    lease_until TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    FOREIGN KEY (core_instance, target_ref)
        REFERENCES dispatch.targets(core_instance, target_ref),
    UNIQUE (core_instance, account_id, model, region, slot),
    CHECK (lease_until > reserved_at)
);

CREATE INDEX probes_active ON dispatch.probes (lease_until)
    WHERE finished_at IS NULL;
CREATE INDEX probes_provider_budget ON dispatch.probes (provider_ref, reserved_at);
CREATE INDEX probes_daily_budget ON dispatch.probes (reserved_at);
