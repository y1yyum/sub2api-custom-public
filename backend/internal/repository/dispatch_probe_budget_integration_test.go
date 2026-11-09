//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func probeBudgetConfig(t *testing.T, core string, daily, providerDaily, price int64, targets ...dispatch.Target) dispatch.Config {
	t.Helper()
	cfg := failureConfig(t, targets, false)
	cfg.CoreInstance = core
	cfg.Probe.CoreOrigin = "http://127.0.0.1:8080"
	cfg.Probe.AllowLoopbackHTTP = true
	cfg.Probe.Audience = core
	cfg.Probe.CredentialsFile = "unused-synthetic-credentials.json"
	cfg.Probe.ReceiptSource = "synthetic-receipts"
	cfg.Probe.RequestTimeoutSeconds = 10
	cfg.Probe.ReceiptWaitSeconds = 3
	cfg.ProbeBudget = &dispatch.ProbeBudgetConfig{Currency: "USD", DailyMicrousd: daily,
		ProviderDailyMicrousd: providerDaily, ActivationEvidenceRef: "operator-activation-234"}
	seen := map[[2]string]bool{}
	for _, target := range targets {
		key := [2]string{target.ProviderRef, target.Model}
		if !target.Enabled || seen[key] {
			continue
		}
		seen[key] = true
		cfg.ProbeBudget.Prices = append(cfg.ProbeBudget.Prices, dispatch.ProbePriceBound{
			ProviderRef: target.ProviderRef, Model: target.Model, Revision: "price-1",
			EvidenceRef: "operator-price-234", MaxMicrousd: price})
	}
	return cfg
}

func probeBudgetConnection(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(t.Context()))
	return db
}

func probeBudgetClaim(t *testing.T, store *dispatch.Store, cfg dispatch.Config, ref string) *dispatch.ProbeLease {
	t.Helper()
	lease, err := store.ClaimProbe(t.Context(), cfg.CoreInstance, ref, "budget-worker", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	require.NotNil(t, lease.Budget)
	require.Equal(t, "USD", lease.Budget.Currency)
	require.Equal(t, "price-1", lease.Budget.PriceRevision)
	return lease
}

func probeBudgetDenied(t *testing.T, store *dispatch.Store, cfg dispatch.Config, ref string) {
	t.Helper()
	lease, err := store.ClaimProbe(t.Context(), cfg.CoreInstance, ref, "denied-worker", cfg.ProbeLimits())
	require.NoError(t, err)
	require.Nil(t, lease, "a rejected monetary reservation must not return an executable lease")
}

func probeBudgetReport(t *testing.T, store *dispatch.Store, cfg dispatch.Config) dispatch.ProbeBudgetReport {
	t.Helper()
	report, err := store.ProbeBudgetReport(t.Context(), cfg.CoreInstance, cfg.ConfigRevision)
	require.NoError(t, err)
	require.True(t, report.SchemaReady)
	return report
}

func probeBudgetEntry(t *testing.T, report dispatch.ProbeBudgetReport, token string) dispatch.ProbeBudgetEntry {
	t.Helper()
	for _, entry := range report.Entries {
		if entry.Token == token {
			return entry
		}
	}
	t.Fatalf("reservation %s is absent from report", token)
	return dispatch.ProbeBudgetEntry{}
}

func TestDispatchProbeBudgetConcurrentAcrossConnectionsAndCores(t *testing.T) {
	db := dispatchDB(t)
	other := probeBudgetConnection(t)
	stores := []*dispatch.Store{dispatch.NewStore(db), dispatch.NewStore(other)}
	configs := make([]dispatch.Config, 2)
	for core := range configs {
		targets := make([]dispatch.Target, 12)
		for i := range targets {
			targets[i] = dispatchTarget(fmt.Sprintf("target-%d", i), int64(core*100+i+1))
		}
		cap := int64(1000)
		if core == 1 {
			cap = 3000
		}
		configs[core] = probeBudgetConfig(t, fmt.Sprintf("core-%d", core), cap, cap, 300, targets...)
		require.NoError(t, stores[core].SyncRuntimeConfig(t.Context(), configs[core]))
	}
	type result struct {
		lease *dispatch.ProbeLease
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 24)
	var wg sync.WaitGroup
	for core, cfg := range configs {
		for _, target := range cfg.Targets {
			wg.Go(func() {
				<-start
				lease, err := stores[core].ClaimProbe(t.Context(), cfg.CoreInstance, target.Ref, "parallel-worker", cfg.ProbeLimits())
				results <- result{lease, err}
			})
		}
	}
	close(start)
	wg.Wait()
	close(results)
	var reserved int64
	claimed := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.lease != nil {
			require.NotNil(t, result.lease.Budget)
			require.EqualValues(t, 300, result.lease.Budget.MaxMicrousd)
			reserved += result.lease.Budget.MaxMicrousd
			claimed++
		}
	}
	require.Equal(t, 3, claimed, "24 distinct domains must contend for one shared monetary cap")
	require.EqualValues(t, 900, reserved)
	require.LessOrEqual(t, reserved, int64(1000))
	require.Equal(t, claimed, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
	for core, cfg := range configs {
		report := probeBudgetReport(t, stores[core], cfg)
		require.NotNil(t, report.GlobalDailyMicrousd)
		require.EqualValues(t, 1000, *report.GlobalDailyMicrousd)
		require.EqualValues(t, reserved, report.Global.ReservedMicrousd)
		require.EqualValues(t, reserved, report.Global.ExposureMicrousd)
	}
}

func TestDispatchProbeBudgetProviderIsolationAndSharedGlobal(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b, c, d := dispatchTarget("a", 1), dispatchTarget("b", 2), dispatchTarget("c", 3), dispatchTarget("d", 4)
	c.ProviderRef, d.ProviderRef = "other-provider", "other-provider"
	low := probeBudgetConfig(t, "low-core", 1200, 400, 400, a, b)
	high := probeBudgetConfig(t, "high-core", 2000, 1200, 400, c, d)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), low))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), high))
	probeBudgetClaim(t, store, low, a.Ref)
	probeBudgetDenied(t, store, low, b.Ref)
	probeBudgetClaim(t, dispatch.NewStore(probeBudgetConnection(t)), high, c.Ref)
	probeBudgetClaim(t, store, high, d.Ref)
	global := probeBudgetReport(t, store, high)
	require.EqualValues(t, 1200, global.Global.ExposureMicrousd)
	require.Len(t, global.Providers, 1)
	require.Equal(t, "other-provider", global.Providers[0].ProviderRef)
	require.EqualValues(t, 800, global.Providers[0].ExposureMicrousd)

	// 增加新域只为区分全局限额与原 Provider 限额，不修改既有账目。
	high.ConfigRevision++
	high.Targets = append(high.Targets, dispatchTarget("e", 5))
	high.Targets[2].ProviderRef = "other-provider"
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), high))
	probeBudgetDenied(t, store, high, "e")
	require.Equal(t, 3, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
}

