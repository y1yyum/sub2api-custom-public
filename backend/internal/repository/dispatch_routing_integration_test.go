//go:build integration

package repository

import (
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

func dispatchRoutingConfig(t *testing.T) dispatch.Config {
	t.Helper()
	return dispatch.Config{Version: 1, CoreInstance: "core", ConfigRevision: 1,
		SnapshotFile: filepath.Join(t.TempDir(), "snapshot.json"), SnapshotTTLSeconds: 30, PollIntervalMilliseconds: 1000,
		Targets:   []dispatch.Target{dispatchTarget("a", 1)},
		Telemetry: &dispatch.TelemetryConfig{SlotTTLSeconds: 1800, LoadMaxAgeSeconds: 5, MaxObservations: 100},
		Routing:   &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}}
}

func dispatchRoutingLoad(t *testing.T, db *sql.DB, active int) *dispatch.LoadSample {
	t.Helper()
	return &dispatch.LoadSample{ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{1: active}}
}

func dispatchRoutingState(t *testing.T, db *sql.DB) (dispatch.Health, []byte) {
	t.Helper()
	var body []byte
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT health FROM dispatch.targets WHERE core_instance='core' AND target_ref='a'`).Scan(&body))
	var health dispatch.Health
	require.NoError(t, json.Unmarshal(body, &health))
	return health, body
}

func TestDispatchRoutingConfigAndPublicationFence(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := dispatchRoutingConfig(t)
	legacy := cfg
	legacy.Routing = nil
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), legacy))
	o := dispatchObservation(t, db, cfg.Targets[0], "legacy", dispatch.Success)
	o.LatencyMillis = 4000
	_, err := store.Observe(t.Context(), cfg.CoreInstance, o)
	require.NoError(t, err)
	snapshot, err := store.PublishRuntime(t.Context(), legacy, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	require.Equal(t, int64(25), snapshot.Candidates[0].EffectiveWeight)
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), cfg), dispatch.ErrConfigRevision)
	cfg.ConfigRevision++
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	defaulted := cfg
	defaulted.Routing = &dispatch.RoutingConfig{}
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), defaulted), "equivalent normalized policies must have the same digest")
	snapshot, err = store.PublishRuntime(t.Context(), defaulted, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	require.Equal(t, int64(100), snapshot.Candidates[0].EffectiveWeight, "routing must not use legacy full latency")
	_, err = store.PublishRuntime(t.Context(), legacy, dispatchRoutingLoad(t, db, 0))
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), legacy), dispatch.ErrConfigRevision)
	for name, change := range map[string]func(*dispatch.Config){
		"nil_policy": func(c *dispatch.Config) { c.Routing = nil },
		"different_policy": func(c *dispatch.Config) {
			c.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 2000, WeightSmoothingSeconds: 10}
		},
		"different_target": func(c *dispatch.Config) {
			c.Targets = append([]dispatch.Target(nil), c.Targets...)
			c.Targets[0].BaseWeight++
		},
		"different_telemetry": func(c *dispatch.Config) {
			c.Telemetry = &dispatch.TelemetryConfig{SlotTTLSeconds: 1800, LoadMaxAgeSeconds: 6, MaxObservations: 100}
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := cfg
			change(&c)
			require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), c), dispatch.ErrConfigRevision)
			_, err := store.PublishRuntime(t.Context(), c, dispatchRoutingLoad(t, db, 0))
			require.ErrorIs(t, err, dispatch.ErrConfigRevision)
		})
	}
	_, err = store.Publish(t.Context(), "core", cfg.ConfigRevision, 30*time.Second)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	_, err = store.PublishTo(t.Context(), "core", cfg.ConfigRevision, 30*time.Second, cfg.SnapshotFile)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	_, err = store.PublishWithLoad(t.Context(), "core", cfg.ConfigRevision, 30*time.Second, cfg.SnapshotFile, *dispatchRoutingLoad(t, db, 0), 5*time.Second)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	_, err = store.PublishRuntime(t.Context(), cfg, nil)
	require.ErrorContains(t, err, "live load sample")
	lease, err := store.ClaimProbe(t.Context(), "core", "a", "owner", cfg.ProbeLimits())
	require.NoError(t, err, "persisted routing metadata must remain compatible with probe limits")
	require.Nil(t, lease)
}

func TestDispatchRoutingLoadExpiryAndFailedPublicationPreserveState(t *testing.T) {
	db := dispatchDB(t)
	cfg := dispatchRoutingConfig(t)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	initial, err := store.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	require.Equal(t, initial.LoadObservedAt.Add(5*time.Second), initial.ExpiresAt)
	_, state := dispatchRoutingState(t, db)
	body, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	for name, sample := range map[string]*dispatch.LoadSample{
		"missing":    nil,
		"expired":    {ObservedAt: dispatchNow(t, db).Add(-6 * time.Second), Accounts: map[int64]int{1: 0}},
		"incomplete": {ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{}},
		"negative":   {ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{1: -1}},
		"extra":      {ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{1: 0, 2: 0}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := store.PublishRuntime(t.Context(), cfg, sample)
			require.Error(t, err)
			_, after := dispatchRoutingState(t, db)
			require.JSONEq(t, string(state), string(after))
			got, err := os.ReadFile(cfg.SnapshotFile)
			require.NoError(t, err)
			require.Equal(t, body, got)
		})
	}
	brokenPath := cfg
	brokenPath.SnapshotFile = filepath.Join(t.TempDir(), "missing", "snapshot.json")
	_, err = store.PublishRuntime(t.Context(), brokenPath, dispatchRoutingLoad(t, db, 5))
	require.Error(t, err)
	_, after := dispatchRoutingState(t, db)
	require.JSONEq(t, string(state), string(after), "a failed file publication must roll back smoothing")
	var revision int64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT revision FROM dispatch.cores WHERE core_instance='core'`).Scan(&revision))
	require.Equal(t, initial.Revision, revision)
}

