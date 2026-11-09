//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestDispatchRoutingSourceTimingAndLegacyPayload(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "legacy"
		if enabled {
			name = "routing"
		}
		t.Run(name, func(t *testing.T) {
			db := dispatchDB(t)
			cfg, producer := passiveConfig(t, db)
			if enabled {
				cfg.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}
			}
			event := insertPassiveEvent(t, db, producer, cfg, dispatch.Success)
			dispatchExec(t, db, `UPDATE bridge.dispatch_observation
			 SET latency_millis=8000,ttft_millis=2000 WHERE event_id=$1`, event.EventID)
			reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), cfg.CoreInstance, cfg.ConfigRevision)
			require.NoError(t, err)
			batch, err := reader.Read(t.Context(), cfg)
			require.NoError(t, err)
			require.Len(t, batch, 1)
			require.Equal(t, int64(8000), batch[0].LatencyMillis)
			if enabled {
				require.Equal(t, int64(2000), batch[0].TTFTMillis)
			} else {
				require.Zero(t, batch[0].TTFTMillis)
				body, err := json.Marshal(batch[0].Observation)
				require.NoError(t, err)
				require.NotContains(t, string(body), "ttft_millis", "legacy observation dedup payload must remain byte-compatible")
			}
			store := dispatch.NewStore(db)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			count, err := store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			before := dispatchHealth(t, db, cfg.CoreInstance, "a")
			require.Equal(t, batch[0].TTFTMillis, before.TTFTMillis)
			count, err = dispatch.NewStore(db).ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
			require.NoError(t, err)
			require.Zero(t, count)
			require.Equal(t, before, dispatchHealth(t, db, cfg.CoreInstance, "a"))
			load := dispatch.LoadSample{ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{1: 5}}
			snapshot, err := store.PublishRuntime(t.Context(), cfg, &load)
			require.NoError(t, err)
			require.Len(t, snapshot.Candidates, 2)
			require.Equal(t, int64(25), snapshot.Candidates[0].EffectiveWeight)
			if enabled {
				require.Equal(t, int64(50), snapshot.Candidates[1].EffectiveWeight, "one target's successful timing does not attribute a different route's output")
			} else {
				require.Equal(t, int64(100), snapshot.Candidates[1].EffectiveWeight)
			}
		})
	}
}

func TestDispatchRoutingSourceWithoutTimingStaysNeutral(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	cfg.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}
	event := insertPassiveEvent(t, db, producer, cfg, dispatch.Success)
	dispatchExec(t, db, `UPDATE bridge.dispatch_observation SET latency_millis=8000 WHERE event_id=$1`, event.EventID)
	dispatchExec(t, db, `ALTER TABLE bridge.dispatch_observation RENAME COLUMN ttft_millis TO legacy_hidden_timing`)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, `ALTER TABLE bridge.dispatch_observation RENAME COLUMN legacy_hidden_timing TO ttft_millis`)
		require.NoError(t, err)
	})
	reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), cfg.CoreInstance, cfg.ConfigRevision)
	require.NoError(t, err)
	batch, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	require.Zero(t, batch[0].TTFTMillis)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	_, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
	require.NoError(t, err)
	snapshot, err := store.PublishRuntime(t.Context(), cfg, &dispatch.LoadSample{
		ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{1: 0},
	})
	require.NoError(t, err)
	require.Equal(t, int64(100), snapshot.Candidates[0].EffectiveWeight, "full response duration cannot stand in for unavailable first-text timing")
}

func TestDispatchRoutingSourceRejectsTimingConflict(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	cfg.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}
	event := insertPassiveEvent(t, db, producer, cfg, dispatch.Success)
	dispatchExec(t, db, `UPDATE bridge.dispatch_observation
	 SET latency_millis=8000,ttft_millis=2000 WHERE event_id=$1`, event.EventID)
	reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), cfg.CoreInstance, cfg.ConfigRevision)
	require.NoError(t, err)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	batch, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	_, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
	require.NoError(t, err)
	before := dispatchHealth(t, db, cfg.CoreInstance, "a")
	dispatchExec(t, db, `UPDATE bridge.dispatch_observation SET ttft_millis=3000 WHERE event_id=$1`, event.EventID)
	batch, err = reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	_, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
	require.ErrorIs(t, err, dispatch.ErrObservationConflict)
	require.Equal(t, before, dispatchHealth(t, db, cfg.CoreInstance, "a"))
}

func TestDispatchRoutingSourceFailureDoesNotRenewSnapshot(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	cfg.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}
	event := insertPassiveEvent(t, db, producer, cfg, dispatch.Success)
	dispatchExec(t, db, `UPDATE bridge.dispatch_observation
	 SET latency_millis=8000,ttft_millis=2000 WHERE event_id=$1`, event.EventID)
	readerDB := passiveReadOnlyDB(t, db)
	reader, err := dispatch.NewPassiveReader(t.Context(), readerDB, cfg.CoreInstance, cfg.ConfigRevision)
	require.NoError(t, err)
	rdb, _ := dispatchLoadRedis(t)
	load, err := dispatch.NewRedisLoadReader(rdb, cfg.Telemetry.SlotTTLSeconds)
	require.NoError(t, err)
	sources := &dispatch.RuntimeSources{Passive: reader, Load: load}
	store := dispatch.NewStore(db)
	_, err = dispatch.RunRound(t.Context(), cfg, store, nil, sources)
	require.NoError(t, err)
	before, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	var role string
	require.NoError(t, readerDB.QueryRowContext(t.Context(), `SELECT current_user`).Scan(&role))
	dispatchExec(t, db, `REVOKE SELECT ON bridge.dispatch_observation FROM `+pq.QuoteIdentifier(role))
	_, err = dispatch.RunRound(t.Context(), cfg, store, nil, sources)
	require.Error(t, err)
	after, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
