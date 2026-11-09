//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func metricsObservationRow(t *testing.T, db *sql.DB, core string, o dispatch.Observation, recorded time.Time) {
	t.Helper()
	body, err := json.Marshal(o)
	require.NoError(t, err)
	dispatchExec(t, db, `INSERT INTO dispatch.observations(core_instance,event_id,payload,recorded_at) VALUES ($1,$2,$3,$4)`,
		core, o.EventID, string(body), recorded)
}

func metricsProbeRow(t *testing.T, db *sql.DB, core string, target dispatch.Target, reserved time.Time, finished bool) string {
	t.Helper()
	token := uuid.NewString()
	metadata, err := json.Marshal(target)
	require.NoError(t, err)
	var finishedAt any
	if finished {
		finishedAt = reserved.Add(time.Second)
	}
	dispatchExec(t, db, `INSERT INTO dispatch.probes
	 (token,core_instance,target_ref,target,generation,owner,account_id,model,region,provider_ref,slot,attempt_id,reservation_id,reserved_at,lease_until,finished_at)
	 VALUES ($1,$2,$3,$4,1,'metrics-history',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		token, core, target.Ref, string(metadata), target.AccountID, target.Model, target.Region, target.ProviderRef,
		reserved.UnixMicro(), "dispatch-attempt:"+token, "dispatch-reservation:"+token, reserved, reserved.Add(time.Minute), finishedAt)
	return token
}

func metricsHoldRows(t *testing.T, db *sql.DB, core, hold, class string, scope string, at time.Time, released bool) {
	t.Helper()
	revision, active := 1, true
	var releasedAt any
	if released {
		revision, active, releasedAt = 2, false, at
	}
	dispatchExec(t, db, `INSERT INTO dispatch.health_holds
	 (core_instance,hold_id,class,scope,active,revision,config_revision,trigger_event_id,triggered_at,released_at)
	 VALUES ($1,$2,$3,$4,$5,$6,1,$7,$8,$9)`, core, hold, class, scope, active, revision, "event-"+hold, at, releasedAt)
	dispatchExec(t, db, `INSERT INTO dispatch.health_hold_audit
	 (audit_id,core_instance,hold_id,hold_revision,action,config_revision,event_id,recorded_at)
	 VALUES ($1,$2,$3,1,'trigger',1,$4,$5)`, uuid.NewString(), core, hold, "event-"+hold, at)
	if released {
		dispatchExec(t, db, `INSERT INTO dispatch.health_hold_audit
		 (audit_id,core_instance,hold_id,hold_revision,action,config_revision,operation_id,actor,reason,evidence_ref,request,release_report,recorded_at)
		 VALUES ($1,$2,$3,2,'release',1,$4,'operator','已核对','review-1','{}','{}',$5)`, uuid.NewString(), core, hold, "op-"+hold, at)
	}
}

func metricsCounts(t *testing.T, db *sql.DB, table, core string, since, until time.Time) map[string]int64 {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT bucket,series,label,count FROM dispatch.`+table+`
	 WHERE core_instance=$1 AND bucket>=$2 AND bucket<$3`, core, since, until)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	counts := map[string]int64{}
	for rows.Next() {
		var bucket time.Time
		var series, label string
		var count int64
		require.NoError(t, rows.Scan(&bucket, &series, &label, &count))
		counts[fmt.Sprintf("%d/%s/%s", bucket.Unix(), series, label)] = count
	}
	require.NoError(t, rows.Err())
	return counts
}

