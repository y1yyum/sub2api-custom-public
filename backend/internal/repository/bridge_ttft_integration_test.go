//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const bridgeTTFTLegacyColumns = `event_id, core_instance, target_ref, config_revision,
	account_id, credential_ref, credential_revision, provider_ref, pool_ref,
	policy_revision, model, region, outcome, provider_evidence,
	retry_after_seconds, latency_millis, observed_at, internal_key_id,
	attempt_id, core_request_id, producer_generation`

const bridgeTTFTText = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic output\"}\n\n"
const bridgeTTFTCompleted = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"

func TestBridgeTTFTAdditiveSchemaCompatibility(t *testing.T) {
	resetTelemetrySchema(t)
	ctx := context.Background()
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	var table sql.NullString
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_regclass('bridge.dispatch_observation')::text`).Scan(&table))
	require.False(t, table.Valid)
	legacy, err := os.ReadFile("../custombridge/migrations/005_telemetry.sql")
	require.NoError(t, err)
	require.Equal(t, "330e54c006cd28c27f2f7464e44b1e6e85cd14bfb8e35c581bf7ae4aa08a52b4", fmt.Sprintf("%x", sha256.Sum256(legacy)), "005 must retain its original bytes")
	_, err = integrationDB.ExecContext(ctx, string(legacy))
	require.NoError(t, err)
	insertLegacy := func(attempt string) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO bridge.dispatch_observation (`+bridgeTTFTLegacyColumns+`)
			VALUES ($1,'legacy-core','legacy-target',1,42,'credential',1,'provider','pool','p1','gpt-5.1','region',
			'success',true,0,42,'2026-11-06T00:00:00Z',12,$2,'legacy-request',$3)`, uuid.NewString(), attempt, uuid.NewString())
		require.NoError(t, err)
	}
	insertLegacy("before-upgrade")
	var before string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_jsonb(o)::text FROM bridge.dispatch_observation o WHERE attempt_id='before-upgrade'`).Scan(&before))
	cmd := newBridgeReceiptCommand(t)
	cfg, _ := producerTelemetryConfig(t, cmd, 1)
	_, err = custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.ErrorIs(t, err, custombridge.ErrTelemetryStorage, "enabled new producer must require explicit 006")
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM bridge.dispatch_telemetry_source`).Scan(&count))
	require.Zero(t, count, "failed activation must not mutate the old source")
	for range 2 {
		require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	}
	var after string
	var ttft int64
	var failureClass string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT (to_jsonb(o)-'ttft_millis'-'failure_class'-'timed_out'-'phase'-'protocol_fact'-'usage_missing')::text,ttft_millis,failure_class FROM bridge.dispatch_observation o WHERE attempt_id='before-upgrade'`).Scan(&after, &ttft, &failureClass))
	require.JSONEq(t, before, after, "upgrade must preserve all original row fields")
	require.Zero(t, ttft)
	require.Empty(t, failureClass)
	insertLegacy("old-writer-after-upgrade")
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT ttft_millis FROM bridge.dispatch_observation WHERE attempt_id='old-writer-after-upgrade'`).Scan(&ttft))
	require.Zero(t, ttft, "old writers may omit the new column")
	var typ, nullable string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT data_type,is_nullable FROM information_schema.columns
		WHERE table_schema='bridge' AND table_name='dispatch_observation' AND column_name='ttft_millis'`).Scan(&typ, &nullable))
	require.Equal(t, "bigint", typ)
	require.Equal(t, "NO", nullable)
	for _, value := range []int64{-1, 900001} {
		_, err := integrationDB.ExecContext(ctx, `UPDATE bridge.dispatch_observation SET ttft_millis=$1`, value)
		var pgErr *pq.Error
		require.ErrorAs(t, err, &pgErr)
		require.Equal(t, pq.ErrorCode("23514"), pgErr.Code)
	}
	for _, value := range []int64{900000, 0} {
		_, err := integrationDB.ExecContext(ctx, `UPDATE bridge.dispatch_observation SET ttft_millis=$1`, value)
		require.NoError(t, err)
	}
	rows, err := integrationDB.QueryContext(ctx, `SELECT `+bridgeTTFTLegacyColumns+` FROM bridge.dispatch_observation`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	require.NoError(t, err)
	require.Len(t, columns, 21, "old reader keeps its original result shape")
	count = 0
	for rows.Next() {
		values := make([]any, len(columns))
		for i := range values {
			values[i] = new(any)
		}
		require.NoError(t, rows.Scan(values...))
		count++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 2, count)
	require.Equal(t, strings.Fields(bridgeTTFTLegacyColumns+", ttft_millis, failure_class, timed_out"), strings.Fields(custombridge.TelemetryEventColumns), "new columns must be appended")
	_, err = custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.NoError(t, err)
}