func TestDispatchProbeBudgetProviderMinimumAcrossActiveCores(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	other := dispatch.NewStore(probeBudgetConnection(t))
	low := probeBudgetConfig(t, "low-core", 3000, 400, 400, dispatchTarget("a", 1))
	high := probeBudgetConfig(t, "high-core", 3000, 1200, 400, dispatchTarget("b", 2), dispatchTarget("c", 3))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), low))
	require.NoError(t, other.SyncRuntimeConfig(t.Context(), high))
	probeBudgetClaim(t, other, high, "b")
	probeBudgetDenied(t, other, high, "c")
	probeBudgetDenied(t, store, low, "a")
	report := probeBudgetReport(t, other, high)
	require.EqualValues(t, 3000, *report.GlobalDailyMicrousd)
	require.EqualValues(t, 400, report.Global.ExposureMicrousd, "the global cap still has room")
	require.Len(t, report.Providers, 1)
	require.NotNil(t, report.Providers[0].DailyMicrousd)
	require.EqualValues(t, 400, *report.Providers[0].DailyMicrousd)
	low.ConfigRevision++
	low.Probe, low.ProbeBudget = nil, nil
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), low))
	probeBudgetClaim(t, other, high, "c")
	report = probeBudgetReport(t, other, high)
	require.EqualValues(t, 800, report.Global.ReservedMicrousd)
	require.EqualValues(t, 1200, *report.Providers[0].DailyMicrousd)
	require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
}

func TestDispatchProbeBudgetRevisionCredentialAliasAndRestartRetainDebt(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	cfg := probeBudgetConfig(t, "core", 600, 600, 600, a)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	lease := probeBudgetClaim(t, store, cfg, a.Ref)
	probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
	before := probeBudgetReport(t, store, cfg)
	require.EqualValues(t, 600, before.Global.UnknownMicrousd)

	a.CredentialRevision++
	alias := a
	alias.Ref, alias.PoolRef = "alias", "other-pool"
	cfg.ConfigRevision++
	cfg.Targets = []dispatch.Target{a, alias}
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	dispatchExec(t, db, `UPDATE dispatch.probes SET slot=slot-10`)
	restarted := dispatch.NewStore(probeBudgetConnection(t))
	for _, target := range cfg.Targets {
		probeBudgetDenied(t, restarted, cfg, target.Ref)
	}
	after := probeBudgetReport(t, restarted, cfg)
	require.Equal(t, before.Global, after.Global)
	require.EqualValues(t, 1, after.EntryCount)
	require.Equal(t, lease.Token, after.Entries[0].Token)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
}