func metricsStoreState(t *testing.T, db *sql.DB) string {
	t.Helper()
	var state string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT jsonb_build_object(
	 'rollups',(SELECT jsonb_agg(to_jsonb(r) ORDER BY core_instance) FROM dispatch.metric_rollups r),
	 'minutes',(SELECT jsonb_agg(to_jsonb(m) ORDER BY core_instance,bucket,series,label) FROM dispatch.metric_minutes m),
	 'hours',(SELECT jsonb_agg(to_jsonb(h) ORDER BY core_instance,bucket,series,label) FROM dispatch.metric_hours h))::text`).Scan(&state))
	return state
}

func TestDispatchMetricsRollupCountsDeduplicatedFactsOnce(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b := dispatchTarget("a", 1), dispatchTarget("b", 2)
	cfg := failureConfig(t, []dispatch.Target{a, b}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	first, err := store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	require.Zero(t, first.Minutes, "the first enablement starts at the current minute without backfill")
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.metric_minutes`))
	base := first.Watermark.Add(-3 * time.Minute)
	dispatchExec(t, db, `UPDATE dispatch.metric_rollups SET watermark=$1 WHERE core_instance='core'`, base)

	// 真实被动路径：重复读取同一批事件只在第一次写入去重事实。
	batch := make([]dispatch.PassiveEvent, 3)
	for i := range batch {
		batch[i] = dispatch.PassiveEvent{Observation: dispatchObservation(t, db, a, uuid.NewString(), dispatch.Success),
			CoreInstance: cfg.CoreInstance, ConfigRevision: cfg.ConfigRevision, CredentialRef: a.CredentialRef,
			ProviderRef: a.ProviderRef, PoolRef: a.PoolRef, PolicyRevision: a.PolicyRevision, Model: a.Model, Region: a.Region}
	}
	inserted, err := store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
	require.NoError(t, err)
	require.Equal(t, 3, inserted)
	inserted, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
	require.NoError(t, err)
	require.Zero(t, inserted)
	dispatchExec(t, db, `UPDATE dispatch.observations SET recorded_at=$1`, base.Add(20*time.Second))

	probe := metricsProbeRow(t, db, "core", b, base.Add(5*time.Second), true)
	for _, item := range []struct {
		o  dispatch.Observation
		at time.Duration
	}{
		{dispatch.Observation{EventID: "dispatch-attempt:" + probe, TargetRef: "b", AccountID: 2, CredentialRevision: 1, Outcome: dispatch.Success, ProviderEvidence: true}, 10 * time.Second},
		{dispatch.Observation{EventID: "m0-break", TargetRef: "a", AccountID: 1, CredentialRevision: 1, Outcome: dispatch.Unknown, Phase: "content", ProtocolFact: "stream_break"}, 30 * time.Second},
		{dispatch.Observation{EventID: "m0-timeout", TargetRef: "a", AccountID: 1, CredentialRevision: 1, Outcome: dispatch.UpstreamFailure, ProviderEvidence: true, TimedOut: true}, 40 * time.Second},
		{dispatch.Observation{EventID: "m1-deadline", TargetRef: "a", AccountID: 1, CredentialRevision: 1, Outcome: dispatch.ClientCanceled, ProtocolFact: "bridge_deadline"}, 70 * time.Second},
		{dispatch.Observation{EventID: "m1-banned", TargetRef: "a", AccountID: 1, CredentialRevision: 1, Outcome: dispatch.UpstreamAuth, ProviderEvidence: true, FailureClass: dispatch.FailureCredentialBanned}, 80 * time.Second},
		{dispatch.Observation{EventID: "m1-usage", TargetRef: "a", AccountID: 1, CredentialRevision: 1, Outcome: dispatch.Success, ProviderEvidence: true, Phase: "terminal", UsageMissing: true}, 90 * time.Second},
	} {
		item.o.At = base.Add(item.at)
		metricsObservationRow(t, db, "core", item.o, base.Add(item.at))
	}
	metricsHoldRows(t, db, "core", "hold-banned", dispatch.FailureCredentialBanned,
		`{"kind":"credential","provider_ref":"provider","credential_ref":"credential-1"}`, base.Add(130*time.Second), false)
	metricsHoldRows(t, db, "core", "hold-quota", dispatch.FailureQuotaExhausted,
		`{"kind":"account_model_region","account_id":2,"model":"model","region":"region"}`, base.Add(140*time.Second), true)

	rolled, err := store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	require.False(t, rolled.Watermark.Before(first.Watermark))
	m0, m1, m2 := base.Unix(), base.Add(time.Minute).Unix(), base.Add(2*time.Minute).Unix()
	expected := map[string]int64{}
	add := func(minute, count int64, keys ...string) {
		for _, key := range keys {
			expected[fmt.Sprintf("%d/%s", minute, key)] = count
		}
	}
	add(m0, 4, "observations/success", "provider_evidence/success")
	add(m0, 1, "probe_observations/success", "probe_reservations/",
		"observations/unknown", "facts/stream_break.content", "phases/content",
		"observations/upstream_failure", "provider_evidence/upstream_failure", "facts/timed_out")
	add(m1, 1, "observations/client_canceled", "facts/bridge_deadline",
		"observations/upstream_auth", "provider_evidence/upstream_auth", "failure_classes/credential_banned",
		"observations/success", "provider_evidence/success", "facts/usage_missing", "phases/terminal")
	add(m2, 1, "hold_triggers/credential_banned", "hold_triggers/quota_exhausted", "hold_releases/quota_exhausted")
	require.Equal(t, expected, metricsCounts(t, db, "metric_minutes", "core", base, rolled.Watermark))
	hourly := map[string]int64{}
	for key, count := range expected {
		var bucket int64
		var rest string
		_, err := fmt.Sscanf(key, "%d/%s", &bucket, &rest)
		require.NoError(t, err)
		hourly[fmt.Sprintf("%d/%s", time.Unix(bucket, 0).Truncate(time.Hour).Unix(), rest)] += count
	}
	require.Equal(t, hourly, metricsCounts(t, db, "metric_hours", "core", base.Truncate(time.Hour), rolled.Watermark.Add(time.Hour)))

	before := metricsCounts(t, db, "metric_minutes", "core", base, rolled.Watermark)
	_, err = store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	require.Equal(t, before, metricsCounts(t, db, "metric_minutes", "core", base, rolled.Watermark), "repeated rollups never recount a minute")

	// 水位被回拨时拒绝重复累计，事务整体回滚。
	state := metricsStoreState(t, db)
	dispatchExec(t, db, `UPDATE dispatch.metric_rollups SET watermark=$1 WHERE core_instance='core'`, base)
	rewound := metricsStoreState(t, db)
	_, err = store.RollupMetrics(t.Context(), "core")
	require.Error(t, err)
	require.Equal(t, rewound, metricsStoreState(t, db))
	require.NotEqual(t, state, rewound)

	report, err := store.MetricsReport(t.Context(), "core", 1)
	require.NoError(t, err)
	require.True(t, report.SchemaReady)
	require.NotNil(t, report.Rollup)
	require.Equal(t, base, report.Rollup.Watermark)
	require.NotEmpty(t, report.Minutes)
	require.NotEmpty(t, report.Hours)
}

