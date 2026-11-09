//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/stretchr/testify/require"
)

func restoreDispatchFailureColumn(t *testing.T, db *sql.DB) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, `ALTER TABLE bridge.dispatch_observation DROP COLUMN IF EXISTS failure_class`)
		require.NoError(t, err)
		require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, db))
	})
}

func TestDispatchFailureSourceConsumesTrustedClassification(t *testing.T) {
	for _, tc := range []struct {
		class   string
		outcome dispatch.Outcome
	}{
		{"quota_exhausted", dispatch.UpstreamRateLimit},
		{"credential_banned", dispatch.UpstreamAuth},
		{"capability_denied", dispatch.UpstreamAuth},
	} {
		t.Run(tc.class, func(t *testing.T) {
			db := dispatchDB(t)
			cfg, producer := passiveConfig(t, db)
			event := insertPassiveEvent(t, db, producer, cfg, tc.outcome)
			dispatchExec(t, db, `UPDATE bridge.dispatch_observation SET failure_class=$2 WHERE event_id=$1`, event.EventID, tc.class)
			reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), cfg.CoreInstance, cfg.ConfigRevision)
			require.NoError(t, err)
			batch, err := reader.Read(t.Context(), cfg)
			require.NoError(t, err)
			require.Len(t, batch, 1)
			require.Equal(t, tc.class, batch[0].FailureClass)
			store := dispatch.NewStore(db)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			count, err := store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			snapshot, err := store.PublishRuntime(t.Context(), cfg, &dispatch.LoadSample{ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{1: 0}})
			require.NoError(t, err)
			for _, candidate := range snapshot.Candidates {
				require.Zero(t, candidate.EffectiveWeight)
				require.Equal(t, tc.class, candidate.Reason)
			}
			before := dispatchHealth(t, db, cfg.CoreInstance, "a")
			count, err = dispatch.NewStore(db).ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
			require.NoError(t, err)
			require.Zero(t, count)
			require.Equal(t, before, dispatchHealth(t, db, cfg.CoreInstance, "a"))
		})
	}
}

func TestDispatchFailureSourceLegacySchemaPreservesPayload(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	insertPassiveEvent(t, db, producer, cfg, dispatch.Success)
	reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), cfg.CoreInstance, cfg.ConfigRevision)
	require.NoError(t, err)
	before, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, before, 1)
	beforeJSON, err := json.Marshal(before[0].Observation)
	require.NoError(t, err)
	require.NotContains(t, string(beforeJSON), "failure_class")
	restoreDispatchFailureColumn(t, db)
	dispatchExec(t, db, `ALTER TABLE bridge.dispatch_observation DROP COLUMN failure_class`)
	after, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	require.Equal(t, before, after)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	count, err := store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, before)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, after)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestDispatchFailureSourceConflictingClassDoesNotRenew(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	event := insertPassiveEvent(t, db, producer, cfg, dispatch.UpstreamAuth)
	dispatchExec(t, db, `UPDATE bridge.dispatch_observation SET failure_class='credential_banned' WHERE event_id=$1`, event.EventID)
	reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), cfg.CoreInstance, cfg.ConfigRevision)
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
	health := dispatchHealth(t, db, cfg.CoreInstance, "a")
	dispatchExec(t, db, `UPDATE bridge.dispatch_observation SET failure_class='capability_denied' WHERE event_id=$1`, event.EventID)
	_, err = dispatch.RunRound(t.Context(), cfg, store, nil, sources)
	require.ErrorIs(t, err, dispatch.ErrObservationConflict)
	after, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, health, dispatchHealth(t, db, cfg.CoreInstance, "a"))
}

func TestDispatchFailureSourcePresentInvalidClassFailsClosed(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	insertPassiveEvent(t, db, producer, cfg, dispatch.Success)
	reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), cfg.CoreInstance, cfg.ConfigRevision)
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
	restoreDispatchFailureColumn(t, db)
	dispatchExec(t, db, `ALTER TABLE bridge.dispatch_observation DROP COLUMN failure_class;
	 ALTER TABLE bridge.dispatch_observation ADD COLUMN failure_class INTEGER NOT NULL DEFAULT 7`)
	_, err = dispatch.RunRound(t.Context(), cfg, store, nil, sources)
	require.Error(t, err, "a present but invalid column must not be replaced by legacy neutral evidence")
	after, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
