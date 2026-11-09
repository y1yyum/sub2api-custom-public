CREATE TABLE dispatch.publish_lineage (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    lineage UUID NOT NULL UNIQUE,
    system_identifier BIGINT NOT NULL,
    database_oid BIGINT NOT NULL CHECK (database_oid > 0),
    reason TEXT NOT NULL CHECK (reason IN ('initial','database_identity_changed','operator_rotation')),
    started_at TIMESTAMPTZ NOT NULL
);

REVOKE ALL ON dispatch.publish_lineage FROM PUBLIC;