func TestDispatchMetricsRollupCatchUpIsBounded(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	first, err := store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	dispatchExec(t, db, `UPDATE dispatch.metric_rollups SET watermark=$1`, first.Watermark.Add(-3*time.Hour))
	next, err := store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	require.Equal(t, 60, next.Minutes)
	require.Equal(t, first.Watermark.Add(-2*time.Hour), next.Watermark)
}

func TestDispatchMetricsRollupAccumulatesHourAcrossPasses(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	first, err := store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	// 每次聚合至多一小时：两次聚合各覆盖同一小时的一部分，小时桶必须累加。
	hour := first.Watermark.Add(-2 * time.Hour).Truncate(time.Hour)
	dispatchExec(t, db, `UPDATE dispatch.metric_rollups SET watermark=$1 WHERE core_instance='core'`, hour.Add(-30*time.Minute))
	for i, at := range []time.Duration{10 * time.Minute, 40 * time.Minute} {
		metricsObservationRow(t, db, "core", dispatch.Observation{EventID: fmt.Sprintf("hour-%d", i), TargetRef: "a", AccountID: 1,
			CredentialRevision: 1, Outcome: dispatch.Success, At: hour.Add(at)}, hour.Add(at))
	}
	key := fmt.Sprintf("%d/observations/success", hour.Unix())
	for pass, want := range []int64{1, 2} {
		_, err := store.RollupMetrics(t.Context(), "core")
		require.NoError(t, err)
		require.Equal(t, want, metricsCounts(t, db, "metric_hours", "core", hour, hour.Add(time.Hour))[key], "pass %d", pass+1)
	}
}

