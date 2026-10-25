CREATE TABLE IF NOT EXISTS relay_monitor_stations (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(200) NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    type VARCHAR(32) NOT NULL CHECK (type IN ('newapi','newapi-key','sub2api','sub2api-password','jucode','monoize','monoize-key','fixed')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    cny_per_usd NUMERIC(24,8) NOT NULL DEFAULT 1 CHECK (cny_per_usd > 0),
    balance_mode VARCHAR(16) NOT NULL DEFAULT 'shared' CHECK (balance_mode IN ('shared','independent','quota')),
    balance_token_id BIGINT NULL,
    low_balance NUMERIC(24,8) NOT NULL DEFAULT 5 CHECK (low_balance >= 0),
    no_renewal BOOLEAN NOT NULL DEFAULT FALSE,
    include_in_cost BOOLEAN NOT NULL DEFAULT TRUE,
    subject_id BIGINT NULL,
    channel_aliases JSONB NOT NULL DEFAULT '[]',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS relay_monitor_tokens (
    id BIGSERIAL PRIMARY KEY,
    station_id BIGINT NOT NULL REFERENCES relay_monitor_stations(id) ON DELETE CASCADE,
    name VARCHAR(200) NOT NULL,
    account_id BIGINT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    cny_per_usd_override NUMERIC(24,8) NULL CHECK (cny_per_usd_override > 0),
    credential_encrypted TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (account_id IS NULL OR credential_encrypted = '')
);
CREATE INDEX IF NOT EXISTS relay_monitor_tokens_station_idx ON relay_monitor_tokens(station_id);
CREATE UNIQUE INDEX IF NOT EXISTS relay_monitor_tokens_account_idx ON relay_monitor_tokens(account_id) WHERE account_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS relay_monitor_snapshots (
    id BIGSERIAL PRIMARY KEY,
    station_id BIGINT NOT NULL REFERENCES relay_monitor_stations(id) ON DELETE CASCADE,
    token_id BIGINT NOT NULL REFERENCES relay_monitor_tokens(id) ON DELETE CASCADE,
    checked_at TIMESTAMPTZ NOT NULL,
    ok BOOLEAN NOT NULL,
    error VARCHAR(200) NOT NULL DEFAULT '',
    latency_ms BIGINT NOT NULL DEFAULT 0,
    cny_per_usd NUMERIC(24,8) NOT NULL,
    remaining_cny NUMERIC(40,16) NULL,
    today_used_cny NUMERIC(40,16) NULL,
    result JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS relay_monitor_snapshots_station_time_idx ON relay_monitor_snapshots(station_id,checked_at DESC);
CREATE INDEX IF NOT EXISTS relay_monitor_snapshots_token_time_idx ON relay_monitor_snapshots(token_id,checked_at DESC);
CREATE TABLE IF NOT EXISTS relay_monitor_rate_changes (
    id BIGSERIAL PRIMARY KEY,
    station_id BIGINT NOT NULL REFERENCES relay_monitor_stations(id) ON DELETE CASCADE,
    token_id BIGINT NULL REFERENCES relay_monitor_tokens(id) ON DELETE CASCADE,
    old_rate NUMERIC(24,8) NULL,
    new_rate NUMERIC(24,8) NULL,
    effective_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS relay_monitor_rate_changes_time_idx ON relay_monitor_rate_changes(station_id,effective_at);
CREATE TABLE IF NOT EXISTS relay_monitor_meta (key TEXT PRIMARY KEY, value JSONB NOT NULL);