func TestDispatchProbeBudgetPriceBoundImmutableAndRevisionFenced(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeBudgetConfig(t, "core", 1000, 1000, 300, dispatchTarget("a", 1), dispatchTarget("b", 2))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	lease := probeBudgetClaim(t, store, cfg, "a")
	probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
	for _, field := range []string{"amount", "evidence", "daily"} {
		t.Run(field, func(t *testing.T) {
			changed := cfg
			budget := *cfg.ProbeBudget
			budget.Prices = append([]dispatch.ProbePriceBound(nil), budget.Prices...)
			changed.ProbeBudget = &budget
			switch field {
			case "amount":
				budget.Prices[0].MaxMicrousd++
			case "evidence":
				budget.Prices[0].EvidenceRef = "different-attestation"
			case "daily":
				budget.DailyMicrousd++
			}
			require.Error(t, store.SyncRuntimeConfig(t.Context(), changed), "the same config revision cannot change a budget")
			if field != "daily" {
				changed.ConfigRevision++
				require.Error(t, store.SyncRuntimeConfig(t.Context(), changed), "an existing Provider/model/price revision must remain immutable")
			}
			require.EqualValues(t, 1, dispatchConfigRevision(t, db, "core"))
		})
	}
	oldLimits := cfg.ProbeLimits()
	conflicting := probeBudgetConfig(t, "conflicting-core", 1000, 1000, 301, dispatchTarget("other", 3))
	require.ErrorIs(t, dispatch.NewStore(probeBudgetConnection(t)).SyncRuntimeConfig(t.Context(), conflicting), dispatch.ErrProbeBudgetConflict,
		"price tuples are immutable across Core instances as well as revisions")
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.cores WHERE core_instance='conflicting-core'`))
	cfg.ConfigRevision++
	budget := *cfg.ProbeBudget
	budget.Prices = append([]dispatch.ProbePriceBound(nil), budget.Prices...)
	budget.Prices[0].Revision = "price-2"
	budget.Prices[0].MaxMicrousd = 400
	cfg.ProbeBudget = &budget
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	stale, err := store.ClaimProbe(t.Context(), "core", "b", "stale-worker", oldLimits)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	require.Nil(t, stale)
	current, err := store.ClaimProbe(t.Context(), "core", "b", "current-worker", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, current)
	require.NotNil(t, current.Budget)
	require.Equal(t, "price-2", current.Budget.PriceRevision)
	require.EqualValues(t, 400, current.Budget.MaxMicrousd)
	report := probeBudgetReport(t, store, cfg)
	require.EqualValues(t, 700, report.Global.ExposureMicrousd)
	require.EqualValues(t, 300, probeBudgetEntry(t, report, lease.Token).MaxMicrousd)
}

func TestDispatchProbeBudgetPublishNormalizesPriceOrder(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b := dispatchTarget("a", 1), dispatchTarget("b", 2)
	a.ProviderRef, b.ProviderRef = "z-provider", "a-provider"
	cfg := probeBudgetConfig(t, "core", 1000, 1000, 300, a, b)
	cfg.Telemetry = &dispatch.TelemetryConfig{SlotTTLSeconds: 1800, LoadMaxAgeSeconds: 5, MaxObservations: 100}
	cfg.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}
	require.Equal(t, "z-provider", cfg.ProbeBudget.Prices[0].ProviderRef, "input prices intentionally reverse canonical order")
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	require.Equal(t, "z-provider", cfg.ProbeBudget.Prices[0].ProviderRef, "sync normalization must preserve the caller's config")
	load := &dispatch.LoadSample{ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{1: 0, 2: 0}}
	snapshot, err := store.PublishRuntime(t.Context(), cfg, load)
	require.NoError(t, err, "semantically identical price order must not fail the persisted routing digest fence")
	require.Len(t, snapshot.Candidates, 2)
	require.Positive(t, snapshot.Revision)
	for _, candidate := range snapshot.Candidates {
		require.Positive(t, candidate.EffectiveWeight)
	}
	load.ObservedAt = dispatchNow(t, db)
	next, err := dispatch.NewStore(probeBudgetConnection(t)).PublishRuntime(t.Context(), cfg, load)
	require.NoError(t, err)
	require.Greater(t, next.Revision, snapshot.Revision)
	require.EqualValues(t, cfg.ConfigRevision, dispatchConfigRevision(t, db, cfg.CoreInstance))
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money`), "publishing does not reserve probe money")
}

