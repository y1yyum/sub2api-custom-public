CREATE TABLE dispatch.health_holds (
    core_instance TEXT NOT NULL REFERENCES dispatch.cores(core_instance),
    hold_id TEXT NOT NULL CHECK (length(hold_id) BETWEEN 1 AND 128),
    class TEXT NOT NULL CHECK (class IN ('quota_exhausted','credential_banned','capability_denied')),
    scope JSONB NOT NULL CHECK (jsonb_typeof(scope)='object'),
    active BOOLEAN NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    config_revision BIGINT NOT NULL CHECK (config_revision > 0),
    trigger_event_id TEXT NOT NULL CHECK (length(trigger_event_id) BETWEEN 1 AND 128),
    triggered_at TIMESTAMPTZ NOT NULL,
    released_at TIMESTAMPTZ,
    PRIMARY KEY (core_instance,hold_id),
    CHECK (active = (released_at IS NULL)),
    CHECK (COALESCE(
        (class='credential_banned' AND scope->>'kind'='credential'
            AND length(scope->>'provider_ref') BETWEEN 1 AND 128 AND length(scope->>'credential_ref') BETWEEN 1 AND 128)
        OR (class='quota_exhausted' AND scope->>'kind'='quota'
            AND length(scope->>'provider_ref') BETWEEN 1 AND 128 AND length(scope->>'quota_ref') BETWEEN 1 AND 128)
        OR (class IN ('quota_exhausted','capability_denied') AND scope->>'kind'='account_model_region'
            AND jsonb_typeof(scope->'account_id')='number' AND (scope->>'account_id')::bigint>0
            AND length(scope->>'model') BETWEEN 1 AND 255 AND length(scope->>'region') BETWEEN 1 AND 128), FALSE))
);

CREATE TABLE dispatch.health_hold_audit (
    audit_id TEXT PRIMARY KEY CHECK (length(audit_id) BETWEEN 1 AND 128),
    core_instance TEXT NOT NULL,
    hold_id TEXT NOT NULL,
    hold_revision BIGINT NOT NULL CHECK (hold_revision > 0),
    action TEXT NOT NULL CHECK (action IN ('trigger','release')),
    config_revision BIGINT NOT NULL CHECK (config_revision > 0),
    event_id TEXT,
    operation_id TEXT,
    actor TEXT,
    reason TEXT,
    evidence_ref TEXT,
    request JSONB,
    release_report JSONB,
    recorded_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (core_instance,hold_id) REFERENCES dispatch.health_holds(core_instance,hold_id),
    UNIQUE (core_instance,hold_id,hold_revision),
    CHECK ((action='trigger' AND event_id IS NOT NULL AND length(event_id) BETWEEN 1 AND 128
        AND operation_id IS NULL AND actor IS NULL AND reason IS NULL AND evidence_ref IS NULL
        AND request IS NULL AND release_report IS NULL)
        OR (action='release' AND event_id IS NULL
        AND operation_id IS NOT NULL AND length(operation_id) BETWEEN 1 AND 128
        AND actor IS NOT NULL AND length(actor) BETWEEN 1 AND 128
        AND reason IS NOT NULL AND octet_length(reason) BETWEEN 1 AND 512 AND length(btrim(reason))>0
        AND evidence_ref IS NOT NULL AND length(evidence_ref) BETWEEN 1 AND 128
        AND request IS NOT NULL AND release_report IS NOT NULL
        AND jsonb_typeof(request)='object' AND jsonb_typeof(release_report)='object'))
);

CREATE UNIQUE INDEX health_hold_operation ON dispatch.health_hold_audit(core_instance,operation_id)
    WHERE operation_id IS NOT NULL;
CREATE INDEX health_hold_audit_lookup ON dispatch.health_hold_audit(core_instance,hold_id,hold_revision DESC)
    WHERE action='release';

CREATE FUNCTION dispatch.guard_health_hold() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP<>'UPDATE' THEN
        RAISE EXCEPTION 'dispatch health hold history cannot be removed';
    END IF;
    IF ROW(NEW.core_instance,NEW.hold_id,NEW.class,NEW.scope)
        IS DISTINCT FROM ROW(OLD.core_instance,OLD.hold_id,OLD.class,OLD.scope)
        OR NEW.revision<>OLD.revision+1 THEN
        RAISE EXCEPTION 'dispatch health hold definition or revision conflict';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER health_hold_guard BEFORE UPDATE OR DELETE ON dispatch.health_holds
    FOR EACH ROW EXECUTE FUNCTION dispatch.guard_health_hold();
CREATE TRIGGER health_hold_truncate_guard BEFORE TRUNCATE ON dispatch.health_holds
    FOR EACH STATEMENT EXECUTE FUNCTION dispatch.guard_health_hold();

CREATE FUNCTION dispatch.guard_health_hold_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'dispatch health hold audit is append only';
END;
$$;
CREATE TRIGGER health_hold_audit_guard BEFORE UPDATE OR DELETE ON dispatch.health_hold_audit
    FOR EACH ROW EXECUTE FUNCTION dispatch.guard_health_hold_audit();
CREATE TRIGGER health_hold_audit_truncate_guard BEFORE TRUNCATE ON dispatch.health_hold_audit
    FOR EACH STATEMENT EXECUTE FUNCTION dispatch.guard_health_hold_audit();

REVOKE ALL ON dispatch.health_holds,dispatch.health_hold_audit FROM PUBLIC;
REVOKE ALL ON FUNCTION dispatch.guard_health_hold(),dispatch.guard_health_hold_audit() FROM PUBLIC;