func TestBridgeTTFTPersistenceAndReceiptGate(t *testing.T) {
	resetTelemetrySchema(t)
	ctx := context.Background()
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	for i, tc := range []struct {
		name, body, media, want              string
		commit, complete, canceled, multiple bool
		positive                             bool
	}{
		{name: "committed_text", body: bridgeTTFTText + bridgeTTFTCompleted, commit: true, complete: true, want: "success", positive: true},
		{name: "missing_receipt_clears_ttft", body: bridgeTTFTText + bridgeTTFTCompleted, complete: true, want: "unknown"},
		{name: "native_completion_missing", body: bridgeTTFTText + bridgeTTFTCompleted, commit: true, want: "unknown"},
		{name: "cancel_after_text", body: bridgeTTFTText + bridgeTTFTCompleted, commit: true, complete: true, canceled: true, want: "client_canceled"},
		{name: "multiple_outbound", body: bridgeTTFTText + bridgeTTFTCompleted, commit: true, complete: true, multiple: true, want: "unknown"},
		{name: "malformed_after_text", body: bridgeTTFTText + "data: {\n\n" + bridgeTTFTCompleted, commit: true, complete: true, want: "unknown"},
		{name: "truncated_after_text", body: bridgeTTFTText + strings.TrimSuffix(bridgeTTFTCompleted, "\n"), commit: true, complete: true, want: "unknown"},
		{name: "completion_without_text", body: bridgeTTFTCompleted, commit: true, complete: true, want: "success"},
		{name: "json_completion", media: "application/json", body: `{"status":"completed","output":[{"text":"full output"}]}`, commit: true, complete: true, want: "success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newBridgeReceiptCommand(t)
			cfg, credentials := producerTelemetryConfig(t, cmd, int64(i+1))
			producer, err := custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
			require.NoError(t, err)
			envelope := bridgeForwardEnvelope(cmd)
			attempts := custombridge.NewForwardAttemptRepository(integrationDB)
			require.NoError(t, attempts.Receive(ctx, envelope, cmd.RequestID))
			require.NoError(t, attempts.ClaimSend(ctx, envelope.InternalKeyID, envelope.AttemptID))
			observed, attempt := producer.Begin(ctx, envelope, cmd.RequestID)
			require.NotNil(t, attempt)
			hook := custombridge.PrepareTelemetryHTTP(observed, cmd.AccountID, credentials)
			media := tc.media
			if media == "" {
				media = "text/event-stream"
			}
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {media}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			hook.Record(response, nil, 25*time.Millisecond)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, tc.body, string(body))
			require.NoError(t, response.Body.Close())
			if tc.complete {
				hook.Complete(tc.canceled)
			}
			if tc.multiple {
				hook.Record(nil, nil, 0)
			}
			if tc.commit {
				result, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
				require.NoError(t, err)
				require.NotNil(t, result.BridgeReceipt)
			}
			before, err := attempts.Get(ctx, envelope.InternalKeyID, envelope.AttemptID)
			require.NoError(t, err)
			var wg sync.WaitGroup
			results := make(chan error, 8)
			for range 8 {
				wg.Go(func() { results <- attempt.Finish(ctx, 200, nil) })
			}
			wg.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			freshDB, err := sql.Open("postgres", integrationDSN)
			require.NoError(t, err)
			defer func() { _ = freshDB.Close() }()
			var outcome string
			var evidence bool
			var ttft, latency int64
			require.NoError(t, freshDB.QueryRowContext(ctx, `SELECT outcome,provider_evidence,ttft_millis,latency_millis
				FROM bridge.dispatch_observation WHERE internal_key_id=$1 AND attempt_id=$2`, envelope.InternalKeyID, envelope.AttemptID).Scan(&outcome, &evidence, &ttft, &latency))
			require.Equal(t, tc.want, outcome)
			require.Equal(t, tc.want == "success", evidence)
			if tc.positive {
				require.GreaterOrEqual(t, ttft, int64(25))
				require.LessOrEqual(t, ttft, latency)
			} else {
				require.Zero(t, ttft)
			}
			_, duplicate := producer.Begin(ctx, envelope, cmd.RequestID)
			require.NoError(t, duplicate.Finish(ctx, 503, nil))
			var count int
			var persistedTTFT int64
			require.NoError(t, freshDB.QueryRowContext(ctx, `SELECT count(*),max(ttft_millis) FROM bridge.dispatch_observation
				WHERE internal_key_id=$1 AND attempt_id=$2`, envelope.InternalKeyID, envelope.AttemptID).Scan(&count, &persistedTTFT))
			require.Equal(t, 1, count)
			require.Equal(t, ttft, persistedTTFT)
			after, err := attempts.Get(ctx, envelope.InternalKeyID, envelope.AttemptID)
			require.NoError(t, err)
			require.Equal(t, before, after)
			requireBridgeReceiptState(t, cmd, tc.commit)
			requireBridgeReceiptCount(t, cmd, map[bool]int{false: 0, true: 1}[tc.commit])
		})
	}
}
