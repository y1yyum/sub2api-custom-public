package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"
)

func readProbePolicy(ctx context.Context, tx *sql.Tx, core string) (persistedProbePolicy, error) {
	var raw []byte
	var policy persistedProbePolicy
	if err := tx.QueryRowContext(ctx, `SELECT probe_limits FROM dispatch.cores WHERE core_instance=$1`, core).Scan(&raw); err != nil {
		return policy, err
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		return policy, err
	}
	return policy, nil
}

type TargetHealthWindowReport struct {
	CoreInstance   string              `json:"core_instance"`
	ConfigRevision int64               `json:"config_revision"`
	TargetRef      string              `json:"target_ref"`
	AccountID      int64               `json:"account_id"`
	Model          string              `json:"model"`
	Region         string              `json:"region"`
	ObservedAt     time.Time           `json:"observed_at"`
	State          State               `json:"state"`
	Reason         string              `json:"reason"`
	PolicyEnabled  bool                `json:"policy_enabled"`
	TargetEnabled  bool                `json:"target_enabled"`
	Window         *HealthWindowReport `json:"window,omitempty"`
}

func (s *Store) HealthWindowReports(ctx context.Context, core string, revision int64) ([]TargetHealthWindowReport, error) {
	if !validRef(core) || revision < 1 {
		return nil, ErrConfigRevision
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, core, revision); err != nil {
		return nil, err
	}
	policy, err := readProbePolicy(ctx, tx, core)
	if err != nil {
		return nil, err
	}
	rows, err := loadTargets(ctx, tx, core)
	if err != nil {
		return nil, err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}
	reports := make([]TargetHealthWindowReport, 0, len(rows))
	for _, row := range rows {
		report := TargetHealthWindowReport{CoreInstance: core, ConfigRevision: revision,
			TargetRef: row.Target.Ref, AccountID: row.Target.AccountID, Model: row.Target.Model,
			Region: row.Target.Region, ObservedAt: now.UTC(), State: row.Health.State,
			Reason: row.Health.Reason, PolicyEnabled: policy.HealthWindow != nil, TargetEnabled: row.Target.Enabled}
		if policy.HealthWindow != nil {
			window := row.Health.HealthWindowReport(now, *policy.HealthWindow, policy.ttftTargetMillis())
			report.Window = &window
		}
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].TargetRef < reports[j].TargetRef })
	return reports, tx.Commit()
}

func (p persistedProbePolicy) ttftTargetMillis() int64 {
	if p.Routing == nil {
		return 1000
	}
	return p.Routing.TTFTTargetMillis
}

func applyHealthWindowWeights(ctx context.Context, tx *sql.Tx, snapshot *Snapshot, now time.Time) error {
	policy, err := readProbePolicy(ctx, tx, snapshot.CoreInstance)
	if err != nil || policy.HealthWindow == nil {
		return err
	}
	rows, err := loadTargets(ctx, tx, snapshot.CoreInstance)
	if err != nil {
		return err
	}
	for i := range snapshot.Candidates {
		candidate := &snapshot.Candidates[i]
		if candidate.EffectiveWeight == 0 {
			continue
		}
		factor := rows[candidate.Ref].Health.WindowFactor(now, *policy.HealthWindow, policy.ttftTargetMillis())
		if factor == 0 {
			candidate.EffectiveWeight = 0
		} else {
			candidate.EffectiveWeight = max(1, candidate.EffectiveWeight*factor/1000)
		}
	}
	return snapshot.Validate(now)
}
