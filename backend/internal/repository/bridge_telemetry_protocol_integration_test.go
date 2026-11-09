//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type bridgeProtocolTimeout struct{}

func (bridgeProtocolTimeout) Error() string   { return "synthetic-sensitive-timeout" }
func (bridgeProtocolTimeout) Timeout() bool   { return true }
func (bridgeProtocolTimeout) Temporary() bool { return true }

func TestBridgeTelemetryProtocolAdditiveSchema(t *testing.T) {
	resetTelemetrySchema(t)
	ctx := t.Context()
	for _, migration := range []struct{ path, hash string }{
		{"005_telemetry.sql", "330e54c006cd28c27f2f7464e44b1e6e85cd14bfb8e35c581bf7ae4aa08a52b4"},
		{"006_telemetry_ttft.sql", "dce6139690cf858978b345bedea05e2cd5e221e9894988fdb53acc59dcea4018"},
		{"007_telemetry_failure.sql", "f945d8c9dbb82cbc9b4aef413b8cdc093d4fd5d23ee8816d914b10f9c4608c96"},
		{"008_telemetry_timeout.sql", "76e64bad897ba17160bd6521ba94236e7c54bdded49b9a5c539d9a33296499aa"},
	} {
		raw, err := os.ReadFile("../custombridge/migrations/" + migration.path)
		require.NoError(t, err)
		require.Equal(t, migration.hash, fmt.Sprintf("%x", sha256.Sum256(raw)), "existing migrations are immutable")
		_, err = integrationDB.ExecContext(ctx, string(raw))
		require.NoError(t, err)
	}
	insertOld := func(attempt, outcome string, evidence bool) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO bridge.dispatch_observation (`+custombridge.TelemetryEventColumns+`)
		 VALUES ($1,'old-core','old-target',1,42,'credential',1,'provider','pool','policy','model','region',
		 $4,$5,0,20,clock_timestamp(),1,$2,'old-request',$3,0,'',FALSE)`, uuid.NewString(), attempt, uuid.NewString(), outcome, evidence)
		require.NoError(t, err)
	}
	insertOld("before-009", "upstream_failure", true)
	var before string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_jsonb(o)::text FROM bridge.dispatch_observation o`).Scan(&before))
	cmd := newBridgeReceiptCommand(t)
	cfg, _ := producerTelemetryConfig(t, cmd, 1)
	_, err := custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.ErrorIs(t, err, custombridge.ErrTelemetryStorage, "the new producer requires 009")
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM bridge.dispatch_telemetry_source`).Scan(&count))
	require.Zero(t, count, "a missing migration cannot fence the old source")
	for range 2 {
		require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	}
	var after, phase, fact string
	var usageMissing bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT (to_jsonb(o)-'phase'-'protocol_fact'-'usage_missing')::text,phase,protocol_fact,usage_missing
	 FROM bridge.dispatch_observation o WHERE attempt_id='before-009'`).Scan(&after, &phase, &fact, &usageMissing))
	require.JSONEq(t, before, after)
	require.Empty(t, phase)
	require.Empty(t, fact)
	require.False(t, usageMissing)
	insertOld("old-writer-after-009", "unknown", false)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT phase,protocol_fact,usage_missing FROM bridge.dispatch_observation
	 WHERE attempt_id='old-writer-after-009'`).Scan(&phase, &fact, &usageMissing))
	require.Equal(t, []any{"", "", false}, []any{phase, fact, usageMissing})
	for _, invalid := range []string{
		`phase='body'`,
		`protocol_fact='provider_error'`,
		`phase='terminal',protocol_fact='semantic_failure'`,
		`phase='content',protocol_fact='stream_break'`,
		`phase='connect',protocol_fact='local_timeout'`,
		`protocol_fact='bridge_deadline'`,
		`usage_missing=TRUE`,
	} {
		// Fixed test-owned clauses: the upstream failure row may never carry a non-penalizing fact.
		_, err := integrationDB.ExecContext(ctx, `UPDATE bridge.dispatch_observation SET `+invalid+` WHERE attempt_id='before-009'`)
		var pgErr *pq.Error
		require.ErrorAs(t, err, &pgErr, invalid)
		require.Equal(t, pq.ErrorCode("23514"), pgErr.Code, invalid)
	}
	for _, invalid := range []string{
		`phase='terminal',protocol_fact='stream_break'`,
		`phase='headers',protocol_fact='semantic_failure'`,
		`phase='terminal',protocol_fact='non_text_terminal',ttft_millis=5`,
		`phase='content',usage_missing=TRUE`,
		`phase='terminal',protocol_fact='incomplete',usage_missing=TRUE`,
	} {
		_, err := integrationDB.ExecContext(ctx, `UPDATE bridge.dispatch_observation SET `+invalid+` WHERE attempt_id='old-writer-after-009'`)
		var pgErr *pq.Error
		require.ErrorAs(t, err, &pgErr, invalid)
		require.Equal(t, pq.ErrorCode("23514"), pgErr.Code, invalid)
	}
	_, err = integrationDB.ExecContext(ctx, `UPDATE bridge.dispatch_observation SET phase='content',protocol_fact='stream_break' WHERE attempt_id='old-writer-after-009'`)
	require.NoError(t, err)
	rows, err := integrationDB.QueryContext(ctx, `SELECT `+custombridge.TelemetryEventColumns+` FROM bridge.dispatch_observation`)
	require.NoError(t, err)
	columns, err := rows.Columns()
	require.NoError(t, err)
	require.Len(t, columns, 24, "old readers keep their column order")
	require.NoError(t, rows.Close())
	_, err = custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.NoError(t, err)
}

func TestBridgeTelemetryProtocolProducerPersistsFactsOnce(t *testing.T) {
	resetTelemetrySchema(t)
	ctx := t.Context()
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	const delta = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic output\"}\n\n"
	const completed = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	revision := int64(0)
	for _, tc := range []struct {
		name, body, media    string
		status               int
		transport, readErr   error
		clientErr            error
		commit               bool
		outcome, phase, fact string
		usageMissing         bool
	}{
		{name: "sse_semantic_failure", status: 200, media: "text/event-stream", body: "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"synthetic-sensitive-code\"}}}\n\n", commit: true, outcome: "unknown", phase: "terminal", fact: "semantic_failure"},
		{name: "json_incomplete", status: 200, media: "application/json", body: `{"status":"incomplete"}`, commit: true, outcome: "unknown", phase: "terminal", fact: "incomplete"},
		{name: "non_text_success_without_usage", status: 200, media: "text/event-stream", body: ": keepalive\n\n" + completed, commit: true, outcome: "success", phase: "terminal", fact: "non_text_terminal", usageMissing: true},
		{name: "non_text_without_receipt", status: 200, media: "text/event-stream", body: completed, outcome: "unknown", phase: "terminal", fact: "non_text_terminal", usageMissing: true},
		{name: "text_success_without_usage", status: 200, media: "text/event-stream", body: delta + completed, commit: true, outcome: "success", phase: "terminal", usageMissing: true},
		{name: "stream_break_after_content", status: 200, media: "text/event-stream", body: delta, commit: true, outcome: "unknown", phase: "content", fact: "stream_break"},
		{name: "read_timeout_before_content", status: 200, media: "text/event-stream", readErr: bridgeProtocolTimeout{}, outcome: "unknown", phase: "headers", fact: "local_timeout"},
		{name: "connect_timeout", transport: bridgeProtocolTimeout{}, outcome: "unknown", phase: "connect", fact: "local_timeout"},
		{name: "connect_failure_unproven", transport: errors.New("synthetic-sensitive-dial"), outcome: "unknown", phase: "connect"},
		{name: "bridge_deadline", transport: bridgeProtocolTimeout{}, clientErr: context.DeadlineExceeded, outcome: "client_canceled", phase: "connect", fact: "bridge_deadline"},
		{name: "provider_failure_keeps_legacy_shape", status: 503, media: "application/json", body: `{"error":{"message":"synthetic-sensitive"}}`, outcome: "upstream_failure", phase: "headers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			revision++
			cmd := newBridgeReceiptCommand(t)
			cfg, credentials := producerTelemetryConfig(t, cmd, revision)
			producer, err := custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
			require.NoError(t, err)
			envelope := bridgeForwardEnvelope(cmd)
			attempts := custombridge.NewForwardAttemptRepository(integrationDB)
			require.NoError(t, attempts.Receive(ctx, envelope, cmd.RequestID))
			require.NoError(t, attempts.ClaimSend(ctx, envelope.InternalKeyID, envelope.AttemptID))
			observed, attempt := producer.Begin(ctx, envelope, cmd.RequestID)
			hook := custombridge.PrepareTelemetryHTTP(observed, cmd.AccountID, credentials)
			if tc.transport != nil {
				hook.Record(nil, tc.transport, 5*time.Millisecond)
			} else {
				var body io.ReadCloser = io.NopCloser(strings.NewReader(tc.body))
				if tc.readErr != nil {
					body = io.NopCloser(io.MultiReader(strings.NewReader(tc.body), iotestErrReader{tc.readErr}))
				}
				response := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.media}}, Body: body}
				hook.Record(response, nil, 5*time.Millisecond)
				_, _ = io.Copy(io.Discard, response.Body)
				require.NoError(t, response.Body.Close())
				hook.Complete(false)
			}
			if tc.commit {
				result, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
				require.NoError(t, err)
				require.NotNil(t, result.BridgeReceipt)
			}
			localStatus := 200
			if tc.transport != nil {
				localStatus = 502
			}
			require.NoError(t, attempt.Finish(ctx, localStatus, tc.clientErr))
			_, duplicate := producer.Begin(ctx, envelope, cmd.RequestID)
			require.NoError(t, duplicate.Finish(ctx, 504, context.DeadlineExceeded), "a duplicate observer cannot replace the facts")
			var outcome, phase, fact, raw string
			var usageMissing bool
			var count int
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT outcome,phase,protocol_fact,usage_missing,to_jsonb(o)::text
			 FROM bridge.dispatch_observation o WHERE attempt_id=$1`, envelope.AttemptID).Scan(&outcome, &phase, &fact, &usageMissing, &raw))
			require.Equal(t, []any{tc.outcome, tc.phase, tc.fact, tc.usageMissing}, []any{outcome, phase, fact, usageMissing})
			require.NotContains(t, raw, "synthetic-sensitive")
			require.NotContains(t, raw, "synthetic-telemetry-key")
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM bridge.dispatch_observation WHERE attempt_id=$1`, envelope.AttemptID).Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}

type iotestErrReader struct{ err error }

func (r iotestErrReader) Read([]byte) (int, error) { return 0, r.err }
