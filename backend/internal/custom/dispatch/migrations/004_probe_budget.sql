CREATE TABLE dispatch.probe_budget_latch (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    first_core TEXT NOT NULL REFERENCES dispatch.cores(core_instance),
    activation_evidence_ref TEXT NOT NULL CHECK (length(activation_evidence_ref) BETWEEN 1 AND 128),
    activated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE dispatch.probe_budget_policies (
    core_instance TEXT PRIMARY KEY REFERENCES dispatch.cores(core_instance),
    active BOOLEAN NOT NULL,
    daily_microusd BIGINT NOT NULL CHECK (daily_microusd BETWEEN 1 AND 1000000000000000),
    provider_daily_microusd BIGINT NOT NULL CHECK (provider_daily_microusd BETWEEN 1 AND 1000000000000000),
    budget JSONB NOT NULL CHECK (jsonb_typeof(budget)='object' AND budget->>'currency'='USD')
);

CREATE TABLE dispatch.probe_prices (
    provider_ref TEXT NOT NULL,
    model TEXT NOT NULL,
    revision TEXT NOT NULL,
    evidence_ref TEXT NOT NULL,
    max_microusd BIGINT NOT NULL CHECK (max_microusd BETWEEN 1 AND 1000000000000),
    PRIMARY KEY (provider_ref,model,revision)
);

CREATE TABLE dispatch.probe_money (
    token TEXT PRIMARY KEY REFERENCES dispatch.probes(token),
    currency TEXT NOT NULL CHECK (currency='USD'),
    provider_ref TEXT NOT NULL,
    model TEXT NOT NULL,
    price_revision TEXT NOT NULL,
    evidence_ref TEXT NOT NULL,
    max_microusd BIGINT NOT NULL CHECK (max_microusd BETWEEN 1 AND 1000000000000),
    reserved_at TIMESTAMPTZ NOT NULL,
    budget_day DATE NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('reserved','unknown','charged')),
    version BIGINT NOT NULL CHECK (version > 0),
    settled_at TIMESTAMPTZ,
    FOREIGN KEY (provider_ref,model,price_revision) REFERENCES dispatch.probe_prices(provider_ref,model,revision),
    CHECK (budget_day=(reserved_at AT TIME ZONE 'UTC')::date),
    CHECK ((state='charged')=(settled_at IS NOT NULL))
);
CREATE INDEX probe_money_exposure ON dispatch.probe_money(provider_ref,budget_day,state);

CREATE TABLE dispatch.probe_money_audit (
    audit_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token TEXT NOT NULL REFERENCES dispatch.probe_money(token),
    version BIGINT NOT NULL CHECK (version > 0),
    action TEXT NOT NULL CHECK (action IN ('reserve','unknown','charge-ceiling','manual-close')),
    actor TEXT,
    reason TEXT,
    evidence_ref TEXT,
    request JSONB,
    result JSONB,
    recorded_at TIMESTAMPTZ NOT NULL,
    UNIQUE (token,version),
    CHECK ((action='manual-close' AND actor IS NOT NULL AND reason IS NOT NULL
        AND evidence_ref IS NOT NULL AND request IS NOT NULL AND result IS NOT NULL
        AND length(actor) BETWEEN 1 AND 128 AND octet_length(reason) BETWEEN 1 AND 512
        AND length(btrim(reason))>0 AND length(evidence_ref) BETWEEN 1 AND 128
        AND jsonb_typeof(request)='object' AND jsonb_typeof(result)='object')
        OR (action<>'manual-close' AND actor IS NULL AND reason IS NULL
        AND evidence_ref IS NULL AND request IS NULL AND result IS NULL))
);

CREATE FUNCTION dispatch.guard_probe_budget_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'dispatch probe budget history is immutable';
END;
$$;
CREATE TRIGGER probe_budget_latch_guard BEFORE UPDATE OR DELETE ON dispatch.probe_budget_latch
    FOR EACH ROW EXECUTE FUNCTION dispatch.guard_probe_budget_immutable();
