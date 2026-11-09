//go:build integration

package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

func healthWindowConfig(t *testing.T) dispatch.Config {
	t.Helper()
	cfg := dispatchRoutingConfig(t)
	cfg.HealthWindow = &dispatch.HealthWindowConfig{MinSamples: 20}
	return cfg
}

func healthWindowEvent(t *testing.T, db *sql.DB, cfg dispatch.Config, id string, outcome dispatch.Outcome) dispatch.PassiveEvent {
	t.Helper()
	a := cfg.Targets[0]
	return dispatch.PassiveEvent{Observation: dispatchObservation(t, db, a, id, outcome),
		CoreInstance: cfg.CoreInstance, ConfigRevision: cfg.ConfigRevision, CredentialRef: a.CredentialRef,
		ProviderRef: a.ProviderRef, PoolRef: a.PoolRef, PolicyRevision: a.PolicyRevision, Model: a.Model, Region: a.Region}
}

func saveHealthWindowFixture(t *testing.T, db *sql.DB, h dispatch.Health) {
	t.Helper()
	body, err := json.Marshal(h)
	require.NoError(t, err)
	dispatchExec(t, db, `UPDATE dispatch.targets SET health=$1::jsonb WHERE core_instance='core' AND target_ref='a'`, string(body))
}

func TestDispatchHealthWindowDurableFactsDedupAndWeight(t *testing.T) {
	db := dispatchDB(t)
	cfg := healthWindowConfig(t)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	before := dispatchNow(t, db)
	events := make([]dispatch.PassiveEvent, 20)
	for i := range events {
		e := healthWindowEvent(t, db, cfg, fmt.Sprintf("window-%d", i), dispatch.Success)
		e.At = before.Add(-4 * time.Minute)
		if i%2 == 0 && i < 8 {
			e.Outcome, e.TimedOut = dispatch.UpstreamFailure, i < 4
		}
		events[i] = e
	}
	count, err := store.ObservePassive(t.Context(), "core", 1, events)
	require.NoError(t, err)
	require.Equal(t, 20, count)
	h := dispatchHealth(t, db, "core", "a")
	report := h.HealthWindowReport(dispatchNow(t, db), *cfg.HealthWindow, 1000)
	require.Equal(t, 20, report.Hot.Samples)
	require.Equal(t, 4, report.Hot.Failures)
	require.Equal(t, 2, report.Hot.Timeouts)
	require.Equal(t, 20, report.Hot.PassiveSamples)
	require.Equal(t, int64(865), report.Hot.Factor)
	require.False(t, h.Rolling.ActivatedAt.Before(before), "window uses database confirmation time rather than source time")
	for _, bucket := range h.Rolling.Buckets {
		require.False(t, bucket.Start.Before(before.Truncate(10*time.Second)))
	}
	otherDB, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, otherDB.Close()) })
	restarted := dispatch.NewStore(otherDB)
	count, err = restarted.ObservePassive(t.Context(), "core", 1, events)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, report.Hot, dispatchHealth(t, otherDB, "core", "a").HealthWindowReport(dispatchNow(t, db), *cfg.HealthWindow, 1000).Hot)
	snapshot, err := restarted.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 5))
	require.NoError(t, err)
	require.Equal(t, int64(43), snapshot.Candidates[0].EffectiveWeight, "window factor multiplies the live-load route exactly once")
	changed := append([]dispatch.PassiveEvent(nil), events...)
	changed[0].TimedOut = false
	_, err = restarted.ObservePassive(t.Context(), "core", 1, changed)
	require.ErrorIs(t, err, dispatch.ErrObservationConflict)
	h = dispatchHealth(t, db, "core", "a")
	require.Equal(t, report.Hot, h.HealthWindowReport(dispatchNow(t, db), *cfg.HealthWindow, 1000).Hot)
	o := dispatchObservation(t, db, cfg.Targets[0], "window-import", dispatch.Success)
	inserted, err := restarted.Observe(t.Context(), "core", o)
	require.NoError(t, err)
	require.True(t, inserted)
	h = dispatchHealth(t, db, "core", "a")
	report = h.HealthWindowReport(dispatchNow(t, db), *cfg.HealthWindow, 1000)
	require.Equal(t, 21, report.Hot.Samples)
	require.Equal(t, 20, report.Hot.PassiveSamples, "import cannot inflate observed passive traffic")
	invalid := healthWindowEvent(t, db, cfg, "invalid-timeout", dispatch.Unknown)
	invalid.ProviderEvidence, invalid.TimedOut = false, true
	_, err = restarted.ObservePassive(t.Context(), "core", 1, []dispatch.PassiveEvent{invalid})
	require.Error(t, err)
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE event_id='invalid-timeout'`))
	require.Equal(t, report.Hot, dispatchHealth(t, db, "core", "a").HealthWindowReport(dispatchNow(t, db), *cfg.HealthWindow, 1000).Hot)
}

func TestDispatchHealthWindowRevisionFenceAndRollback(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := healthWindowConfig(t)
	legacy := cfg
	legacy.HealthWindow = nil
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), legacy))
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), cfg), dispatch.ErrConfigRevision)
	cfg.ConfigRevision++
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	defaulted := cfg
	defaulted.HealthWindow = &dispatch.HealthWindowConfig{}
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), defaulted), "normalization produces the same policy digest")
	event := healthWindowEvent(t, db, cfg, "revision-fact", dispatch.Success)
	count, err := store.ObservePassive(t.Context(), "core", cfg.ConfigRevision, []dispatch.PassiveEvent{event})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	previous := dispatchHealth(t, db, "core", "a")
	require.NotNil(t, previous.Rolling)
	changed := cfg
	changed.HealthWindow = &dispatch.HealthWindowConfig{MinSamples: 40}
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), changed), dispatch.ErrConfigRevision)
	_, err = store.PublishRuntime(t.Context(), changed, dispatchRoutingLoad(t, db, 0))
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	missing := cfg
	missing.HealthWindow = nil
	_, err = store.PublishRuntime(t.Context(), missing, dispatchRoutingLoad(t, db, 0))
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	_, err = store.PublishWithLoad(t.Context(), "core", cfg.ConfigRevision, 30*time.Second, cfg.SnapshotFile, *dispatchRoutingLoad(t, db, 0), 5*time.Second)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	changed.ConfigRevision++
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), changed))
	current := dispatchHealth(t, db, "core", "a")
	require.Nil(t, current.Rolling, "a new policy revision starts a fresh complete-window horizon")
	require.Equal(t, previous.State, current.State)
	_, err = store.ObservePassive(t.Context(), "core", cfg.ConfigRevision, []dispatch.PassiveEvent{event})
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	missing.ConfigRevision = changed.ConfigRevision + 1
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), missing))
	_, err = store.PublishRuntime(t.Context(), missing, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	require.Nil(t, dispatchHealth(t, db, "core", "a").Rolling)
}

func TestDispatchHealthWindowPublicationIsTransactional(t *testing.T) {
	db := dispatchDB(t)
	cfg := healthWindowConfig(t)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	now := dispatchNow(t, db)
	start := now.Truncate(time.Minute).Add(-2 * time.Minute)
	h := dispatch.InitialHealth(cfg.Targets[0], start)
	for window := range 2 {
		for i := range 20 {
			at := start.Add(time.Duration(window)*time.Minute + time.Duration(i)*time.Second)
			o := dispatch.Observation{EventID: "window", TargetRef: "a", AccountID: 1, CredentialRevision: 1,
				Outcome: dispatch.Success, ProviderEvidence: true, At: at, TTFTMillis: 2000, LatencyMillis: 4000}
			if i%2 == 0 && i < 16 {
				o.Outcome, o.TimedOut, o.TTFTMillis = dispatch.UpstreamFailure, true, 0
			}
			require.NoError(t, h.ApplyWithWindow(cfg.Targets[0], o, at, false, true, cfg.HealthWindow, 1000))
		}
	}
	require.Equal(t, dispatch.Healthy, h.State)
	require.Equal(t, 1, h.Rolling.BadWindows)
	saveHealthWindowFixture(t, db, h)
	_, before := dispatchRoutingState(t, db)
	broken := cfg
	broken.SnapshotFile = filepath.Join(t.TempDir(), "missing", "snapshot.json")
	_, err := store.PublishRuntime(t.Context(), broken, dispatchRoutingLoad(t, db, 0))
	require.Error(t, err)
	_, after := dispatchRoutingState(t, db)
	require.JSONEq(t, string(before), string(after), "failed publication rolls back full-window advancement")
	_, err = os.Stat(broken.SnapshotFile)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = store.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	published := dispatchHealth(t, db, "core", "a")
	require.Equal(t, dispatch.Degraded, published.State)
	require.Equal(t, 2, published.Rolling.BadWindows)
	completed := published.Rolling.CompletedThrough
	for range 3 {
		_, err = dispatch.NewStore(db).PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 0))
		require.NoError(t, err)
	}
	repeated := dispatchHealth(t, db, "core", "a")
	require.Equal(t, completed, repeated.Rolling.CompletedThrough)
	require.Equal(t, published.Rolling.BadWindows, repeated.Rolling.BadWindows)
	require.Equal(t, published.State, repeated.State)
}

func TestDispatchHealthWindowClockRollbackKeepsConfirmedFacts(t *testing.T) {
	db := dispatchDB(t)
	cfg := healthWindowConfig(t)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	now := dispatchNow(t, db)
	future := now.Add(time.Minute)
	h := dispatch.InitialHealth(cfg.Targets[0], future)
	h.AdvanceHealthWindow(future, cfg.HealthWindow, 1000)
	h.State, h.RecoveryStep = dispatch.Degraded, 1
	h.Rolling.GoodWindows = 1
	saveHealthWindowFixture(t, db, h)
	e := healthWindowEvent(t, db, cfg, "clock-rollback", dispatch.Success)
	count, err := store.ObservePassive(t.Context(), "core", 1, []dispatch.PassiveEvent{e})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	after := dispatchHealth(t, db, "core", "a")
	require.Equal(t, future, after.UpdatedAt)
	require.Equal(t, future, after.Rolling.AdvancedAt)
	report := after.HealthWindowReport(dispatchNow(t, db), *cfg.HealthWindow, 1000)
	require.True(t, report.ClockBehind)
	require.Equal(t, 1, report.Baseline.Samples)
	require.Equal(t, 1, report.Baseline.PassiveSamples)
	require.Equal(t, dispatch.Degraded, after.State)
	require.Equal(t, 1, after.RecoveryStep)
}

func TestDispatchHealthWindowPolicyActivationKeepsNativeClockFence(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeScheduleConfig(t, dispatchTarget("a", 1))
	cfg.HealthWindow, cfg.ProbeSchedule = nil, nil
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	now := dispatchNow(t, db)
	h := dispatchHealth(t, db, cfg.CoreInstance, "a")
	h.State, h.Reason, h.ProbeSuccesses = dispatch.Probing, "probe_inflight", 1
	h.UpdatedAt, h.CoolUntil = now.Add(10*time.Minute), now.Add(-time.Second)
	saveHealthWindowFixture(t, db, h)
	cfg.ConfigRevision++
	cfg.HealthWindow = &dispatch.HealthWindowConfig{MinSamples: 20}
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	before := dispatchHealth(t, db, cfg.CoreInstance, "a")
	require.Equal(t, dispatch.Tripped, before.State)
	require.Nil(t, before.Rolling)
	require.Equal(t, h.UpdatedAt, before.UpdatedAt)
	lease, err := store.ClaimProbe(t.Context(), cfg.CoreInstance, "a", "clock-fence-owner", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, lease)
	o := dispatchObservation(t, db, cfg.Targets[0], lease.AttemptID, dispatch.Success)
	require.NoError(t, store.FinishProbe(t.Context(), *lease, o))
	after := dispatchHealth(t, db, cfg.CoreInstance, "a")
	require.Equal(t, dispatch.Probing, after.State)
	require.Equal(t, 1, after.ProbeSuccesses)
	require.Equal(t, h.UpdatedAt, after.UpdatedAt)
	require.True(t, after.HealthWindowReport(dispatchNow(t, db), *cfg.HealthWindow, cfg.Routing.TTFTTargetMillis).ClockBehind)
}