func TestDispatchProbeBudgetFinishChargesCeilingOnlyWithProviderSuccess(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcome  dispatch.Outcome
		evidence bool
		state    string
	}{
		{"success", dispatch.Success, true, "charged"},
		{"success-without-evidence", dispatch.Success, false, "unknown"},
		{"unknown", dispatch.Unknown, false, "unknown"},
		{"provider-failure", dispatch.UpstreamFailure, true, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			cfg := probeBudgetConfig(t, "core", 601, 601, 601, dispatchTarget("a", 1), dispatchTarget("b", 2))
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			lease := probeBudgetClaim(t, store, cfg, "a")
			frozen := *lease.Budget
			// 调用方的副本不能降低账目中已冻结的上界或改变价格依据。
			lease.Budget.MaxMicrousd = 1
			lease.Budget.PriceRevision = "forged-price"
			o := dispatchObservation(t, db, lease.Target, lease.AttemptID, tc.outcome)
			o.ProviderEvidence = tc.evidence
			require.NoError(t, store.FinishProbe(t.Context(), *lease, o))
			report := probeBudgetReport(t, store, cfg)
			entry := probeBudgetEntry(t, report, lease.Token)
			require.Equal(t, frozen, entry.ProbeBudgetReservation)
			require.Equal(t, tc.state, entry.State)
			require.EqualValues(t, 2, entry.Version)
			require.EqualValues(t, 601, report.Global.ExposureMicrousd)
			if tc.state == "charged" {
				require.EqualValues(t, 601, report.Global.ChargedMicrousd)
				require.NotNil(t, entry.SettledAt)
			} else {
				require.EqualValues(t, 601, report.Global.UnknownMicrousd)
				require.Nil(t, entry.SettledAt)
			}
			probeBudgetDenied(t, store, cfg, "b")
			require.ErrorIs(t, store.FinishProbe(t.Context(), *lease, o), dispatch.ErrProbeLease)
			require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
			require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money_audit WHERE token=$1`, lease.Token))
		})
	}
}

func TestDispatchProbeBudgetExpiryKeepsReservationAndRequestLimits(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeBudgetConfig(t, "core", 500, 500, 500, dispatchTarget("a", 1), dispatchTarget("b", 2))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	lease := probeBudgetClaim(t, store, cfg, "a")
	dispatchExec(t, db, `UPDATE dispatch.probes SET lease_until=clock_timestamp()-interval '1 microsecond' WHERE token=$1`, lease.Token)
	require.ErrorIs(t, store.FinishProbe(t.Context(), *lease,
		dispatchObservation(t, db, lease.Target, lease.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	probeBudgetDenied(t, dispatch.NewStore(probeBudgetConnection(t)), cfg, "b")
	report := probeBudgetReport(t, store, cfg)
	require.Equal(t, "reserved", probeBudgetEntry(t, report, lease.Token).State)
	require.EqualValues(t, 500, report.Global.ReservedMicrousd)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
}

// 仅插入合成历史/未来账目；不可变日期列和数据库时钟均保持原约束。
func probeBudgetSeed(t *testing.T, db *sql.DB, cfg dispatch.Config, target dispatch.Target, at time.Time, state string, count int) []string {
	t.Helper()
	var price dispatch.ProbePriceBound
	for _, candidate := range cfg.ProbeBudget.Prices {
		if candidate.ProviderRef == target.ProviderRef && candidate.Model == target.Model {
			price = candidate
		}
	}
	require.NotEmpty(t, price.Revision)
	metadata, err := json.Marshal(target)
	require.NoError(t, err)
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	tokens := make([]string, count)
	for i := range tokens {
		token := uuid.NewString()
		tokens[i] = token
		var finished, settled any
		if state != "reserved" {
			finished = at.Add(time.Second)
		}
		if state == "charged" {
			settled = at.Add(time.Second)
		}
		_, err = tx.ExecContext(t.Context(), `INSERT INTO dispatch.probes
		 (token,core_instance,target_ref,target,generation,owner,account_id,model,region,provider_ref,slot,attempt_id,reservation_id,reserved_at,lease_until,finished_at)
		 VALUES ($1,$2,$3,$4,1,'synthetic-history',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			token, cfg.CoreInstance, target.Ref, string(metadata), target.AccountID, target.Model, target.Region, target.ProviderRef,
			at.Unix()/30-int64(i+1), "dispatch-attempt:"+token, "dispatch-reservation:"+token, at, at.Add(time.Minute), finished)
		require.NoError(t, err)
		_, err = tx.ExecContext(t.Context(), `INSERT INTO dispatch.probe_money
		 (token,currency,provider_ref,model,price_revision,evidence_ref,max_microusd,reserved_at,budget_day,state,version,settled_at)
		 VALUES ($1,'USD',$2,$3,$4,$5,$6,$7,($7::timestamptz AT TIME ZONE 'UTC')::date,$8,1,$9)`,
			token, target.ProviderRef, target.Model, price.Revision, price.EvidenceRef, price.MaxMicrousd, at, state, settled)
		require.NoError(t, err)
		_, err = tx.ExecContext(t.Context(), `INSERT INTO dispatch.probe_money_audit(token,version,action,recorded_at) VALUES ($1,1,$2,$3)`,
			token, map[string]string{"reserved": "reserve", "unknown": "unknown", "charged": "charge-ceiling"}[state], at)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	return tokens
}

func TestDispatchProbeBudgetUTCDayCarryAndClockRollback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		day    int
		state  string
		blocks bool
	}{
		{"previous-day-reserved", -1, "reserved", true},
		{"previous-day-unknown", -1, "unknown", true},
		{"previous-day-charged", -1, "charged", false},
		{"current-day-charged", 0, "charged", true},
		{"future-day-charged", 1, "charged", true},
		{"future-day-unknown", 1, "unknown", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			cfg := probeBudgetConfig(t, "core", 500, 500, 500, dispatchTarget("history", 1), dispatchTarget("new", 2))
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			now := dispatchNow(t, db)
			day := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC).AddDate(0, 0, tc.day)
			tokens := probeBudgetSeed(t, db, cfg, cfg.Targets[0], day, tc.state, 1)
			report := probeBudgetReport(t, store, cfg)
			require.Equal(t, day.Format("2006-01-02"), probeBudgetEntry(t, report, tokens[0]).BudgetDay)
			if tc.blocks {
				require.EqualValues(t, 500, report.Global.ExposureMicrousd)
				probeBudgetDenied(t, dispatch.NewStore(probeBudgetConnection(t)), cfg, "new")
			} else {
				require.Zero(t, report.Global.ExposureMicrousd)
				probeBudgetClaim(t, store, cfg, "new")
			}
			require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money WHERE token=$1 AND state=$2`, tokens[0], tc.state))
		})
	}
}

func TestDispatchProbeBudgetActivationFencesLegacyWorkers(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	legacy := failureConfig(t, []dispatch.Target{dispatchTarget("legacy", 1)}, false)
	legacy.CoreInstance = "legacy-core"
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), legacy))
	lease, err := store.ClaimProbe(t.Context(), legacy.CoreInstance, "legacy", "legacy-worker", legacy.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	require.Nil(t, lease.Budget, "nil budget retains pre-activation behavior")
	cfg := probeBudgetConfig(t, "budget-core", 500, 500, 500, dispatchTarget("priced", 2))
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), cfg), dispatch.ErrProbeBudgetConflict)
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_budget_latch`))
	probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	report := probeBudgetReport(t, store, cfg)
	require.True(t, report.Enabled)
	require.NotNil(t, report.Activation)
	require.Equal(t, cfg.ProbeBudget.ActivationEvidenceRef, report.Activation.ActivationEvidenceRef)
	require.Equal(t, cfg.CoreInstance, report.Activation.CoreInstance)
	require.True(t, report.Fenced, "a legacy active worker without a money policy must fence new claims")
	_, err = store.ClaimProbe(t.Context(), legacy.CoreInstance, "legacy", "restart", legacy.ProbeLimits())
	require.ErrorIs(t, err, dispatch.ErrProbeBudgetRequired)
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), legacy), dispatch.ErrProbeBudgetRequired)
	_, err = store.ClaimProbe(t.Context(), cfg.CoreInstance, "priced", "priced-worker", cfg.ProbeLimits())
	require.ErrorIs(t, err, dispatch.ErrProbeBudgetRequired)
	legacy.ConfigRevision++
	legacy.Probe = nil
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), legacy))
	require.False(t, probeBudgetReport(t, store, cfg).Fenced)
	probeBudgetClaim(t, store, cfg, "priced")
	newLegacy := failureConfig(t, []dispatch.Target{dispatchTarget("new", 3)}, false)
	newLegacy.CoreInstance = "new-legacy"
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), newLegacy), dispatch.ErrProbeBudgetRequired)
}