func TestDispatchRoutingTTFTFreshnessAndGenerationReset(t *testing.T) {
	db := dispatchDB(t)
	cfg := dispatchRoutingConfig(t)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	o := dispatchObservation(t, db, cfg.Targets[0], "timing", dispatch.Success)
	o.LatencyMillis, o.TTFTMillis = 8000, 2000
	before := dispatchNow(t, db)
	_, err := store.Observe(t.Context(), "core", o)
	require.NoError(t, err)
	h := dispatchHealth(t, db, "core", "a")
	require.Equal(t, int64(2000), h.TTFTMillis)
	require.False(t, h.TTFTObservedAt.Before(before))
	require.False(t, h.TTFTObservedAt.After(dispatchNow(t, db)))
	snapshot, err := store.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	require.Equal(t, int64(50), snapshot.Candidates[0].EffectiveWeight)
	// 时间由数据库夹具推进，不等待真实五分钟。
	dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health-'routing','{ttft_observed_at}',to_jsonb(clock_timestamp()-interval '6 minutes'))`)
	snapshot, err = store.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	require.Equal(t, int64(100), snapshot.Candidates[0].EffectiveWeight, "expired TTFT is neutral")
	stale := dispatchHealth(t, db, "core", "a")
	o = dispatchObservation(t, db, cfg.Targets[0], "no-text", dispatch.Success)
	o.LatencyMillis = 8000
	_, err = store.Observe(t.Context(), "core", o)
	require.NoError(t, err)
	require.Equal(t, stale.TTFTObservedAt, dispatchHealth(t, db, "core", "a").TTFTObservedAt)
	o = dispatchObservation(t, db, cfg.Targets[0], "fresh-after-gap", dispatch.Success)
	o.LatencyMillis, o.TTFTMillis = 8000, 1000
	_, err = store.Observe(t.Context(), "core", o)
	require.NoError(t, err)
	require.Equal(t, int64(1000), dispatchHealth(t, db, "core", "a").TTFTMillis, "a new sample must not revive expired timing")
	prior := dispatchHealth(t, db, "core", "a")
	cfg.ConfigRevision++
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	after := dispatchHealth(t, db, "core", "a")
	require.Zero(t, after.TTFTMillis)
	require.True(t, after.TTFTObservedAt.IsZero())
	require.Nil(t, after.Routing)
	require.Equal(t, prior.UpdatedAt, after.UpdatedAt)
	require.Equal(t, prior.WindowAt, after.WindowAt)
	require.Equal(t, prior.LatencyMillis, after.LatencyMillis)
	snapshot, err = store.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	require.Equal(t, int64(100), snapshot.Candidates[0].EffectiveWeight)
	_, err = store.Observe(t.Context(), "core", dispatch.Observation{EventID: "bad-timing", TargetRef: "a", AccountID: 1, CredentialRevision: 1,
		Outcome: dispatch.Success, At: dispatchNow(t, db), ProviderEvidence: true, LatencyMillis: 1000, TTFTMillis: 1001})
	require.Error(t, err)
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE event_id='bad-timing'`))
}

