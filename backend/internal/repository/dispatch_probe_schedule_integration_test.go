//go:build integration

package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func probeScheduleConfig(t *testing.T, targets ...dispatch.Target) dispatch.Config {
	t.Helper()
	cfg := failureConfig(t, targets, true)
	cfg.HealthWindow = &dispatch.HealthWindowConfig{MinSamples: 20}
	cfg.ProbeSchedule = &dispatch.ProbeScheduleConfig{IdleIntervalSeconds: 30, BusyIntervalSeconds: 300, PassiveMinSamples: 20}
	return cfg
}

func probeSchedulePassive(t *testing.T, db *sql.DB, store *dispatch.Store, cfg dispatch.Config, target dispatch.Target, count int) {
	t.Helper()
	batch := make([]dispatch.PassiveEvent, count)
	for i := range batch {
		batch[i] = dispatch.PassiveEvent{Observation: dispatchObservation(t, db, target, uuid.NewString(), dispatch.Success),
			CoreInstance: cfg.CoreInstance, ConfigRevision: cfg.ConfigRevision, CredentialRef: target.CredentialRef,
			ProviderRef: target.ProviderRef, PoolRef: target.PoolRef, PolicyRevision: target.PolicyRevision, Model: target.Model, Region: target.Region}
	}
	inserted, err := store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
	require.NoError(t, err)
	require.Equal(t, count, inserted)
}

func probeScheduleFinish(t *testing.T, db *sql.DB, store *dispatch.Store, lease *dispatch.ProbeLease, outcome dispatch.Outcome) {
	t.Helper()
	o := dispatchObservation(t, db, lease.Target, lease.AttemptID, outcome)
	if outcome == dispatch.Unknown {
		o.ProviderEvidence = false
	}
	require.NoError(t, store.FinishProbe(t.Context(), *lease, o))
}

func probeScheduleAgeClaim(t *testing.T, db *sql.DB, lease *dispatch.ProbeLease, at time.Time) {
	t.Helper()
	dispatchExec(t, db, `UPDATE dispatch.probes SET reserved_at=$2,slot=floor(extract(epoch FROM $2::timestamptz)/30)::bigint,
	 lease_until=$2::timestamptz+interval '1 minute',finished_at=$2::timestamptz+interval '1 second' WHERE token=$1`, lease.Token, at)
}

func probeScheduleSaveHealth(t *testing.T, db *sql.DB, core, ref string, health dispatch.Health) {
	t.Helper()
	body, err := json.Marshal(health)
	require.NoError(t, err)
	dispatchExec(t, db, `UPDATE dispatch.targets SET health=$3::jsonb WHERE core_instance=$1 AND target_ref=$2`, core, ref, string(body))
}

func TestDispatchProbeSchedulePassiveAliasesAndExpiry(t *testing.T) {
	db := dispatchDB(t)
	a := dispatchTarget("a", 1)
	alias := a
	alias.Ref, alias.PoolRef = "alias", "alias-pool"
	other := dispatchTarget("other", 2)
	cfg := probeScheduleConfig(t, a, alias, other)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	lease, err := store.ClaimProbe(t.Context(), "core", a.Ref, "first", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	probeScheduleFinish(t, db, store, lease, dispatch.Success)
	probeScheduleAgeClaim(t, db, lease, dispatchNow(t, db).Add(-2*time.Minute))
	probeSchedulePassive(t, db, store, cfg, a, 20)
	require.Equal(t, 20, dispatchHealth(t, db, "core", a.Ref).RecentPassiveSamples(dispatchNow(t, db)))
	require.Zero(t, dispatchHealth(t, db, "core", alias.Ref).RecentPassiveSamples(dispatchNow(t, db)), "success evidence is not duplicated into aliases")
	secondDB, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, secondDB.Close()) })
	second := dispatch.NewStore(secondDB)
	for _, target := range []dispatch.Target{a, alias} {
		denied, err := second.ClaimProbe(t.Context(), "core", target.Ref, "other-process", cfg.ProbeLimits())
		require.NoError(t, err)
		require.Nil(t, denied, "both aliases share busy pacing, even if only one has successful passive evidence")
	}
	independent, err := second.ClaimProbe(t.Context(), "core", other.Ref, "other-domain", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, independent, "native domains do not share adaptive intervals")
	probeScheduleFinish(t, db, second, independent, dispatch.Success)
	// Move only the bucket facts into the past to exercise expiry without sleeps.
	health := dispatchHealth(t, db, "core", a.Ref)
	for i := range health.Rolling.Buckets {
		health.Rolling.Buckets[i].Start = health.Rolling.Buckets[i].Start.Add(-2 * time.Minute)
	}
	probeScheduleSaveHealth(t, db, "core", a.Ref, health)
	ready, err := second.ClaimProbe(t.Context(), "core", alias.Ref, "after-expiry", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, ready, "expired passive facts must restore idle pacing")
	probeScheduleFinish(t, db, second, ready, dispatch.Success)
	require.Equal(t, 3, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
}

