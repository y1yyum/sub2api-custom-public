CREATE TABLE scheduled_owner.database_identity (
 singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
 database_id UUID NOT NULL UNIQUE
);
CREATE TABLE scheduled_owner.plans (
 plan_id BIGINT PRIMARY KEY CHECK(plan_id>0),
 epoch BIGINT NOT NULL CHECK(epoch>0),
 owner TEXT NOT NULL CHECK(owner IN ('legacy','dispatch')),
 state TEXT NOT NULL CHECK(state IN ('legacy','draining','dispatch')),
 snapshot JSONB NOT NULL,
 import_snapshot JSONB NOT NULL,
 plan_digest TEXT NOT NULL,
 results_count BIGINT NOT NULL,
 results_max_id BIGINT NOT NULL,
 results_latest_at TIMESTAMPTZ,
 results_digest TEXT NOT NULL,
 observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK(state='draining' OR state=owner)
);
CREATE TABLE scheduled_owner.runs (
 plan_id BIGINT NOT NULL,
 slot TIMESTAMPTZ NOT NULL,
 epoch BIGINT NOT NULL CHECK(epoch>=0),
 owner TEXT NOT NULL CHECK(owner IN ('legacy','dispatch')),
 state TEXT NOT NULL CHECK(state IN ('queued','running','completed','unknown','cancelled')),
 plan_digest TEXT NOT NULL,
 result_id BIGINT,
 outcome TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 started_at TIMESTAMPTZ,
 finished_at TIMESTAMPTZ,
 PRIMARY KEY(plan_id,slot)
);
CREATE UNIQUE INDEX one_pending_per_plan ON scheduled_owner.runs(plan_id)
 WHERE state IN ('queued','running');
CREATE INDEX queued_runs ON scheduled_owner.runs(created_at,plan_id) WHERE state='queued';
CREATE VIEW scheduled_owner.queue WITH (security_barrier=true) AS
 SELECT * FROM scheduled_owner.runs
 WHERE owner='dispatch' AND state='queued' AND result_id IS NULL
 AND outcome='' AND started_at IS NULL AND finished_at IS NULL
 WITH CASCADED CHECK OPTION;
CREATE TABLE scheduled_owner.audit (
 id BIGSERIAL PRIMARY KEY,
 plan_id BIGINT NOT NULL,
 epoch BIGINT NOT NULL,
 action TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 facts JSONB NOT NULL DEFAULT '{}',
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
