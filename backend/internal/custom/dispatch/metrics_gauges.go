package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type targetGaugeKey struct {
	State   State
	Enabled bool
}

type seriesLabel struct {
	Series string
	Label  string
}

// MetricsState 是一次只读事务内读出的低基数现状；不含目标、账号、凭据或模型标识。
type MetricsState struct {
	ObservedAt        time.Time
	Targets           map[targetGaugeKey]int
	AuthBlocked       int
	ActiveHolds       map[string]int
	ActiveLeases      int
	ExpiredUnfinished int
	BudgetReady       bool
	Budget            ProbeBudgetTotals
	BudgetDailyLimit  *int64
	MetricsReady      bool
	RollupWatermark   *time.Time
	LastHour          map[seriesLabel]int64
	OldestObservation *time.Time
	OldestProbe       *time.Time
}

func (s *Store) MetricsState(ctx context.Context, core string, revision int64) (MetricsState, error) {
	state := MetricsState{Targets: map[targetGaugeKey]int{}, ActiveHolds: map[string]int{}, LastHour: map[seriesLabel]int64{}}
	if !validRef(core) || revision < 1 {
		return state, ErrConfigRevision
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return state, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, core, revision); err != nil {
		return state, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&state.ObservedAt); err != nil {
		return state, err
	}
	state.ObservedAt = state.ObservedAt.UTC()
	targets, err := loadTargets(ctx, tx, core)
	if err != nil {
		return state, err
	}
	for _, row := range targets {
		state.Targets[targetGaugeKey{State: row.Health.State, Enabled: row.Target.Enabled}]++
		if row.Health.AuthBlocked {
			state.AuthBlocked++
		}
	}
	holds, err := loadHealthHolds(ctx, tx, core)
	if err != nil {
		return state, err
	}
	for _, hold := range holds {
		if hold.Active {
			state.ActiveHolds[hold.Class]++
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE lease_until>$2),count(*) FILTER (WHERE lease_until<=$2)
	 FROM dispatch.probes WHERE core_instance=$1 AND finished_at IS NULL`, core, state.ObservedAt).Scan(&state.ActiveLeases, &state.ExpiredUnfinished); err != nil {
		return state, err
	}
	var oldestObservation, oldestProbe sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT min(recorded_at) FROM dispatch.observations WHERE core_instance=$1),
	 (SELECT min(reserved_at) FROM dispatch.probes WHERE core_instance=$1)`, core).Scan(&oldestObservation, &oldestProbe); err != nil {
		return state, err
	}
	state.OldestObservation, state.OldestProbe = utcTime(oldestObservation), utcTime(oldestProbe)
	if state.BudgetReady, err = probeBudgetSchemaReady(ctx, tx); err != nil {
		return state, err
	}
	if state.BudgetReady {
		if state.Budget, err = probeBudgetTotals(ctx, tx, probeBudgetDay(state.ObservedAt), ""); err != nil {
			return state, err
		}
		limit, _, _, err := probeBudgetLimits(ctx, tx, "")
		if err != nil {
			return state, err
		}
		if limit.Valid {
			state.BudgetDailyLimit = &limit.Int64
		}
	}
	if state.MetricsReady, err = metricsSchemaReady(ctx, tx); err != nil {
		return state, err
	}
	if !state.MetricsReady {
		return state, tx.Commit()
	}
	var watermark time.Time
	err = tx.QueryRowContext(ctx, `SELECT watermark FROM dispatch.metric_rollups WHERE core_instance=$1`, core).Scan(&watermark)
	if err == nil {
		watermark = watermark.UTC()
		state.RollupWatermark = &watermark
	} else if !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT series,label,sum(count)::bigint FROM dispatch.metric_minutes
	 WHERE core_instance=$1 AND bucket>=$2 GROUP BY 1,2`, core, state.ObservedAt.Truncate(time.Minute).Add(-time.Hour))
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var key seriesLabel
		var count int64
		if err := rows.Scan(&key.Series, &key.Label, &count); err != nil {
			_ = rows.Close()
			return state, err
		}
		state.LastHour[key] = count
	}
	if err := closeRows(rows); err != nil {
		return state, err
	}
	return state, tx.Commit()
}

func utcTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time.UTC()
	return &at
}