func TestDispatchRetentionKeepsLeasesBudgetHoldsAndAudit(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b := dispatchTarget("a", 1), dispatchTarget("b", 2)
	cfg := failureConfig(t, []dispatch.Target{a, b}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	now := dispatchNow(t, db)
	old := now.Add(-10 * 24 * time.Hour)

	// 真实触发两次同一隔离：holds 引用第二个事件，审计仍引用第一个。
	for _, id := range []string{"hold-first", "hold-second"} {
		_, err := store.Observe(t.Context(), "core", failureEvent(t, db, a, id, dispatch.FailureCredentialBanned))
		require.NoError(t, err)
	}
	dispatchExec(t, db, `UPDATE dispatch.observations SET recorded_at=$1 WHERE event_id IN ('hold-first','hold-second')`, old)
	metricsObservationRow(t, db, "core", dispatch.Observation{EventID: "old-plain", TargetRef: "a", AccountID: 1, CredentialRevision: 1, Outcome: dispatch.Success, At: old}, old)
	metricsObservationRow(t, db, "core", dispatch.Observation{EventID: "recent", TargetRef: "a", AccountID: 1, CredentialRevision: 1, Outcome: dispatch.Success, At: now}, now.Add(-time.Hour))

	superseded := metricsProbeRow(t, db, "core", a, old, true)
	unfinished := metricsProbeRow(t, db, "core", a, old.Add(time.Second), false)
	priced := metricsProbeRow(t, db, "core", a, old.Add(2*time.Second), true)
	dispatchExec(t, db, `INSERT INTO dispatch.probe_prices VALUES ('provider','model','price-1','operator-price',100)`)
	dispatchExec(t, db, `INSERT INTO dispatch.probe_money(token,currency,provider_ref,model,price_revision,evidence_ref,max_microusd,reserved_at,budget_day,state,version)
	 SELECT token,'USD',provider_ref,model,'price-1','operator-price',100,reserved_at,(reserved_at AT TIME ZONE 'UTC')::date,'unknown',2 FROM dispatch.probes WHERE token=$1`, priced)
	dispatchExec(t, db, `INSERT INTO dispatch.probe_money_audit(token,version,action,recorded_at) VALUES ($1,1,'reserve',$2),($1,2,'unknown',$2)`, priced, old)
	latestOld := metricsProbeRow(t, db, "core", b, old, true)
	latestA := metricsProbeRow(t, db, "core", a, now.Add(-time.Hour), true)

	protected := func() string {
		var state string
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT jsonb_build_object(
		 'targets',(SELECT jsonb_agg(to_jsonb(t) ORDER BY target_ref) FROM dispatch.targets t),
		 'cores',(SELECT jsonb_agg(to_jsonb(c) ORDER BY core_instance) FROM dispatch.cores c),
		 'holds',(SELECT jsonb_agg(to_jsonb(h) ORDER BY hold_id) FROM dispatch.health_holds h),
		 'hold_audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY audit_id) FROM dispatch.health_hold_audit a),
		 'money',(SELECT jsonb_agg(to_jsonb(m) ORDER BY token) FROM dispatch.probe_money m),
		 'money_audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY audit_id) FROM dispatch.probe_money_audit a))::text`).Scan(&state))
		return state
	}
	before := protected()
	retention := dispatch.MonitoringConfig{Retention: &dispatch.RetentionConfig{ObservationDays: 1, ProbeDays: 1}}

	// 聚合水位早于截止点时，未聚合的原始行一律保留。
	dispatchExec(t, db, `INSERT INTO dispatch.metric_rollups VALUES ('core',$1,$1,$2)`, now.Add(-20*24*time.Hour).Truncate(time.Minute), now)
	results, err := store.ApplyRetention(t.Context(), "core", retention)
	require.NoError(t, err)
	for _, result := range results {
		require.Zero(t, result.Deleted, result.Table)
	}
	dispatchExec(t, db, `DELETE FROM dispatch.metric_rollups`)

	results, err = store.ApplyRetention(t.Context(), "core", retention)
	require.NoError(t, err)
	require.Equal(t, []string{"observations", "probes"}, []string{results[0].Table, results[1].Table})
	require.EqualValues(t, 1, results[0].Deleted)
	require.EqualValues(t, 1, results[1].Deleted)
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE event_id='old-plain'`))
	require.Equal(t, 3, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE event_id IN ('hold-first','hold-second','recent')`))
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE token=$1`, superseded))
	require.Equal(t, 4, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE token=ANY(ARRAY[$1,$2,$3,$4])`, unfinished, priced, latestOld, latestA))
	require.Equal(t, before, protected(), "holds, audit, money and Health JSON are untouched")
	metricsState, err := store.MetricsState(t.Context(), "core", 1)
	require.NoError(t, err)
	require.True(t, metricsState.MetricsReady)
	require.Equal(t, 1, metricsState.ActiveHolds[dispatch.FailureCredentialBanned])
	require.Equal(t, 1, metricsState.ExpiredUnfinished)
	require.EqualValues(t, 100, metricsState.Budget.UnknownMicrousd)

	again, err := store.ApplyRetention(t.Context(), "core", retention)
	require.NoError(t, err)
	for _, result := range again {
		require.Zero(t, result.Deleted, "retention is idempotent")
	}
	// 已清理的旧事件即使被重放也因超出五分钟观测窗口而被拒绝，不会重复改变健康。
	stale := dispatch.Observation{EventID: "old-plain", TargetRef: "a", AccountID: 1, CredentialRevision: 1, Outcome: dispatch.UpstreamFailure, ProviderEvidence: true, At: old}
	_, err = store.Observe(t.Context(), "core", stale)
	require.Error(t, err)
	require.Equal(t, before, protected())
}