func TestDispatchProbeScheduleProbeAndImportStayIdle(t *testing.T) {
	for _, origin := range []string{"probe", "import", "unknown", "unproven"} {
		t.Run(origin, func(t *testing.T) {
			db := dispatchDB(t)
			cfg := probeScheduleConfig(t, dispatchTarget("a", 1))
			store := dispatch.NewStore(db)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			var last *dispatch.ProbeLease
			for i := range 20 {
				if origin == "probe" {
					lease, err := store.ClaimProbe(t.Context(), "core", "a", "owner", cfg.ProbeLimits())
					require.NoError(t, err)
					require.NotNil(t, lease)
					probeScheduleFinish(t, db, store, lease, dispatch.Success)
					probeScheduleAgeClaim(t, db, lease, dispatchNow(t, db).Add(-time.Duration(40+i)*time.Minute))
					last = lease
				} else {
					o := dispatchObservation(t, db, cfg.Targets[0], fmt.Sprintf("%s-%d", origin, i), dispatch.Success)
					if origin == "unknown" {
						o.Outcome = dispatch.Unknown
					}
					if origin == "unproven" {
						o.ProviderEvidence = false
					}
					inserted, err := store.Observe(t.Context(), "core", o)
					require.NoError(t, err)
					require.True(t, inserted)
				}
			}
			if last == nil {
				var err error
				last, err = store.ClaimProbe(t.Context(), "core", "a", "seed", cfg.ProbeLimits())
				require.NoError(t, err)
				require.NotNil(t, last)
				probeScheduleFinish(t, db, store, last, dispatch.Success)
			}
			probeScheduleAgeClaim(t, db, last, dispatchNow(t, db).Add(-2*time.Minute))
			require.Zero(t, dispatchHealth(t, db, "core", "a").RecentPassiveSamples(dispatchNow(t, db)))
			ready, err := dispatch.NewStore(db).ClaimProbe(t.Context(), "core", "a", "idle", cfg.ProbeLimits())
			require.NoError(t, err)
			require.NotNil(t, ready, "active probes and imports cannot pretend the upstream has passive traffic")
		})
	}
}

func TestDispatchProbeScheduleAliasesSerializeAcrossConnections(t *testing.T) {
	db := dispatchDB(t)
	a := dispatchTarget("a", 1)
	alias := a
	alias.Ref = "alias"
	cfg := probeScheduleConfig(t, a, alias)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	secondDB, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, secondDB.Close()) })
	stores := []*dispatch.Store{store, dispatch.NewStore(secondDB)}
	var wg sync.WaitGroup
	results := make(chan *dispatch.ProbeLease, 2)
	errors := make(chan error, 2)
	for i, target := range cfg.Targets {
		wg.Go(func() {
			lease, err := stores[i].ClaimProbe(t.Context(), "core", target.Ref, "owner", cfg.ProbeLimits())
			results <- lease
			errors <- err
		})
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	claimed := 0
	for lease := range results {
		if lease != nil {
			claimed++
		}
	}
	require.Equal(t, 1, claimed)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
}