func TestDispatchRoutingDurableSmoothingAndConcurrentPublish(t *testing.T) {
	db := dispatchDB(t)
	cfg := dispatchRoutingConfig(t)
	cfg.Targets[0].BaseWeight = 10000
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	_, err := store.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	initial := dispatchHealth(t, db, "core", "a")
	require.NotNil(t, initial.Routing)
	require.Equal(t, 1.0, initial.Routing.Factor)
	dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health,'{routing,updated_at}',to_jsonb(clock_timestamp()-interval '10 seconds'))`)
	seed := dispatchHealth(t, db, "core", "a")
	otherDB, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, otherDB.Close()) })
	restarted := dispatch.NewStore(otherDB)
	load := dispatchRoutingLoad(t, db, 5)
	var wg sync.WaitGroup
	snapshots := make(chan dispatch.Snapshot, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		publisher := store
		if i%2 == 1 {
			publisher = restarted
		}
		wg.Go(func() {
			snapshot, err := publisher.PublishRuntime(t.Context(), cfg, load)
			errs <- err
			snapshots <- snapshot
		})
	}
	wg.Wait()
	close(errs)
	close(snapshots)
	for err := range errs {
		require.NoError(t, err)
	}
	var latest dispatch.Snapshot
	seen := map[int64]bool{}
	for snapshot := range snapshots {
		require.False(t, seen[snapshot.Revision])
		seen[snapshot.Revision] = true
		expected := 0.5 + 0.5*math.Exp(-snapshot.GeneratedAt.Sub(seed.Routing.UpdatedAt).Seconds()/10)
		require.InDelta(t, int64(10000*expected), snapshot.Candidates[0].EffectiveWeight, 1)
		if snapshot.Revision > latest.Revision {
			latest = snapshot
		}
	}
	after := dispatchHealth(t, db, "core", "a")
	expected := 0.5 + 0.5*math.Exp(-latest.GeneratedAt.Sub(seed.Routing.UpdatedAt).Seconds()/10)
	require.InDelta(t, expected, after.Routing.Factor, 1e-12)
	require.Equal(t, initial.UpdatedAt, after.UpdatedAt)
	require.Equal(t, initial.WindowAt, after.WindowAt)
	require.Equal(t, initial.Samples, after.Samples)
	visible, err := dispatch.LoadSnapshot(cfg.SnapshotFile, dispatchNow(t, db))
	require.NoError(t, err)
	require.Equal(t, latest.Revision, visible.Revision)
	snapshot, err := restarted.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, cfg.Targets[0].MaxInflight))
	require.NoError(t, err)
	require.Zero(t, snapshot.Candidates[0].EffectiveWeight)
	require.Zero(t, dispatchHealth(t, db, "core", "a").Routing.Factor)
}

func TestDispatchRoutingHardVetoRecoveryAndQualityComposition(t *testing.T) {
	db := dispatchDB(t)
	require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
	cfg, _ := qualityStoreConfig(t, 1)
	cfg.Telemetry = dispatchRoutingConfig(t).Telemetry
	cfg.Routing = dispatchRoutingConfig(t).Routing
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	_, err := store.IngestQuality(t.Context(), cfg, qualityStoreSource(dispatchNow(t, db)))
	require.NoError(t, err)
	snapshot, err := store.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 5))
	require.NoError(t, err)
	require.Equal(t, int64(43), snapshot.Candidates[0].EffectiveWeight, "quality applies after the routing load factor")
	dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(jsonb_set(health,'{state}','"degraded"'),'{recovery_step}','1')`)
	snapshot, err = store.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 5))
	require.NoError(t, err)
	require.LessOrEqual(t, snapshot.Candidates[0].EffectiveWeight, int64(5), "smoothing cannot override the immediate recovery cap")
	o := dispatchObservation(t, db, cfg.Targets[0], "block", dispatch.UpstreamAuth)
	_, err = store.Observe(t.Context(), "core", o)
	require.NoError(t, err)
	snapshot, err = store.PublishRuntime(t.Context(), cfg, dispatchRoutingLoad(t, db, 0))
	require.NoError(t, err)
	for _, candidate := range snapshot.Candidates {
		require.Zero(t, candidate.EffectiveWeight)
	}
}