func TestDispatchRetentionBatchesAreResumableAndAggregatesBounded(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	now := dispatchNow(t, db)
	dispatchExec(t, db, `INSERT INTO dispatch.observations(core_instance,event_id,payload,recorded_at)
	 SELECT 'core','bulk-'||i,'{}'::jsonb,$1::timestamptz-make_interval(secs=>i) FROM generate_series(1,10050) AS i`, now.Add(-5*24*time.Hour))
	minute := now.Truncate(time.Minute)
	hour := now.Truncate(time.Hour)
	dispatchExec(t, db, `INSERT INTO dispatch.metric_minutes VALUES ('core',$1,'observations','success',1),('core',$2,'observations','success',2)`,
		minute.Add(-3*24*time.Hour), minute.Add(-time.Hour))
	dispatchExec(t, db, `INSERT INTO dispatch.metric_hours VALUES ('core',$1,'observations','success',1),('core',$2,'observations','success',2)`,
		hour.Add(-40*24*time.Hour), hour.Add(-24*time.Hour))
	monitoring := dispatch.MonitoringConfig{Retention: &dispatch.RetentionConfig{ObservationDays: 2},
		Aggregation: &dispatch.AggregationConfig{MinuteRetentionDays: 2, HourRetentionDays: 30}}
	first, err := store.ApplyRetention(t.Context(), "core", monitoring)
	require.NoError(t, err)
	require.Len(t, first, 3, "probe retention stays off when probe_days is omitted")
	require.Equal(t, "observations", first[0].Table)
	require.EqualValues(t, 10000, first[0].Deleted)
	require.True(t, first[0].Pending, "one pass stops at the batch limit and resumes next round")
	require.EqualValues(t, 1, first[1].Deleted)
	require.EqualValues(t, 1, first[2].Deleted)
	second, err := store.ApplyRetention(t.Context(), "core", monitoring)
	require.NoError(t, err)
	require.EqualValues(t, 50, second[0].Deleted)
	require.False(t, second[0].Pending)
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.metric_minutes`))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.metric_hours`))
}

func TestDispatchRetentionReportsCommittedBatchesOnFailure(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	now := dispatchNow(t, db)
	dispatchExec(t, db, `INSERT INTO dispatch.observations(core_instance,event_id,payload,recorded_at)
	 SELECT 'core','bulk-'||i,'{}'::jsonb,$1::timestamptz-make_interval(secs=>i) FROM generate_series(1,1500) AS i`, now.Add(-5*24*time.Hour))
	// 序列不随事务回滚：第一批提交，第二批语句在触发器中失败。
	dispatchExec(t, db, `CREATE SEQUENCE dispatch.retention_failure_after`)
	dispatchExec(t, db, `CREATE FUNCTION dispatch.retention_failure_after() RETURNS trigger LANGUAGE plpgsql AS $$
	 BEGIN IF nextval('dispatch.retention_failure_after') > 1 THEN RAISE EXCEPTION 'second batch fails'; END IF; RETURN NULL; END $$`)
	dispatchExec(t, db, `CREATE TRIGGER retention_failure_after BEFORE DELETE ON dispatch.observations
	 FOR EACH STATEMENT EXECUTE FUNCTION dispatch.retention_failure_after()`)
	results, err := store.ApplyRetention(t.Context(), "core", dispatch.MonitoringConfig{Retention: &dispatch.RetentionConfig{ObservationDays: 2}})
	require.Error(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "observations", results[0].Table)
	require.EqualValues(t, 1000, results[0].Deleted, "the committed first batch is reported")
	require.True(t, results[0].Pending, "rows remain after the failed batch")
	require.Equal(t, 500, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
}

func TestDispatchRetentionKeepsProbesOfUnaggregatedResults(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	b := dispatchTarget("b", 2)
	cfg := failureConfig(t, []dispatch.Target{b}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	first, err := store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	// 聚合落后 48 小时：探针预留在水位前，结果观测在水位后，尚未聚合。
	watermark := first.Watermark.Add(-48 * time.Hour)
	dispatchExec(t, db, `UPDATE dispatch.metric_rollups SET watermark=$1 WHERE core_instance='core'`, watermark)
	result := func(token string, at time.Time) {
		metricsObservationRow(t, db, "core", dispatch.Observation{EventID: "dispatch-attempt:" + token, TargetRef: "b", AccountID: 2,
			CredentialRevision: 1, Outcome: dispatch.Success, At: at}, at)
	}
	aggregated := metricsProbeRow(t, db, "core", b, watermark.Add(-10*time.Minute), true)
	result(aggregated, watermark.Add(-9*time.Minute))
	pending := metricsProbeRow(t, db, "core", b, watermark.Add(-30*time.Second), true)
	result(pending, watermark.Add(10*time.Second))
	metricsProbeRow(t, db, "core", b, watermark.Add(10*time.Minute), true)

	results, err := store.ApplyRetention(t.Context(), "core", dispatch.MonitoringConfig{Retention: &dispatch.RetentionConfig{ProbeDays: 1}})
	require.NoError(t, err)
	require.EqualValues(t, 1, results[0].Deleted, "only the probe whose result is already aggregated is removed")
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE token=$1`, aggregated))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE token=$1`, pending))

	_, err = store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	counts := metricsCounts(t, db, "metric_minutes", "core", watermark, watermark.Add(time.Minute))
	require.EqualValues(t, 1, counts[fmt.Sprintf("%d/observations/success", watermark.Unix())])
	require.EqualValues(t, 1, counts[fmt.Sprintf("%d/probe_observations/success", watermark.Unix())])
}

