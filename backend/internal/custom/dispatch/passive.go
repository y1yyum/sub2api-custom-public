package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

type PassiveEvent struct {
	Observation
	CoreInstance   string
	ConfigRevision int64
	CredentialRef  string
	ProviderRef    string
	PoolRef        string
	PolicyRevision string
	Model          string
	Region         string
}

type PassiveReader struct {
	db       *sql.DB
	core     string
	revision int64
}

func NewPassiveReader(ctx context.Context, db *sql.DB, core string, revision int64) (*PassiveReader, error) {
	if db == nil || !validRef(core) || revision < 1 {
		return nil, errors.New("invalid dispatch passive source")
	}
	r := &PassiveReader{db: db, core: core, revision: revision}
	if err := r.checkPrivileges(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *PassiveReader) checkPrivileges(ctx context.Context) error {
	var unsafe bool
	err := r.db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM pg_roles p WHERE pg_has_role(current_user,p.oid,'MEMBER')
		 AND (p.rolsuper OR p.rolcreaterole OR p.rolcreatedb OR p.rolbypassrls))
		OR pg_has_role(current_user,'pg_write_all_data','MEMBER')
		OR pg_has_role(current_user,'pg_read_all_data','MEMBER')
		OR has_schema_privilege(current_user,'bridge','CREATE')
		OR NOT has_schema_privilege(current_user,'bridge','USAGE')
		OR EXISTS(SELECT 1 FROM pg_database d WHERE d.datname=current_database() AND pg_has_role(current_user,d.datdba,'MEMBER'))
		OR EXISTS(SELECT 1 FROM (VALUES ('bridge.dispatch_telemetry_source'),('bridge.dispatch_observation')) t(name)
		 WHERE NOT has_table_privilege(current_user,t.name,'SELECT')
		 OR has_table_privilege(current_user,t.name,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		 OR has_any_column_privilege(current_user,t.name,'INSERT,UPDATE,REFERENCES'))`).Scan(&unsafe)
	if err != nil || unsafe {
		return errors.New("dispatch passive source requires a restricted SELECT-only role")
	}
	return nil
}

// Read rescans a bounded recent window. Deduplication is transactional in the
// dispatch store; no sequence cursor can skip a transaction that commits late.
func (r *PassiveReader) Read(ctx context.Context, cfg Config) ([]PassiveEvent, error) {
	if cfg.Telemetry == nil || cfg.CoreInstance != r.core || cfg.ConfigRevision != r.revision {
		return nil, ErrConfigRevision
	}
	if err := r.checkPrivileges(ctx); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var now time.Time
	var core, digest string
	var revision int64
	var slotTTLSeconds int
	err = tx.QueryRowContext(ctx, `SELECT clock_timestamp(),core_instance,config_revision,config_digest,slot_ttl_seconds
	 FROM bridge.dispatch_telemetry_source WHERE singleton`).Scan(&now, &core, &revision, &digest, &slotTTLSeconds)
	if err != nil {
		return nil, errors.New("dispatch passive source identity unavailable")
	}
	if core != r.core || revision != r.revision || len(digest) != 64 || slotTTLSeconds != cfg.Telemetry.SlotTTLSeconds {
		return nil, ErrConfigRevision
	}
	timingColumn := "0::bigint"
	failureColumn := "''::text"
	timeoutColumn := "FALSE"
	protocolColumns := "''::text,''::text,FALSE"
	var hasTiming, hasFailure, hasTimeout, hasProtocol bool
	if err := tx.QueryRowContext(ctx, `SELECT
	 EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='bridge.dispatch_observation'::regclass
	 AND attname='ttft_millis' AND NOT attisdropped),
	 EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='bridge.dispatch_observation'::regclass
	 AND attname='failure_class' AND NOT attisdropped),
	 EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='bridge.dispatch_observation'::regclass
	 AND attname='timed_out' AND NOT attisdropped),
	 (SELECT count(*)=3 FROM pg_attribute WHERE attrelid='bridge.dispatch_observation'::regclass
	 AND attname IN ('phase','protocol_fact','usage_missing') AND NOT attisdropped)`).Scan(&hasTiming, &hasFailure, &hasTimeout, &hasProtocol); err != nil {
		return nil, err
	}
	if cfg.Routing != nil {
		if hasTiming {
			timingColumn = "ttft_millis"
		}
	}
	if hasFailure {
		failureColumn = "failure_class"
	}
	if hasTimeout && cfg.HealthWindow != nil {
		timeoutColumn = "timed_out"
	}
	// 未显式启用时投影旧值，保持既有观测去重 JSON 逐字不变。
	if hasProtocol && cfg.HealthWindow != nil && cfg.HealthWindow.ProtocolFacts {
		protocolColumns = "phase,protocol_fact,usage_missing"
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_id::text,core_instance,target_ref,config_revision,
	 account_id,credential_ref,credential_revision,provider_ref,pool_ref,policy_revision,model,region,
	 outcome,provider_evidence,retry_after_seconds,latency_millis,observed_at,`+timingColumn+`,`+failureColumn+`,`+timeoutColumn+`,`+protocolColumns+`
	 FROM bridge.dispatch_observation WHERE core_instance=$1 AND config_revision=$2
	 AND observed_at >= $3 AND observed_at <= $4 ORDER BY observed_at,event_id LIMIT $5`,
		r.core, r.revision, now.Add(-5*time.Minute), now, cfg.Telemetry.MaxObservations+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]PassiveEvent, 0)
	for rows.Next() {
		var event PassiveEvent
		if err := rows.Scan(&event.EventID, &event.CoreInstance, &event.TargetRef, &event.ConfigRevision,
			&event.AccountID, &event.CredentialRef, &event.CredentialRevision, &event.ProviderRef, &event.PoolRef, &event.PolicyRevision, &event.Model, &event.Region,
			&event.Outcome, &event.ProviderEvidence, &event.RetryAfterSeconds, &event.LatencyMillis, &event.At, &event.TTFTMillis, &event.FailureClass, &event.TimedOut,
			&event.Phase, &event.ProtocolFact, &event.UsageMissing); err != nil {
			return nil, err
		}
		events = append(events, event)
		if len(events) > cfg.Telemetry.MaxObservations {
			return nil, errors.New("dispatch passive observation window exceeds limit")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

func (e PassiveEvent) matches(core string, revision int64, target Target) bool {
	return e.CoreInstance == core && e.ConfigRevision == revision && e.TargetRef == target.Ref && e.AccountID == target.AccountID &&
		e.CredentialRef == target.CredentialRef && e.CredentialRevision == target.CredentialRevision && e.ProviderRef == target.ProviderRef &&
		e.PoolRef == target.PoolRef && e.PolicyRevision == target.PolicyRevision && e.Model == target.Model && e.Region == target.Region
}

func (s *Store) ObservePassive(ctx context.Context, core string, revision int64, events []PassiveEvent) (int, error) {
	if !validRef(core) || revision < 1 || len(events) > 100000 {
		return 0, errors.New("invalid dispatch passive batch")
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, core, revision); err != nil {
		return 0, err
	}
	targets, err := loadTargets(ctx, tx, core)
	if err != nil {
		return 0, err
	}
	known, err := knownPassiveObservations(ctx, tx, core, events)
	if err != nil {
		return 0, err
	}
	inserted := 0
	for _, event := range events {
		row := targets[event.TargetRef]
		// Removed targets and old credential/config generations cannot change health.
		if row == nil || !row.Target.Enabled || !event.matches(core, revision, row.Target) {
			continue
		}
		if event.At.Before(now.Add(-5 * time.Minute)) {
			continue
		}
		if err := validateObservation(event.Observation, row.Target, now); err != nil {
			return 0, err
		}
		attributable := event.Outcome == Success || event.Outcome == UpstreamAuth || event.Outcome == UpstreamRateLimit || event.Outcome == UpstreamFailure
		if event.ProviderEvidence != attributable || (event.Outcome != UpstreamRateLimit && event.RetryAfterSeconds != 0) {
			return 0, errors.New("invalid dispatch passive attribution")
		}
		observation := event.Observation
		observation.At = observation.At.UTC()
		if previous, exists := known[observation.EventID]; exists {
			if previous != observation {
				return 0, ErrObservationConflict
			}
			continue
		}
		fresh, err := recordObservation(ctx, tx, core, event.Observation, now)
		if err != nil {
			return 0, err
		}
		if !fresh {
			continue
		}
		if err := applyObservation(ctx, tx, core, targets, row.Target, event.Observation, now, observationPassive); err != nil {
			return 0, err
		}
		inserted++
		known[observation.EventID] = observation
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit dispatch passive observations: %w", err)
	}
	return inserted, nil
}

func knownPassiveObservations(ctx context.Context, tx *sql.Tx, core string, events []PassiveEvent) (map[string]Observation, error) {
	ids := make([]string, len(events))
	for i, event := range events {
		ids[i] = event.EventID
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_id,payload FROM dispatch.observations WHERE core_instance=$1 AND event_id=ANY($2)`, core, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	known := make(map[string]Observation)
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var value Observation
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		value.At = value.At.UTC()
		known[id] = value
	}
	return known, rows.Err()
}

type PassiveSource interface {
	Read(context.Context, Config) ([]PassiveEvent, error)
}
type LoadSource interface {
	Read(context.Context, []int64) (LoadSample, error)
}

type RuntimeSources struct {
	Passive   PassiveSource
	Load      LoadSource
	Quality   QualityResultSource
	Scheduled ScheduledPlanSource
}

type TelemetryReader = RuntimeSources
