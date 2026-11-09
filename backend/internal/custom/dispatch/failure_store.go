package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

func loadHealthHolds(ctx context.Context, tx *sql.Tx, core string) ([]HealthHoldReport, error) {
	rows, err := tx.QueryContext(ctx, `SELECT hold_id,class,scope,active,revision,config_revision,
	 trigger_event_id,triggered_at,released_at FROM dispatch.health_holds WHERE core_instance=$1 ORDER BY hold_id LIMIT $2`, core, MaxHealthHolds+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	holds := []HealthHoldReport{}
	for rows.Next() {
		h := HealthHoldReport{CoreInstance: core, Targets: []Target{}}
		var scope []byte
		var released sql.NullTime
		if err := rows.Scan(&h.ID, &h.Class, &scope, &h.Active, &h.Revision, &h.ConfigRevision,
			&h.TriggerEventID, &h.TriggeredAt, &released); err != nil {
			return nil, err
		}
		if err := decodeJSON(scope, &h.Scope); err != nil {
			return nil, err
		}
		if !h.Scope.valid(h.Class) || h.ID != healthHoldID(core, h.Class, h.Scope) || !validRef(h.TriggerEventID) {
			return nil, errors.New("invalid persisted dispatch health hold")
		}
		if released.Valid {
			h.ReleasedAt = &released.Time
		}
		holds = append(holds, h)
		if len(holds) > MaxHealthHolds {
			return nil, errors.New("dispatch health hold count exceeds limit")
		}
	}
	return holds, rows.Err()
}

func reportHold(h HealthHoldReport, targets map[string]*storedTarget) HealthHoldReport {
	h.Targets = []Target{}
	for _, row := range targets {
		if h.Scope.matches(row.Target) {
			h.Targets = append(h.Targets, row.Target)
		}
	}
	sort.Slice(h.Targets, func(i, j int) bool { return h.Targets[i].Ref < h.Targets[j].Ref })
	return h
}

func resetForHold(row *storedTarget, hold HealthHoldReport, now time.Time) {
	h := &row.Health
	h.State, h.Reason, h.CoolUntil = Tripped, "hold_released", now
	if hold.Active {
		h.Reason = hold.Class
	}
	h.ProbeSuccesses, h.RecoveryStep, h.ConsecutiveFailures, h.Backoff = 0, 0, 0, 0
	h.Samples, h.Failures, h.GoodWindows, h.BadWindows = 0, 0, 0, 0
	h.WindowAt, h.UpdatedAt = now, now
	h.TTFTMillis, h.TTFTObservedAt, h.Routing = 0, time.Time{}, nil
	h.Rolling = nil
	if h.HoldRevisions == nil {
		h.HoldRevisions = make(map[string]int64)
	}
	h.HoldRevisions[hold.ID] = hold.Revision
	row.Generation++
}

func saveHoldRecovery(ctx context.Context, tx *sql.Tx, core string, row *storedTarget) error {
	health, err := json.Marshal(row.Health)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE dispatch.targets SET health=$3,generation=$4
	 WHERE core_instance=$1 AND target_ref=$2`, core, row.Target.Ref, string(health), row.Generation)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("dispatch health hold recovery target missing")
	}
	return nil
}

func reconcileHealthHolds(ctx context.Context, tx *sql.Tx, core string, targets map[string]*storedTarget, now time.Time) error {
	holds, err := loadHealthHolds(ctx, tx, core)
	if err != nil {
		return err
	}
	for _, hold := range holds {
		for _, row := range targets {
			if hold.Scope.matches(row.Target) && row.Health.HoldRevisions[hold.ID] != hold.Revision {
				resetForHold(row, hold, now)
				if err := saveTarget(ctx, tx, core, row); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// 在权重计算后仍读取当前持久锁，避免 routing/quality 或旧 Health 覆盖硬否决。
func applyHealthHoldVeto(ctx context.Context, tx *sql.Tx, snapshot *Snapshot) error {
	holds, err := loadHealthHolds(ctx, tx, snapshot.CoreInstance)
	if err != nil {
		return err
	}
	for i := range snapshot.Candidates {
		candidate := &snapshot.Candidates[i]
		for _, hold := range holds {
			if hold.Active && hold.Scope.matches(candidate.Target) {
				candidate.EffectiveWeight, candidate.State, candidate.Reason = 0, Tripped, hold.Class
				break
			}
		}
	}
	return nil
}

func checkHeldQuotaConfig(ctx context.Context, tx *sql.Tx, core string, previous map[string]*storedTarget, targets []Target) error {
	holds, err := loadHealthHolds(ctx, tx, core)
	if err != nil {
		return err
	}
	for _, hold := range holds {
		if !hold.Active || hold.Scope.Kind != "quota" {
			continue
		}
		for _, old := range previous {
			if !hold.Scope.matches(old.Target) {
				continue
			}
			for _, target := range targets {
				if sameDomain(old.Target, target) && !hold.Scope.matches(target) {
					return errors.New("dispatch active quota hold prevents affected domain reassignment")
				}
			}
		}
	}
	return nil
}

func triggerHealthHold(ctx context.Context, tx *sql.Tx, core string, targets map[string]*storedTarget, source Target, o Observation, now time.Time) error {
	scope := failureScope(o.FailureClass, source)
	id := healthHoldID(core, o.FailureClass, scope)
	encoded, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	var revision, configRevision int64
	var exists bool
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch.health_holds WHERE core_instance=$1 AND hold_id=$2),
	 (SELECT count(*) FROM dispatch.health_holds WHERE core_instance=$1)`, core, id).Scan(&exists, &count); err != nil {
		return err
	}
	if !exists && count >= MaxHealthHolds {
		return errors.New("dispatch retained health hold limit exceeded")
	}
	if err := tx.QueryRowContext(ctx, `SELECT config_revision FROM dispatch.cores WHERE core_instance=$1`, core).Scan(&configRevision); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO dispatch.health_holds
	 (core_instance,hold_id,class,scope,active,revision,config_revision,trigger_event_id,triggered_at)
	 VALUES ($1,$2,$3,$4,TRUE,1,$5,$6,$7)
	 ON CONFLICT (core_instance,hold_id) DO UPDATE SET active=TRUE,revision=dispatch.health_holds.revision+1,
	 config_revision=EXCLUDED.config_revision,trigger_event_id=EXCLUDED.trigger_event_id,
	 triggered_at=EXCLUDED.triggered_at,released_at=NULL
	 WHERE dispatch.health_holds.class=EXCLUDED.class AND dispatch.health_holds.scope=EXCLUDED.scope
	 RETURNING revision`, core, id, o.FailureClass, string(encoded), configRevision, o.EventID, now).Scan(&revision)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.health_hold_audit
	 (audit_id,core_instance,hold_id,hold_revision,action,config_revision,event_id,recorded_at)
	 VALUES ($1,$2,$3,$4,'trigger',$5,$6,$7)`, uuid.NewString(), core, id, revision, configRevision, o.EventID, now); err != nil {
		return err
	}
	hold := HealthHoldReport{ID: id, Revision: revision, Active: true, Class: o.FailureClass, Scope: scope}
	for _, row := range targets {
		if scope.matches(row.Target) {
			resetForHold(row, hold, now)
			if err := saveTarget(ctx, tx, core, row); err != nil {
				return err
			}
		}
	}
	return nil
}