func TestDispatchSlowMetricsScanDoesNotHoldStoreLock(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	first, err := store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	dispatchExec(t, db, `UPDATE dispatch.metric_rollups SET watermark=$1 WHERE core_instance='core'`, first.Watermark.Add(-time.Hour))
	_, err = dispatch.RunRound(t.Context(), cfg, store, nil)
	require.NoError(t, err)

	// 另一事务独占聚合扫描读取的审计表，使扫描阻塞，模拟慢聚合。
	blocker, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.ExecContext(t.Context(), `LOCK TABLE dispatch.health_hold_audit IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)
	rolled := make(chan error, 1)
	go func() {
		_, err := store.RollupMetrics(context.Background(), "core")
		rolled <- err
	}()
	require.Eventually(t, func() bool {
		var waiting int
		err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_locks
		 WHERE NOT granted AND relation='dispatch.health_hold_audit'::regclass`).Scan(&waiting)
		return err == nil && waiting == 1
	}, 5*time.Second, 10*time.Millisecond, "the rollup scan waits on the audit table")

	roundCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	_, err = dispatch.RunRound(roundCtx, cfg, store, nil)
	cancel()
	require.NoError(t, err, "a blocked rollup scan must not hold the dispatch store lock")
	require.NoError(t, blocker.Rollback())
	require.NoError(t, <-rolled)
}

// slowMaintenanceTriggers 让维护写入在语句执行时或提交时（延迟约束触发器）耗时 4 秒，超过维护持锁上限。
var slowMaintenanceTriggers = []struct{ name, create string }{
	{"statement", `CREATE TRIGGER %[1]s BEFORE %[2]s ON %[3]s FOR EACH STATEMENT EXECUTE FUNCTION dispatch.%[1]s()`},
	{"commit", `CREATE CONSTRAINT TRIGGER %[1]s AFTER %[2]s ON %[3]s DEFERRABLE INITIALLY DEFERRED
	 FOR EACH ROW EXECUTE FUNCTION dispatch.%[1]s()`},
}

func TestDispatchSlowMetricsWriteDoesNotDelayFinishProbe(t *testing.T) {
	for _, trigger := range slowMaintenanceTriggers {
		t.Run(trigger.name, func(t *testing.T) { slowMetricsWriteDoesNotDelayFinishProbe(t, trigger.create) })
	}
}

