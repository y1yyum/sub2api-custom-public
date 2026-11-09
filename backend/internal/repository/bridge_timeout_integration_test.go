//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestBridgeTimeoutAdditiveSchemaPreservesLegacyFacts(t *testing.T) {
	resetTelemetrySchema(t)
	for _, name := range []string{"005_telemetry.sql", "006_telemetry_ttft.sql", "007_telemetry_failure.sql"} {
		raw, err := os.ReadFile("../custombridge/migrations/" + name)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(t.Context(), string(raw))
		require.NoError(t, err)
	}
	oldColumns := bridgeTTFTLegacyColumns + ", ttft_millis, failure_class"
	insertOld := func(attempt string) {
		t.Helper()
		_, err := integrationDB.ExecContext(t.Context(), `INSERT INTO bridge.dispatch_observation (`+oldColumns+`)
		 VALUES ($1,'old-core','old-target',1,42,'credential',1,'provider','pool','policy','model','region',
		 'upstream_failure',TRUE,0,20,clock_timestamp(),1,$2,'old-request',$3,0,'')`, uuid.NewString(), attempt, uuid.NewString())
		require.NoError(t, err)
	}
	insertOld("before-008")
	var before, after string
	require.NoError(t, integrationDB.QueryRowContext(t.Context(), `SELECT to_jsonb(o)::text FROM bridge.dispatch_observation o`).Scan(&before))
	cmd := newBridgeReceiptCommand(t)
	cfg, _ := producerTelemetryConfig(t, cmd, 1)
	_, err := custombridge.ActivateTelemetry(t.Context(), integrationDB, cfg, 900)
	require.ErrorIs(t, err, custombridge.ErrTelemetryStorage)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(t.Context(), `SELECT count(*) FROM bridge.dispatch_telemetry_source`).Scan(&count))
	require.Zero(t, count, "a missing migration cannot fence the old source")
	for range 2 {
		require.NoError(t, custombridge.ApplyTelemetrySchema(t.Context(), integrationDB))
	}
	var timedOut bool
	require.NoError(t, integrationDB.QueryRowContext(t.Context(), `SELECT (to_jsonb(o)-'timed_out'-'phase'-'protocol_fact'-'usage_missing')::text,timed_out
	 FROM bridge.dispatch_observation o WHERE attempt_id='before-008'`).Scan(&after, &timedOut))
	require.JSONEq(t, before, after)
	require.False(t, timedOut)
	insertOld("old-writer-after-008")
	require.NoError(t, integrationDB.QueryRowContext(t.Context(), `SELECT timed_out FROM bridge.dispatch_observation WHERE attempt_id='old-writer-after-008'`).Scan(&timedOut))
	require.False(t, timedOut)
	_, err = integrationDB.ExecContext(t.Context(), `UPDATE bridge.dispatch_observation SET timed_out=TRUE`)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(t.Context(), `UPDATE bridge.dispatch_observation SET outcome='success'`)
	var pgErr *pq.Error
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, pq.ErrorCode("23514"), pgErr.Code)
	rows, err := integrationDB.QueryContext(t.Context(), `SELECT `+oldColumns+` FROM bridge.dispatch_observation`)
	require.NoError(t, err)
	columns, err := rows.Columns()
	require.NoError(t, err)
	require.Len(t, columns, 23)
	require.NoError(t, rows.Close())
	require.Equal(t, strings.Fields(oldColumns+", timed_out"), strings.Fields(custombridge.TelemetryEventColumns))
	_, err = custombridge.ActivateTelemetry(t.Context(), integrationDB, cfg, 900)
	require.NoError(t, err)
}

