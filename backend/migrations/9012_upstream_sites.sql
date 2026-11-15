CREATE SEQUENCE IF NOT EXISTS upstream_site_ids;
CREATE TABLE IF NOT EXISTS upstream_site_records (
    collection VARCHAR(32) NOT NULL,
    key TEXT NOT NULL,
    value JSONB NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    expires_at TIMESTAMPTZ,
    PRIMARY KEY (collection, key)
);
CREATE INDEX IF NOT EXISTS upstream_site_records_expiry
    ON upstream_site_records (expires_at) WHERE expires_at IS NOT NULL;