func slowMetricsWriteDoesNotDelayFinishProbe(t *testing.T, createTrigger string) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	cfg := failureConfig(t, []dispatch.Target{a}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	first, err := store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	watermark := first.Watermark.Add(-10 * time.Minute)
	dispatchExec(t, db, `UPDATE dispatch.metric_rollups SET watermark=$1 WHERE core_instance='core'`, watermark)
	metricsObservationRow(t, db, "core", dispatch.Observation{EventID: "slow-write", TargetRef: "a", AccountID: 1,
		CredentialRevision: 1, Outcome: dispatch.Success, At: watermark.Add(10 * time.Second)}, watermark.Add(10*time.Second))
	lease, err := store.ClaimProbe(t.Context(), "core", "a", "metrics-owner", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	probe := dispatchObservation(t, db, a, lease.AttemptID, dispatch.Success)

	dispatchExec(t, db, `CREATE FUNCTION dispatch.slow_metric_write() RETURNS trigger LANGUAGE plpgsql AS $$
	 BEGIN PERFORM pg_sleep(4); RETURN NULL; END $$`)
	dispatchExec(t, db, fmt.Sprintf(createTrigger, "slow_metric_write", "INSERT", "dispatch.metric_minutes"))
	rolled := make(chan error, 1)
	go func() {
		_, err := store.RollupMetrics(context.Background(), "core")
		rolled <- err
	}()
	require.Eventually(t, func() bool {
		var sleeping int
		err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event='PgSleep'`).Scan(&sleeping)
		return err == nil && sleeping == 1
	}, 5*time.Second, 10*time.Millisecond, "the rollup write holds the store lock")

	// 与 RunRound 相同的 3 秒结算时限。
	finishCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	err = store.FinishProbe(finishCtx, *lease, probe)
	cancel()
	require.NoError(t, err, "a slow rollup write must release the store lock before the settlement deadline")
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE finished_at IS NULL`))
	require.Error(t, <-rolled, "the rollup is rolled back at the lock hold limit")
	var current time.Time
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT watermark FROM dispatch.metric_rollups WHERE core_instance='core'`).Scan(&current))
	require.True(t, watermark.Equal(current), "the watermark does not advance")
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.metric_minutes`))

	dispatchExec(t, db, `DROP TRIGGER slow_metric_write ON dispatch.metric_minutes`)
	next, err := store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	require.True(t, next.Watermark.After(watermark))
	require.EqualValues(t, 1, metricsCounts(t, db, "metric_minutes", "core", watermark, watermark.Add(time.Minute))[fmt.Sprintf("%d/observations/success", watermark.Unix())])
}

func TestDispatchSlowRetentionBatchDoesNotDelayFinishProbe(t *testing.T) {
	for _, trigger := range slowMaintenanceTriggers {
		t.Run(trigger.name, func(t *testing.T) { slowRetentionBatchDoesNotDelayFinishProbe(t, trigger.create) })
	}
}

func slowRetentionBatchDoesNotDelayFinishProbe(t *testing.T, createTrigger string) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	cfg := failureConfig(t, []dispatch.Target{a}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	now := dispatchNow(t, db)
	dispatchExec(t, db, `INSERT INTO dispatch.observations(core_instance,event_id,payload,recorded_at)
	 SELECT 'core','old-'||i,'{}'::jsonb,$1::timestamptz-make_interval(secs=>i) FROM generate_series(1,10) AS i`, now.Add(-5*24*time.Hour))
	lease, err := store.ClaimProbe(t.Context(), "core", "a", "retention-owner", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	probe := dispatchObservation(t, db, a, lease.AttemptID, dispatch.Success)
	dispatchExec(t, db, `CREATE FUNCTION dispatch.slow_retention() RETURNS trigger LANGUAGE plpgsql AS $$
	 BEGIN PERFORM pg_sleep(4); RETURN NULL; END $$`)
	dispatchExec(t, db, fmt.Sprintf(createTrigger, "slow_retention", "DELETE", "dispatch.observations"))
	monitoring := dispatch.MonitoringConfig{Retention: &dispatch.RetentionConfig{ObservationDays: 2}}
	cleaned := make(chan error, 1)
	go func() {
		_, err := store.ApplyRetention(context.Background(), "core", monitoring)
		cleaned <- err
	}()
	require.Eventually(t, func() bool {
		var sleeping int
		err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event='PgSleep'`).Scan(&sleeping)
		return err == nil && sleeping == 1
	}, 5*time.Second, 10*time.Millisecond, "the retention batch holds the store lock")
	finishCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	err = store.FinishProbe(finishCtx, *lease, probe)
	cancel()
	require.NoError(t, err, "a slow retention batch must release the store lock before the settlement deadline")
	require.Error(t, <-cleaned)
	require.Equal(t, 10, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE event_id LIKE 'old-%'`))
	dispatchExec(t, db, `DROP TRIGGER slow_retention ON dispatch.observations`)
	results, err := store.ApplyRetention(t.Context(), "core", monitoring)
	require.NoError(t, err)
	require.EqualValues(t, 10, results[0].Deleted)
}

func rollbackMetricsSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	dispatchExec(t, db, `DROP TABLE dispatch.metric_rollups, dispatch.metric_minutes, dispatch.metric_hours`)
	dispatchExec(t, db, `DROP INDEX dispatch.observations_retention, dispatch.probes_retention, dispatch.health_hold_audit_event`)
	dispatchExec(t, db, `DELETE FROM dispatch.schema_migrations WHERE version='006'`)
}