func TestBridgeTimeoutProducerPersistsBoundStatusOnce(t *testing.T) {
	resetTelemetrySchema(t)
	require.NoError(t, custombridge.ApplyReceiptSchema(t.Context(), integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(t.Context(), integrationDB))
	require.NoError(t, custombridge.ApplyTelemetrySchema(t.Context(), integrationDB))
	for n, status := range []int{504, 503} {
		cmd := newBridgeReceiptCommand(t)
		cfg, credentials := producerTelemetryConfig(t, cmd, int64(n+1))
		producer, err := custombridge.ActivateTelemetry(t.Context(), integrationDB, cfg, 900)
		require.NoError(t, err)
		envelope := bridgeForwardEnvelope(cmd)
		attempts := custombridge.NewForwardAttemptRepository(integrationDB)
		require.NoError(t, attempts.Receive(t.Context(), envelope, cmd.RequestID))
		require.NoError(t, attempts.ClaimSend(t.Context(), envelope.InternalKeyID, envelope.AttemptID))
		observed, attempt := producer.Begin(t.Context(), envelope, cmd.RequestID)
		hook := custombridge.PrepareTelemetryHTTP(observed, cmd.AccountID, credentials)
		response := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("synthetic-error"))}
		hook.Record(response, nil, time.Millisecond)
		raw, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, "synthetic-error", string(raw))
		require.NoError(t, response.Body.Close())
		require.NoError(t, attempt.Finish(t.Context(), status, nil))
		_, duplicate := producer.Begin(t.Context(), envelope, cmd.RequestID)
		require.NoError(t, duplicate.Finish(t.Context(), 504, context.DeadlineExceeded))
		var timedOut, evidence bool
		var outcome string
		var count int
		require.NoError(t, integrationDB.QueryRowContext(t.Context(), `SELECT outcome,timed_out,provider_evidence
		 FROM bridge.dispatch_observation WHERE attempt_id=$1`, envelope.AttemptID).Scan(&outcome, &timedOut, &evidence))
		require.Equal(t, "upstream_failure", outcome)
		require.True(t, evidence)
		require.Equal(t, status == 504, timedOut)
		require.NoError(t, integrationDB.QueryRowContext(t.Context(), `SELECT count(*) FROM bridge.dispatch_observation WHERE attempt_id=$1`, envelope.AttemptID).Scan(&count))
		require.Equal(t, 1, count)
		requireBridgeReceiptCount(t, cmd, 0)
	}
}

func restoreBridgeTimeoutColumn(t *testing.T, db *sql.DB) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, `ALTER TABLE bridge.dispatch_observation DROP COLUMN IF EXISTS timed_out`)
		require.NoError(t, err)
		require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, db))
	})
}

func TestBridgeTimeoutSourceTruthLegacyAndFailure(t *testing.T) {
	for _, mode := range []string{"trusted", "legacy", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			db := dispatchDB(t)
			cfg, producer := passiveConfig(t, db)
			if mode != "legacy" {
				cfg.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}
				cfg.HealthWindow = &dispatch.HealthWindowConfig{}
			}
			event := insertPassiveEvent(t, db, producer, cfg, dispatch.UpstreamFailure)
			reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), cfg.CoreInstance, cfg.ConfigRevision)
			require.NoError(t, err)
			if mode == "trusted" || mode == "legacy" {
				dispatchExec(t, db, `UPDATE bridge.dispatch_observation SET timed_out=TRUE WHERE event_id=$1`, event.EventID)
			}
			before, err := reader.Read(t.Context(), cfg)
			require.NoError(t, err)
			require.Len(t, before, 1)
			require.Equal(t, mode == "trusted", before[0].TimedOut)
			store := dispatch.NewStore(db)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			count, err := store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, before)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			if mode == "trusted" {
				dispatchExec(t, db, `UPDATE bridge.dispatch_observation SET timed_out=FALSE WHERE event_id=$1`, event.EventID)
				after, err := reader.Read(t.Context(), cfg)
				require.NoError(t, err)
				_, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, after)
				require.ErrorIs(t, err, dispatch.ErrObservationConflict)
				return
			}
			restoreBridgeTimeoutColumn(t, db)
			if mode == "legacy" {
				raw, err := json.Marshal(before[0].Observation)
				require.NoError(t, err)
				require.NotContains(t, string(raw), "timed_out")
				dispatchExec(t, db, `ALTER TABLE bridge.dispatch_observation DROP COLUMN timed_out`)
				after, err := reader.Read(t.Context(), cfg)
				require.NoError(t, err)
				require.Equal(t, before, after)
				count, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, after)
				require.NoError(t, err)
				require.Zero(t, count)
				return
			}
			rdb, _ := dispatchLoadRedis(t)
			load, err := dispatch.NewRedisLoadReader(rdb, cfg.Telemetry.SlotTTLSeconds)
			require.NoError(t, err)
			sources := &dispatch.RuntimeSources{Passive: reader, Load: load}
			_, err = dispatch.RunRound(t.Context(), cfg, store, nil, sources)
			require.NoError(t, err)
			snapshot, err := os.ReadFile(cfg.SnapshotFile)
			require.NoError(t, err)
			dispatchExec(t, db, `ALTER TABLE bridge.dispatch_observation DROP COLUMN timed_out;
			 ALTER TABLE bridge.dispatch_observation ADD COLUMN timed_out TEXT NOT NULL DEFAULT 'invalid'`)
			_, err = dispatch.RunRound(t.Context(), cfg, store, nil, sources)
			require.Error(t, err)
			after, err := os.ReadFile(cfg.SnapshotFile)
			require.NoError(t, err)
			require.Equal(t, snapshot, after)
		})
	}
}
