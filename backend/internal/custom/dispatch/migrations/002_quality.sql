CREATE TABLE dispatch.quality_configs (
    core_instance TEXT PRIMARY KEY REFERENCES dispatch.cores(core_instance),
    config_revision BIGINT NOT NULL CHECK (config_revision > 0),
    baseline_digest TEXT NOT NULL CHECK (length(baseline_digest) = 64),
    activated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE dispatch.quality_baselines (
    core_instance TEXT NOT NULL REFERENCES dispatch.cores(core_instance),
    baseline_revision TEXT NOT NULL,
    baseline_digest TEXT NOT NULL CHECK (length(baseline_digest) = 64),
    PRIMARY KEY (core_instance, baseline_revision)
);

CREATE TABLE dispatch.quality_results (
    core_instance TEXT NOT NULL REFERENCES dispatch.cores(core_instance),
    config_revision BIGINT NOT NULL CHECK (config_revision > 0),
    result_id BIGINT NOT NULL CHECK (result_id > 0),
    plan_id BIGINT NOT NULL CHECK (plan_id > 0),
    target_ref TEXT NOT NULL,
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    credential_revision BIGINT NOT NULL CHECK (credential_revision > 0),
    model TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('char_art','quiz')),
    question_ref TEXT NOT NULL,
    source_digest TEXT NOT NULL CHECK (length(source_digest) = 64),
    score JSONB NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (core_instance, config_revision, result_id),
    CHECK (finished_at >= started_at)
);

CREATE INDEX quality_results_recent ON dispatch.quality_results
    (core_instance, config_revision, finished_at DESC, result_id DESC);
CREATE INDEX quality_results_retention ON dispatch.quality_results(finished_at);