func TestDispatchProbeBudgetLegacyConfigOverwriteFencesCohort(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	other := dispatch.NewStore(probeBudgetConnection(t))
	a := probeBudgetConfig(t, "core-a", 1000, 1000, 100, dispatchTarget("existing", 1), dispatchTarget("next", 2))
	b := probeBudgetConfig(t, "core-b", 1000, 1000, 100, dispatchTarget("existing", 3))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), a))
	require.NoError(t, other.SyncRuntimeConfig(t.Context(), b))
	first := probeBudgetClaim(t, store, a, "existing")
	probeScheduleFinish(t, db, store, first, dispatch.Unknown)
	probeBudgetClaim(t, other, b, "existing")
	for _, cfg := range []dispatch.Config{a, b} {
		report := probeBudgetReport(t, store, cfg)
		require.True(t, report.Active)
		require.False(t, report.Fenced)
		require.EqualValues(t, 200, report.Global.ExposureMicrousd)
	}

	// 模拟旧程序同步更高版次：仍启用探针，但不认识并丢弃金额配置。
	b.ConfigRevision++
	dispatchExec(t, db, `UPDATE dispatch.cores SET config_revision=$2,
	 probe_limits=jsonb_set(probe_limits-'probe_budget','{config_revision}',to_jsonb($2::bigint))
	 WHERE core_instance=$1`, b.CoreInstance, b.ConfigRevision)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.cores c
	 JOIN dispatch.probe_budget_policies p USING(core_instance) WHERE c.core_instance=$1
	 AND (c.probe_limits->>'lease_duration')::bigint>0 AND NOT(c.probe_limits?'probe_budget') AND p.active`, b.CoreInstance))

	moneyState := func() string {
		var snapshot string
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT jsonb_build_object(
		 'money',(SELECT jsonb_agg(to_jsonb(m) ORDER BY token) FROM dispatch.probe_money m),
		 'audit',(SELECT jsonb_agg(to_jsonb(h) ORDER BY audit_id) FROM dispatch.probe_money_audit h),
		 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY core_instance) FROM dispatch.probe_budget_policies p),
		 'prices',(SELECT jsonb_agg(to_jsonb(q) ORDER BY provider_ref,model,revision) FROM dispatch.probe_prices q),
		 'latch',(SELECT jsonb_agg(to_jsonb(l)) FROM dispatch.probe_budget_latch l))::text`).Scan(&snapshot))
		return snapshot
	}
	beforeOperational := probeBudgetOperationalState(t, db)
	beforeMoney := moneyState()
	reportA := probeBudgetReport(t, store, a)
	reportB := probeBudgetReport(t, other, b)
	assert.True(t, reportA.Fenced, "an active peer missing its frozen budget must fence the whole cohort")
	assert.True(t, reportB.Fenced)
	assert.True(t, reportA.Active)
	assert.False(t, reportB.Active, "a stale policy table flag cannot make an overwritten Core config active")
	assert.EqualValues(t, 200, reportA.Global.ExposureMicrousd)
	assert.Equal(t, reportA.Global, reportB.Global)
	lease, err := dispatch.NewStore(probeBudgetConnection(t)).ClaimProbe(t.Context(), a.CoreInstance, "next", "cohort-worker", a.ProbeLimits())
	assert.ErrorIs(t, err, dispatch.ErrProbeBudgetRequired)
	assert.Nil(t, lease)
	assert.Equal(t, beforeOperational, probeBudgetOperationalState(t, db), "rejected claims must preserve health, generations, counts and RPM facts")
	assert.Equal(t, beforeMoney, moneyState(), "reporting and a fenced claim must preserve all monetary state and audit history")
}

func TestDispatchProbeBudgetStoppingProbeRetainsDebt(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeBudgetConfig(t, "core", 500, 500, 500, dispatchTarget("a", 1))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	lease := probeBudgetClaim(t, store, cfg, "a")
	probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
	cfg.ConfigRevision++
	cfg.Probe, cfg.ProbeBudget = nil, nil
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	report := probeBudgetReport(t, store, cfg)
	require.True(t, report.Enabled)
	require.False(t, report.Active)
	require.EqualValues(t, 500, report.Global.UnknownMicrousd)
	require.EqualValues(t, 500, report.Global.ExposureMicrousd)
	require.EqualValues(t, 1, report.EntryCount)
	next := probeBudgetConfig(t, "other-core", 500, 500, 500, dispatchTarget("b", 2))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), next))
	probeBudgetDenied(t, dispatch.NewStore(probeBudgetConnection(t)), next, "b")
}

func probeBudgetCloseRequest(entry dispatch.ProbeBudgetEntry) dispatch.ProbeBudgetCloseRequest {
	return dispatch.ProbeBudgetCloseRequest{Token: entry.Token, ExpectedVersion: entry.Version,
		Actor: "operator-234", Reason: "操作者确认按整次请求上界结清", EvidenceRef: "operator-close-234"}
}