// HealthHolds 只读一致快照，包含已解除的锁和最近一次人工解除审计。
func (s *Store) HealthHolds(ctx context.Context, core string, configRevision int64) ([]HealthHoldReport, error) {
	if !validRef(core) || configRevision < 1 {
		return nil, ErrConfigRevision
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, core, configRevision); err != nil {
		return nil, err
	}
	holds, err := loadHealthHolds(ctx, tx, core)
	if err != nil {
		return nil, err
	}
	targets, err := loadTargets(ctx, tx, core)
	if err != nil {
		return nil, err
	}
	for i, hold := range holds {
		holds[i] = reportHold(hold, targets)
		var body []byte
		err := tx.QueryRowContext(ctx, `SELECT release_report->'audit' FROM dispatch.health_hold_audit
		 WHERE core_instance=$1 AND hold_id=$2 AND action='release' ORDER BY hold_revision DESC LIMIT 1`, core, hold.ID).Scan(&body)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var audit HealthHoldAudit
		if err := json.Unmarshal(body, &audit); err != nil {
			return nil, err
		}
		holds[i].LastRelease = &audit
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return holds, nil
}

func (s *Store) ReleaseHealthHold(ctx context.Context, request HoldReleaseRequest) (HoldReleaseResult, error) {
	if err := request.Validate(); err != nil {
		return HoldReleaseResult{}, err
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return HoldReleaseResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, request.CoreInstance, request.ConfigRevision); err != nil {
		return HoldReleaseResult{}, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return HoldReleaseResult{}, err
	}
	var same bool
	var original []byte
	err = tx.QueryRowContext(ctx, `SELECT request=$3::jsonb,release_report FROM dispatch.health_hold_audit
	 WHERE core_instance=$1 AND operation_id=$2`, request.CoreInstance, request.OperationID, string(body)).Scan(&same, &original)
	if err == nil {
		if !same {
			return HoldReleaseResult{}, ErrHealthHoldOperationConflict
		}
		var result HoldReleaseResult
		if err := json.Unmarshal(original, &result); err != nil {
			return HoldReleaseResult{}, err
		}
		result.Idempotent = true
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return HoldReleaseResult{}, err
	}
	holds, err := loadHealthHolds(ctx, tx, request.CoreInstance)
	if err != nil {
		return HoldReleaseResult{}, err
	}
	var hold *HealthHoldReport
	for i := range holds {
		if holds[i].ID == request.HoldID {
			hold = &holds[i]
			break
		}
	}
	if hold == nil {
		return HoldReleaseResult{}, ErrHealthHoldNotFound
	}
	if !hold.Active || hold.Revision != request.HoldRevision {
		return HoldReleaseResult{}, ErrHealthHoldRevision
	}
	hold.Revision++
	hold.Active, hold.ReleasedAt, hold.ConfigRevision = false, &now, request.ConfigRevision
	if _, err := tx.ExecContext(ctx, `UPDATE dispatch.health_holds SET active=FALSE,revision=$3,
	 released_at=$4,config_revision=$5 WHERE core_instance=$1 AND hold_id=$2`, request.CoreInstance, hold.ID, hold.Revision, now, request.ConfigRevision); err != nil {
		return HoldReleaseResult{}, err
	}
	targets, err := loadTargets(ctx, tx, request.CoreInstance)
	if err != nil {
		return HoldReleaseResult{}, err
	}
	for _, row := range targets {
		if hold.Scope.matches(row.Target) {
			resetForHold(row, *hold, now)
			if err := saveHoldRecovery(ctx, tx, request.CoreInstance, row); err != nil {
				return HoldReleaseResult{}, err
			}
		}
	}
	audit := HealthHoldAudit{ID: uuid.NewString(), CoreInstance: request.CoreInstance, HoldID: hold.ID,
		HoldRevision: hold.Revision, Action: "release", ConfigRevision: request.ConfigRevision, OperationID: request.OperationID,
		Actor: request.Actor, Reason: request.Reason, EvidenceRef: request.EvidenceRef, RecordedAt: now}
	hold.LastRelease = &audit
	result := HoldReleaseResult{Hold: reportHold(*hold, targets), Audit: audit}
	report, err := json.Marshal(result)
	if err != nil {
		return HoldReleaseResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.health_hold_audit
	 (audit_id,core_instance,hold_id,hold_revision,action,config_revision,operation_id,actor,reason,evidence_ref,request,release_report,recorded_at)
	 VALUES ($1,$2,$3,$4,'release',$5,$6,$7,$8,$9,$10,$11,$12)`, audit.ID, audit.CoreInstance, audit.HoldID,
		audit.HoldRevision, audit.ConfigRevision, audit.OperationID, audit.Actor, audit.Reason, audit.EvidenceRef, string(body), string(report), now); err != nil {
		return HoldReleaseResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return HoldReleaseResult{}, err
	}
	return result, nil
}