func TestDispatchMetricsSchemaUpgradeFrom005KeepsLineage(t *testing.T) {
	db := dispatchDB(t)
	rollbackMetricsSchema(t, db)
	store := dispatch.NewStore(db)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)
	cfg.SnapshotServer = &dispatch.SnapshotServerConfig{SocketPath: "/run/dispatchd/snapshot.sock", KeysFile: "unused.json"}
	published := lineageRound(t, store, cfg)
	lineage, reason := lineageRow(t, db)
	var checksums string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT string_agg(version||':'||checksum,',' ORDER BY version) FROM dispatch.schema_migrations`).Scan(&checksums))
	_, err := store.RollupMetrics(t.Context(), "core")
	require.ErrorIs(t, err, dispatch.ErrMetricsSchema, "a 005 database rejects rollup until 006 is applied")

	require.NoError(t, dispatch.ApplySchema(t.Context(), db))
	require.NoError(t, dispatch.ApplySchema(t.Context(), db))
	var after string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT string_agg(version||':'||checksum,',' ORDER BY version) FROM dispatch.schema_migrations WHERE version<>'006'`).Scan(&after))
	require.Equal(t, checksums, after, "001-005 keep their recorded bytes")
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.schema_migrations WHERE version='006'`))
	gotLineage, gotReason := lineageRow(t, db)
	require.Equal(t, lineage, gotLineage)
	require.Equal(t, reason, gotReason)
	next := lineageRound(t, store, cfg)
	require.Equal(t, published.Lineage, next.Lineage, "006 upgrade keeps the publish lineage")
	_, err = store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
}

func TestDispatchMetricsSchemaUpgradeFrom004KeepsHistory(t *testing.T) {
	db := dispatchDB(t)
	probeBudgetLegacySchema(t, db, true)
	body, err := os.ReadFile("../custom/dispatch/migrations/004_probe_budget.sql")
	require.NoError(t, err)
	dispatchExec(t, db, string(body))
	dispatchExec(t, db, `INSERT INTO dispatch.schema_migrations(version,checksum) VALUES ('004',$1)`, fmt.Sprintf("%x", sha256.Sum256(body)))
	store := dispatch.NewStore(db)
	a := dispatchTarget("legacy", 1)
	cfg := failureConfig(t, []dispatch.Target{a}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg), "runtime works before the explicit 006 upgrade")
	lease, err := store.ClaimProbe(t.Context(), "core", a.Ref, "legacy-worker", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	probeScheduleFinish(t, db, store, lease, dispatch.Success)

	_, err = store.RollupMetrics(t.Context(), "core")
	require.ErrorIs(t, err, dispatch.ErrMetricsSchema)
	_, err = store.ApplyRetention(t.Context(), "core", dispatch.MonitoringConfig{Retention: &dispatch.RetentionConfig{ObservationDays: 1}})
	require.ErrorIs(t, err, dispatch.ErrMetricsSchema)
	report, err := store.MetricsReport(t.Context(), "core", 1)
	require.NoError(t, err)
	require.False(t, report.SchemaReady)
	require.Len(t, report.History, 2)
	state, err := store.MetricsState(t.Context(), "core", 1)
	require.NoError(t, err)
	require.False(t, state.MetricsReady)

	var checksums string
	readChecksums := func() string {
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT string_agg(version||':'||checksum,',' ORDER BY version)
		 FROM dispatch.schema_migrations WHERE version IN ('001','003','004')`).Scan(&checksums))
		return checksums
	}
	legacyChecksums := readChecksums()
	before := probeBudgetOperationalState(t, db)
	require.NoError(t, dispatch.ApplySchema(t.Context(), db))
	require.NoError(t, dispatch.ApplySchema(t.Context(), db))
	require.Equal(t, before, probeBudgetOperationalState(t, db))
	require.Equal(t, legacyChecksums, readChecksums(), "applied migrations keep their recorded bytes")
	require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.schema_migrations WHERE version IN ('005','006')`), "004 upgrades through 005 to 006")
	_, err = store.RollupMetrics(t.Context(), "core")
	require.NoError(t, err)
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.metric_minutes`), "history before enablement is not backfilled")

	dispatchExec(t, db, `UPDATE dispatch.schema_migrations SET checksum='tampered' WHERE version='006'`)
	_, err = store.RollupMetrics(t.Context(), "core")
	require.ErrorIs(t, err, dispatch.ErrMetricsSchema)
	require.Error(t, dispatch.ApplySchema(t.Context(), db))
}