func probeBudgetOperationalState(t *testing.T, db *sql.DB) string {
	t.Helper()
	var snapshot string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT jsonb_build_object(
	 'cores',(SELECT jsonb_agg(to_jsonb(c) ORDER BY core_instance) FROM dispatch.cores c),
	 'targets',(SELECT jsonb_agg(to_jsonb(t) ORDER BY core_instance,target_ref) FROM dispatch.targets t),
	 'probes',(SELECT jsonb_agg(to_jsonb(p) ORDER BY token) FROM dispatch.probes p),
	 'observations',(SELECT jsonb_agg(to_jsonb(o) ORDER BY core_instance,event_id) FROM dispatch.observations o))::text`).Scan(&snapshot))
	return snapshot
}

func TestDispatchProbeBudgetCloseCASIdempotencyAuditAndNoHealthChange(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired-%v", expired), func(t *testing.T) {
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			other := dispatch.NewStore(probeBudgetConnection(t))
			cfg := probeBudgetConfig(t, "core", 500, 500, 500, dispatchTarget("a", 1), dispatchTarget("b", 2))
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			lease := probeBudgetClaim(t, store, cfg, "a")
			live := probeBudgetEntry(t, probeBudgetReport(t, store, cfg), lease.Token)
			_, err := store.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, probeBudgetCloseRequest(live))
			require.ErrorIs(t, err, dispatch.ErrProbeBudgetConflict, "a live unfinished probe cannot be manually closed")
			if expired {
				dispatchExec(t, db, `UPDATE dispatch.probes SET lease_until=clock_timestamp()-interval '1 microsecond' WHERE token=$1`, lease.Token)
			} else {
				probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
			}
			entry := probeBudgetEntry(t, probeBudgetReport(t, store, cfg), lease.Token)
			request := probeBudgetCloseRequest(entry)
			before := probeBudgetOperationalState(t, db)
			type result struct {
				entry dispatch.ProbeBudgetEntry
				err   error
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i, worker := range []*dispatch.Store{store, other} {
				wg.Go(func() {
					<-start
					competing := request
					if i == 1 {
						competing.Actor = "other-operator"
					}
					closed, err := worker.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, competing)
					results <- result{closed, err}
				})
			}
			close(start)
			wg.Wait()
			close(results)
			var winner dispatch.ProbeBudgetEntry
			successes, conflicts := 0, 0
			for result := range results {
				if result.err == nil {
					winner = result.entry
					successes++
				} else {
					require.ErrorIs(t, result.err, dispatch.ErrProbeBudgetConflict)
					conflicts++
				}
			}
			require.Equal(t, 1, successes)
			require.Equal(t, 1, conflicts)
			require.Equal(t, "charged", winner.State)
			require.Equal(t, entry.Version+1, winner.Version)
			require.Equal(t, entry.ProbeBudgetReservation, winner.ProbeBudgetReservation)
			require.NotNil(t, winner.CloseRequest)
			require.NotNil(t, winner.SettledAt)
			for range 3 {
				retry, err := other.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, *winner.CloseRequest)
				require.NoError(t, err)
				require.Equal(t, winner, retry, "same request returns the originally committed result")
			}
			for _, mutate := range []func(*dispatch.ProbeBudgetCloseRequest){
				func(r *dispatch.ProbeBudgetCloseRequest) { r.Reason = "different reason" },
				func(r *dispatch.ProbeBudgetCloseRequest) { r.EvidenceRef = "different-evidence" },
				func(r *dispatch.ProbeBudgetCloseRequest) { r.ExpectedVersion++ },
			} {
				changed := *winner.CloseRequest
				mutate(&changed)
				_, err := other.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, changed)
				require.ErrorIs(t, err, dispatch.ErrProbeBudgetConflict)
			}
			_, err = other.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision+1, *winner.CloseRequest)
			require.ErrorIs(t, err, dispatch.ErrConfigRevision)
			require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money_audit WHERE token=$1 AND action='manual-close'`, lease.Token))
			var audited dispatch.ProbeBudgetCloseRequest
			var raw []byte
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT request FROM dispatch.probe_money_audit
			 WHERE token=$1 AND version=$2 AND actor=$3 AND reason=$4 AND evidence_ref=$5`,
				lease.Token, winner.Version, winner.CloseRequest.Actor, winner.CloseRequest.Reason, winner.CloseRequest.EvidenceRef).Scan(&raw))
			require.NoError(t, json.Unmarshal(raw, &audited))
			require.Equal(t, *winner.CloseRequest, audited)
			report := probeBudgetReport(t, store, cfg)
			require.EqualValues(t, 500, report.Global.ChargedMicrousd)
			require.EqualValues(t, 500, report.Global.ExposureMicrousd)
			require.Zero(t, report.Global.ReservedMicrousd+report.Global.UnknownMicrousd)
			require.Equal(t, before, probeBudgetOperationalState(t, db), "manual money settlement cannot change health, request counts, RPM facts or replay state")
			probeBudgetDenied(t, store, cfg, "b")
		})
	}
}

func TestDispatchProbeBudgetCloseAuditFailureRollsBackCharge(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeBudgetConfig(t, "core", 500, 500, 500, dispatchTarget("a", 1))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	lease := probeBudgetClaim(t, store, cfg, "a")
	probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
	before := probeBudgetEntry(t, probeBudgetReport(t, store, cfg), lease.Token)
	dispatchExec(t, db, `CREATE FUNCTION dispatch.reject_budget_audit() RETURNS trigger LANGUAGE plpgsql AS $$
	 BEGIN RAISE EXCEPTION 'synthetic audit failure'; END $$;
	 CREATE TRIGGER reject_budget_audit BEFORE INSERT ON dispatch.probe_money_audit
	 FOR EACH ROW EXECUTE FUNCTION dispatch.reject_budget_audit()`)
	_, err := store.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, probeBudgetCloseRequest(before))
	require.ErrorContains(t, err, "synthetic audit failure")
	require.Equal(t, before, probeBudgetEntry(t, probeBudgetReport(t, store, cfg), lease.Token))
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money_audit WHERE action='manual-close'`))
	dispatchExec(t, db, `DROP TRIGGER reject_budget_audit ON dispatch.probe_money_audit; DROP FUNCTION dispatch.reject_budget_audit()`)
	_, err = store.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, probeBudgetCloseRequest(before))
	require.NoError(t, err)
}