func TestDispatchProbeScheduleSharedBudgetsAndUnknown(t *testing.T) {
	for _, budget := range []string{"concurrent", "provider_rpm", "daily"} {
		t.Run(budget, func(t *testing.T) {
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			low := probeScheduleConfig(t, dispatchTarget("a", 1))
			switch budget {
			case "concurrent":
				low.Probe.GlobalConcurrent = 1
			case "provider_rpm":
				low.Probe.ProviderRPM = 1
			case "daily":
				low.Probe.DailyRequests = 1
			}
			high := probeScheduleConfig(t, dispatchTarget("b", 2), dispatchTarget("c", 3))
			high.CoreInstance = "other-core"
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), low))
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), high))
			lease, err := store.ClaimProbe(t.Context(), high.CoreInstance, "b", "first", high.ProbeLimits())
			require.NoError(t, err)
			require.NotNil(t, lease)
			if budget != "concurrent" {
				probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
			}
			for _, next := range []struct{ core, ref string }{{"core", "a"}, {"other-core", "c"}} {
				cfg := low
				if next.core == high.CoreInstance {
					cfg = high
				}
				denied, err := dispatch.NewStore(db).ClaimProbe(t.Context(), next.core, next.ref, "restart", cfg.ProbeLimits())
				require.NoError(t, err)
				require.Nil(t, denied, "adaptive pacing must retain the strictest persisted shared budget")
			}
			if budget == "concurrent" {
				probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
				ready, err := store.ClaimProbe(t.Context(), "core", "a", "freed", low.ProbeLimits())
				require.NoError(t, err)
				require.NotNil(t, ready, "finished Unknown releases concurrency, never request budgets")
			} else {
				require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE payload->>'outcome'='unknown'`))
				require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
			}
		})
	}
}

func TestDispatchProbeScheduleUTCDayRetainsUnknownHistory(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeScheduleConfig(t, dispatchTarget("a", 1), dispatchTarget("b", 2), dispatchTarget("c", 3))
	cfg.Probe.DailyRequests = 1
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	lease, err := store.ClaimProbe(t.Context(), "core", "a", "yesterday", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
	now := dispatchNow(t, db)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	probeScheduleAgeClaim(t, db, lease, midnight.Add(-2*time.Minute))
	today, err := dispatch.NewStore(db).ClaimProbe(t.Context(), "core", "b", "today", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, today)
	probeScheduleFinish(t, db, store, today, dispatch.Unknown)
	denied, err := dispatch.NewStore(db).ClaimProbe(t.Context(), "core", "c", "still-today", cfg.ProbeLimits())
	require.NoError(t, err)
	require.Nil(t, denied, "UTC rollover is not a refund of today's unknown attempts")
	require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
	require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE payload->>'outcome'='unknown'`))
}

func TestDispatchProbeScheduleConfigFenceAndRecoveryReset(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeScheduleConfig(t, dispatchTarget("a", 1))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	defaulted := cfg
	defaulted.ProbeSchedule = &dispatch.ProbeScheduleConfig{}
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), defaulted))
	legacy := cfg
	legacy.ProbeSchedule = nil
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), legacy), dispatch.ErrConfigRevision)
	changed := cfg
	changed.ProbeSchedule = &dispatch.ProbeScheduleConfig{IdleIntervalSeconds: 30, BusyIntervalSeconds: 600, PassiveMinSamples: 20}
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), changed), dispatch.ErrConfigRevision)
	lease, err := store.ClaimProbe(t.Context(), "core", "a", "first", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	probeScheduleFinish(t, db, store, lease, dispatch.Success)
	probeScheduleAgeClaim(t, db, lease, dispatchNow(t, db).Add(-time.Minute))
	probeSchedulePassive(t, db, store, cfg, cfg.Targets[0], 20)
	denied, err := dispatch.NewStore(db).ClaimProbe(t.Context(), "core", "a", "old-wrapper", legacy.ProbeLimits())
	require.NoError(t, err)
	require.Nil(t, denied, "ProbeLimits has no policy field, but cannot bypass persisted pacing")
	old := cfg
	cfg.ConfigRevision++
	cfg.Targets = append([]dispatch.Target(nil), cfg.Targets...)
	cfg.Targets[0].CredentialRevision++
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	_, err = store.ClaimProbe(t.Context(), "core", "a", "old-generation", old.ProbeLimits())
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	health := dispatchHealth(t, db, "core", "a")
	require.Equal(t, dispatch.Tripped, health.State)
	require.Zero(t, health.Backoff)
	require.Nil(t, health.Rolling)
	recovered, err := dispatch.NewStore(db).ClaimProbe(t.Context(), "core", "a", "rotated", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, recovered, "config/credential recovery is not delayed by the prior busy interval")
	require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
}