CREATE TRIGGER probe_budget_latch_truncate_guard BEFORE TRUNCATE ON dispatch.probe_budget_latch
    FOR EACH STATEMENT EXECUTE FUNCTION dispatch.guard_probe_budget_immutable();
CREATE TRIGGER probe_prices_guard BEFORE UPDATE OR DELETE ON dispatch.probe_prices
    FOR EACH ROW EXECUTE FUNCTION dispatch.guard_probe_budget_immutable();
CREATE TRIGGER probe_prices_truncate_guard BEFORE TRUNCATE ON dispatch.probe_prices
    FOR EACH STATEMENT EXECUTE FUNCTION dispatch.guard_probe_budget_immutable();
CREATE TRIGGER probe_money_audit_guard BEFORE UPDATE OR DELETE ON dispatch.probe_money_audit
    FOR EACH ROW EXECUTE FUNCTION dispatch.guard_probe_budget_immutable();
CREATE TRIGGER probe_money_audit_truncate_guard BEFORE TRUNCATE ON dispatch.probe_money_audit
    FOR EACH STATEMENT EXECUTE FUNCTION dispatch.guard_probe_budget_immutable();

CREATE FUNCTION dispatch.guard_probe_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP<>'UPDATE' THEN
        RAISE EXCEPTION 'dispatch probe money cannot be removed';
    END IF;
    IF ROW(NEW.token,NEW.currency,NEW.provider_ref,NEW.model,NEW.price_revision,
        NEW.evidence_ref,NEW.max_microusd,NEW.reserved_at,NEW.budget_day)
        IS DISTINCT FROM ROW(OLD.token,OLD.currency,OLD.provider_ref,OLD.model,OLD.price_revision,
        OLD.evidence_ref,OLD.max_microusd,OLD.reserved_at,OLD.budget_day)
        OR NEW.version<>OLD.version+1 OR OLD.state='charged'
        OR (OLD.state='unknown' AND NEW.state<>'charged')
        OR (OLD.state='reserved' AND NEW.state NOT IN ('unknown','charged')) THEN
        RAISE EXCEPTION 'dispatch probe money definition or state conflict';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER probe_money_guard BEFORE UPDATE OR DELETE ON dispatch.probe_money
    FOR EACH ROW EXECUTE FUNCTION dispatch.guard_probe_money();
CREATE TRIGGER probe_money_truncate_guard BEFORE TRUNCATE ON dispatch.probe_money
    FOR EACH STATEMENT EXECUTE FUNCTION dispatch.guard_probe_money();

CREATE FUNCTION dispatch.lock_probe_budget_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('transaction_isolation')<>'read committed' THEN
        RAISE EXCEPTION 'dispatch probe writes require READ COMMITTED'
            USING ERRCODE='25000';
    END IF;
    PERFORM pg_advisory_xact_lock(813028810208);
    RETURN NEW;
END;
$$;
CREATE TRIGGER probe_budget_insert_lock BEFORE INSERT ON dispatch.probes
    FOR EACH ROW EXECUTE FUNCTION dispatch.lock_probe_budget_insert();

CREATE FUNCTION dispatch.check_probe_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM dispatch.probe_budget_latch)
        AND NOT EXISTS (SELECT 1 FROM dispatch.probe_money m WHERE m.token=NEW.token) THEN
        RAISE EXCEPTION 'dispatch probe budget requires a frozen money reservation';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER probe_money_required AFTER INSERT ON dispatch.probes
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION dispatch.check_probe_money();

REVOKE ALL ON dispatch.probe_budget_latch,dispatch.probe_budget_policies,dispatch.probe_prices,
    dispatch.probe_money,dispatch.probe_money_audit FROM PUBLIC;
REVOKE ALL ON FUNCTION dispatch.guard_probe_budget_immutable(),dispatch.guard_probe_money(),
    dispatch.lock_probe_budget_insert(),dispatch.check_probe_money() FROM PUBLIC;