func TestDispatchProbeBudgetReportReadOnlyAndCoreIsolation(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeBudgetConfig(t, "core", 1000, 1000, 300, dispatchTarget("a", 1))
	other := probeBudgetConfig(t, "other-core", 1000, 1000, 300, dispatchTarget("b", 2))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), other))
	lease := probeBudgetClaim(t, store, cfg, "a")
	probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
	probeBudgetClaim(t, store, other, "b")
	role := "budget_reader_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := "synthetic-" + uuid.NewString()
	dispatchExec(t, db, `CREATE ROLE `+pq.QuoteIdentifier(role)+` LOGIN PASSWORD `+pq.QuoteLiteral(password))
	dispatchExec(t, db, `GRANT USAGE ON SCHEMA dispatch TO `+pq.QuoteIdentifier(role)+`;
	 GRANT SELECT ON ALL TABLES IN SCHEMA dispatch TO `+pq.QuoteIdentifier(role))
	dsn, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	dsn.User = url.UserPassword(role, password)
	restricted, err := sql.Open("post"+"gres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, restricted.Close())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, `DROP OWNED BY `+pq.QuoteIdentifier(role)+`; DROP ROLE `+pq.QuoteIdentifier(role))
		require.NoError(t, err)
	})
	reader := dispatch.NewStore(restricted)
	before := probeBudgetOperationalState(t, db)
	auditCount := dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money_audit`)
	for range 2 {
		report := probeBudgetReport(t, reader, cfg)
		require.EqualValues(t, 600, report.Global.ExposureMicrousd, "global totals include other Core reservations")
		require.EqualValues(t, 1, report.EntryCount)
		require.Len(t, report.Entries, 1)
		require.Equal(t, cfg.CoreInstance, report.Entries[0].CoreInstance)
		require.Equal(t, lease.Token, report.Entries[0].Token)
		require.Len(t, report.Providers, 1)
		require.EqualValues(t, 600, report.Providers[0].ExposureMicrousd, "Provider totals are global across Core instances")
	}
	entry := probeBudgetEntry(t, probeBudgetReport(t, reader, cfg), lease.Token)
	_, err = reader.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, probeBudgetCloseRequest(entry))
	require.Error(t, err, "a SELECT-only role cannot settle money")
	_, err = store.CloseProbeBudget(t.Context(), other.CoreInstance, other.ConfigRevision, probeBudgetCloseRequest(entry))
	require.ErrorIs(t, err, dispatch.ErrProbeBudgetConflict, "one Core cannot close another Core's reservation")
	_, err = reader.ProbeBudgetReport(t.Context(), cfg.CoreInstance, cfg.ConfigRevision+1)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	require.Equal(t, before, probeBudgetOperationalState(t, db))
	require.Equal(t, auditCount, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money_audit`))
	require.Equal(t, entry, probeBudgetEntry(t, probeBudgetReport(t, store, cfg), lease.Token))
}

