CREATE TABLE dispatch.metric_rollups (
    core_instance TEXT PRIMARY KEY REFERENCES dispatch.cores(core_instance),
    watermark TIMESTAMPTZ NOT NULL CHECK (extract(epoch FROM watermark) = floor(extract(epoch FROM watermark) / 60) * 60),
    started_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (watermark <= updated_at AND started_at <= updated_at)
);

CREATE TABLE dispatch.metric_minutes (
    core_instance TEXT NOT NULL REFERENCES dispatch.cores(core_instance),
    bucket TIMESTAMPTZ NOT NULL CHECK (extract(epoch FROM bucket) = floor(extract(epoch FROM bucket) / 60) * 60),
    series TEXT NOT NULL CHECK (series IN ('observations','probe_observations','provider_evidence','facts',
        'phases','failure_classes','probe_reservations','hold_triggers','hold_releases')),
    label TEXT NOT NULL CHECK (label ~ '^[a-z_.]{0,48}$'),
    count BIGINT NOT NULL CHECK (count > 0),
    PRIMARY KEY (core_instance, bucket, series, label)
);

CREATE TABLE dispatch.metric_hours (
    core_instance TEXT NOT NULL REFERENCES dispatch.cores(core_instance),
    bucket TIMESTAMPTZ NOT NULL CHECK (extract(epoch FROM bucket) = floor(extract(epoch FROM bucket) / 3600) * 3600),
    series TEXT NOT NULL CHECK (series IN ('observations','probe_observations','provider_evidence','facts',
        'phases','failure_classes','probe_reservations','hold_triggers','hold_releases')),
    label TEXT NOT NULL CHECK (label ~ '^[a-z_.]{0,48}$'),
    count BIGINT NOT NULL CHECK (count > 0),
    PRIMARY KEY (core_instance, bucket, series, label)
);

CREATE INDEX metric_minutes_retention ON dispatch.metric_minutes(bucket);
CREATE INDEX metric_hours_retention ON dispatch.metric_hours(bucket);
CREATE INDEX observations_retention ON dispatch.observations(core_instance, recorded_at);
CREATE INDEX probes_retention ON dispatch.probes(core_instance, reserved_at);
CREATE INDEX health_hold_audit_event ON dispatch.health_hold_audit(core_instance, event_id)
    WHERE event_id IS NOT NULL;

REVOKE ALL ON dispatch.metric_rollups, dispatch.metric_minutes, dispatch.metric_hours FROM PUBLIC;