func TestDispatchProbeScheduleHalfOpenAndManualHolds(t *testing.T) {
	for _, independentAuth := range []bool{false, true} {
		t.Run(fmt.Sprintf("auth-%v", independentAuth), func(t *testing.T) {
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			a := dispatchTarget("a", 1)
			cfg := probeScheduleConfig(t, a)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			lease, err := store.ClaimProbe(t.Context(), "core", "a", "seed", cfg.ProbeLimits())
			require.NoError(t, err)
			require.NotNil(t, lease)
			probeScheduleFinish(t, db, store, lease, dispatch.Success)
			probeScheduleAgeClaim(t, db, lease, dispatchNow(t, db).Add(-time.Minute))
			probeSchedulePassive(t, db, store, cfg, a, 20)
			if independentAuth {
				_, err := store.Observe(t.Context(), "core", dispatchObservation(t, db, a, "auth", dispatch.UpstreamAuth))
				require.NoError(t, err)
			}
			_, err = store.Observe(t.Context(), "core", failureEvent(t, db, a, "capability", dispatch.FailureCapabilityDenied))
			require.NoError(t, err)
			denied, err := store.ClaimProbe(t.Context(), "core", "a", "held", cfg.ProbeLimits())
			require.NoError(t, err)
			require.Nil(t, denied)
			holds, err := store.HealthHolds(t.Context(), "core", 1)
			require.NoError(t, err)
			require.Len(t, holds, 1)
			_, err = store.ReleaseHealthHold(t.Context(), releaseRequest(holds[0], cfg, "release"))
			require.NoError(t, err)
			first, err := dispatch.NewStore(db).ClaimProbe(t.Context(), "core", "a", "released", cfg.ProbeLimits())
			require.NoError(t, err)
			if independentAuth {
				require.Nil(t, first)
				require.True(t, dispatchHealth(t, db, "core", "a").AuthBlocked)
				return
			}
			require.NotNil(t, first, "manual release ignores busy interval while preserving existing slot and budgets")
			probeScheduleFinish(t, db, store, first, dispatch.Success)
			health := dispatchHealth(t, db, "core", "a")
			require.Equal(t, dispatch.Tripped, health.State)
			require.Equal(t, 1, health.ProbeSuccesses)
			denied, err = store.ClaimProbe(t.Context(), "core", "a", "too-soon", cfg.ProbeLimits())
			require.NoError(t, err)
			require.Nil(t, denied, "release still requires a separate confirmation probe")
			probeScheduleAgeClaim(t, db, first, dispatchNow(t, db).Add(-30*time.Minute))
			health.CoolUntil = dispatchNow(t, db).Add(-time.Second)
			probeScheduleSaveHealth(t, db, "core", "a", health)
			second, err := dispatch.NewStore(db).ClaimProbe(t.Context(), "core", "a", "confirm", cfg.ProbeLimits())
			require.NoError(t, err)
			require.NotNil(t, second)
			probeScheduleFinish(t, db, store, second, dispatch.Success)
			health = dispatchHealth(t, db, "core", "a")
			require.Equal(t, dispatch.Degraded, health.State)
			require.Equal(t, 1, health.RecoveryStep)
		})
	}
}

func TestDispatchProbeScheduleDailyBudgetFairnessAcrossRestarts(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	targets := []dispatch.Target{dispatchTarget("a", 1), dispatchTarget("b", 2), dispatchTarget("c", 3), dispatchTarget("d", 4)}
	for i := range 9 {
		alias := targets[0]
		alias.Ref, alias.PoolRef = fmt.Sprintf("alias-%02d", i), fmt.Sprintf("pool-%02d", i)
		targets = append(targets, alias)
	}
	cfg := probeScheduleConfig(t, targets...)
	cfg.Probe.DailyRequests = 1
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	fixedTime := time.Date(2026, 11, 7, 6, 17, 0, 0, time.UTC)
	seen := map[int64]int{}
	for day := range 4 {
		if day > 0 {
			// Shift retained facts across a UTC day instead of changing the DB clock.
			dispatchExec(t, db, `UPDATE dispatch.probes SET reserved_at=reserved_at-interval '1 day',
			 lease_until=lease_until-interval '1 day',finished_at=finished_at-interval '1 day',slot=slot-2880`)
		}
		restartedDB, err := sql.Open("postgres", integrationDSN)
		require.NoError(t, err)
		restarted := dispatch.NewStore(restartedDB)
		claims := 0
		for _, target := range dispatch.ProbeOrder(cfg.Targets, cfg.CoreInstance, fixedTime.AddDate(0, 0, day)) {
			lease, err := restarted.ClaimProbe(t.Context(), cfg.CoreInstance, target.Ref, "daily-restart", cfg.ProbeLimits())
			require.NoError(t, err)
			if lease == nil {
				continue
			}
			claims++
			seen[lease.Target.AccountID]++
			probeScheduleFinish(t, db, restarted, lease, dispatch.Unknown)
		}
		require.NoError(t, restartedDB.Close())
		require.Equal(t, 1, claims, "daily request budget still applies across every alias and connection")
		require.Equal(t, day+1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
	}
	require.Len(t, seen, 4, "a daily budget of one must rotate every native domain, despite aliases and store restart")
	for _, claims := range seen {
		require.Equal(t, 1, claims)
	}
	require.Equal(t, 4, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE payload->>'outcome'='unknown'`))
}