func TestDispatchProbeBudgetReportTruncationDoesNotTruncateTotals(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeBudgetConfig(t, "core", 100000, 100000, 7, dispatchTarget("history", 1), dispatchTarget("new", 2))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	now := dispatchNow(t, db)
	probeBudgetSeed(t, db, cfg, cfg.Targets[0], now.Add(-time.Minute), "unknown", 1001)
	oldest := probeBudgetSeed(t, db, cfg, cfg.Targets[0], now.Add(-48*time.Hour), "unknown", 1)[0]
	probeBudgetSeed(t, db, cfg, cfg.Targets[0], now, "charged", 1)
	before := probeBudgetOperationalState(t, db)
	report := probeBudgetReport(t, store, cfg)
	require.EqualValues(t, 1003, report.EntryCount)
	require.Len(t, report.Entries, 1000)
	require.True(t, report.EntriesTruncated)
	require.False(t, report.ProvidersTruncated)
	require.Equal(t, oldest, report.Entries[0].Token, "oldest pending debt remains actionable before charged history")
	for _, entry := range report.Entries {
		require.Equal(t, "unknown", entry.State, "charged history cannot hide pending debt")
	}
	require.EqualValues(t, 7014, report.Global.UnknownMicrousd)
	require.EqualValues(t, 7, report.Global.ChargedMicrousd)
	require.EqualValues(t, 7021, report.Global.ExposureMicrousd)
	require.Len(t, report.Providers, 1)
	require.EqualValues(t, 7021, report.Providers[0].ExposureMicrousd)
	require.Equal(t, before, probeBudgetOperationalState(t, db))
	require.Equal(t, 1003, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money_audit`))
}

func TestDispatchProbeBudgetReportProviderTruncationKeepsGlobalExposure(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	target := dispatchTarget("priced", 1)
	target.ProviderRef = "zz-priced-provider"
	cfg := probeBudgetConfig(t, "core", 1000, 1000, 7, target)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	lease := probeBudgetClaim(t, store, cfg, target.Ref)
	probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
	// 合成保留的已停用目标，构造报告列表边界，不发起探测或流量。
	dispatchExec(t, db, `INSERT INTO dispatch.targets(core_instance,target_ref,metadata,health,generation)
	 SELECT core_instance,'retired-'||n,metadata||jsonb_build_object(
	 'ref','retired-'||n,'account_id',100+n,'credential_ref','retired-credential-'||n,
	 'provider_ref','archived-'||lpad(n::text,4,'0'),'enabled',false),health,1
	 FROM dispatch.targets CROSS JOIN generate_series(1,1000) AS n WHERE core_instance=$1 AND target_ref=$2`,
		cfg.CoreInstance, target.Ref)
	before := probeBudgetOperationalState(t, db)
	report := probeBudgetReport(t, store, cfg)
	require.Len(t, report.Providers, 1000)
	require.True(t, report.ProvidersTruncated)
	require.False(t, report.EntriesTruncated)
	for _, provider := range report.Providers {
		require.NotEqual(t, target.ProviderRef, provider.ProviderRef)
		require.Zero(t, provider.ExposureMicrousd)
	}
	require.EqualValues(t, 7, report.Global.UnknownMicrousd)
	require.EqualValues(t, 7, report.Global.ExposureMicrousd, "a Provider omitted from its list still contributes to global exposure")
	require.EqualValues(t, 1, report.EntryCount)
	require.Equal(t, before, probeBudgetOperationalState(t, db))
}

func TestDispatchProbeBudgetRequestLimitsRemainIndependent(t *testing.T) {
	for _, limit := range []string{"concurrent", "provider-rpm", "daily-requests"} {
		t.Run(limit, func(t *testing.T) {
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			cfg := probeBudgetConfig(t, "core", 1000, 1000, 100, dispatchTarget("a", 1), dispatchTarget("b", 2))
			switch limit {
			case "concurrent":
				cfg.Probe.GlobalConcurrent = 1
			case "provider-rpm":
				cfg.Probe.ProviderRPM = 1
			case "daily-requests":
				cfg.Probe.DailyRequests = 1
			}
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			lease := probeBudgetClaim(t, store, cfg, "a")
			probeBudgetDenied(t, store, cfg, "b")
			probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
			entry := probeBudgetEntry(t, probeBudgetReport(t, store, cfg), lease.Token)
			_, err := store.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, probeBudgetCloseRequest(entry))
			require.NoError(t, err)
			if limit == "concurrent" {
				probeBudgetClaim(t, dispatch.NewStore(probeBudgetConnection(t)), cfg, "b")
				require.EqualValues(t, 200, probeBudgetReport(t, store, cfg).Global.ExposureMicrousd)
			} else {
				probeBudgetDenied(t, dispatch.NewStore(probeBudgetConnection(t)), cfg, "b")
				require.EqualValues(t, 100, probeBudgetReport(t, store, cfg).Global.ExposureMicrousd)
				require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
			}
		})
	}
}

func TestDispatchProbeBudgetClosePreviousDayDebtUsesFrozenDay(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeBudgetConfig(t, "core", 500, 500, 500, dispatchTarget("history", 1), dispatchTarget("new", 2))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	token := probeBudgetSeed(t, db, cfg, cfg.Targets[0], dispatchNow(t, db).Add(-48*time.Hour), "unknown", 1)[0]
	entry := probeBudgetEntry(t, probeBudgetReport(t, store, cfg), token)
	probeBudgetDenied(t, store, cfg, "new")
	before := probeBudgetOperationalState(t, db)
	closed, err := store.CloseProbeBudget(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, probeBudgetCloseRequest(entry))
	require.NoError(t, err)
	require.Equal(t, entry.BudgetDay, closed.BudgetDay)
	require.EqualValues(t, 500, closed.MaxMicrousd)
	report := probeBudgetReport(t, store, cfg)
	require.Zero(t, report.Global.ExposureMicrousd, "settled historical charges leave today's cap without rewriting their budget day")
	require.Equal(t, before, probeBudgetOperationalState(t, db))
	probeBudgetClaim(t, store, cfg, "new")
}

func TestDispatchProbeBudgetActivationAllowsExpiredUnpricedHistory(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	target := dispatchTarget("legacy", 1)
	legacy := failureConfig(t, []dispatch.Target{target}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), legacy))
	lease, err := store.ClaimProbe(t.Context(), legacy.CoreInstance, target.Ref, "legacy-worker", legacy.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	require.Nil(t, lease.Budget)
	dispatchExec(t, db, `UPDATE dispatch.probes SET lease_until=clock_timestamp()-interval '1 microsecond' WHERE token=$1`, lease.Token)
	cfg := probeBudgetConfig(t, "core", 500, 500, 500, target, dispatchTarget("new", 2))
	cfg.ConfigRevision++
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	report := probeBudgetReport(t, store, cfg)
	require.True(t, report.Enabled)
	require.False(t, report.Fenced)
	require.Zero(t, report.EntryCount)
	require.Zero(t, report.Global.ExposureMicrousd)
	probeBudgetClaim(t, store, cfg, "new")
	require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE token=$1 AND finished_at IS NULL`, lease.Token))
}
