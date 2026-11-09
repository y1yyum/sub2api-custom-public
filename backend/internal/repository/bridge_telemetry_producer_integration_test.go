//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func producerTelemetryConfig(t *testing.T, cmd *service.UsageBillingCommand, revision int64) (custombridge.TelemetryConfig, map[string]any) {
	t.Helper()
	credentials := map[string]any{"api_key": "synthetic-telemetry-key", "base_url": "http://127.0.0.1:12345"}
	digest, err := custombridge.TelemetryCredentialSHA256(credentials)
	require.NoError(t, err)
	return custombridge.TelemetryConfig{SchemaVersion: 1, CoreInstance: "producer-core", ConfigRevision: revision, Targets: []custombridge.TelemetryTarget{{
		TargetRef: "target", AccountID: cmd.AccountID, CredentialRef: "credential", CredentialRevision: 3, CredentialSHA256: digest,
		ProviderRef: "provider", PoolRef: "pool", PolicyRevision: "p1", Model: cmd.Model, Region: "region",
	}}}, credentials
}

func resetTelemetrySchema(t *testing.T) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS bridge CASCADE")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS bridge CASCADE")
		require.NoError(t, err)
	})
}

func TestBridgeTelemetryProducer(t *testing.T) {
	resetTelemetrySchema(t)
	ctx := context.Background()
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	cmd := newBridgeReceiptCommand(t)
	cfg, _ := producerTelemetryConfig(t, cmd, 1)
	_, err := custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.ErrorIs(t, err, custombridge.ErrTelemetryStorage)
	var table sql.NullString
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_regclass('bridge.dispatch_observation')::text`).Scan(&table))
	require.False(t, table.Valid, "startup must not apply telemetry schema")
	require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	revision := int64(0)
	for _, tc := range []struct {
		name                                                                                 string
		status                                                                               int
		commit, complete, canceled, transport, badCredentials, badAccount, truncated, noSend bool
		mutateReceipt                                                                        string
		want                                                                                 string
		body, mediaType                                                                      string
	}{
		{name: "receipt_committed_success", status: 200, commit: true, complete: true, want: "success"},
		{name: "json_failed_with_receipt", status: 200, commit: true, complete: true, body: `{"status":"failed","usage":{"input_tokens":1200,"output_tokens":30}}`, want: "unknown"},
		{name: "json_incomplete_with_receipt", status: 200, commit: true, complete: true, body: `{"status":"incomplete","usage":{"input_tokens":1200,"output_tokens":30}}`, want: "unknown"},
		{name: "json_missing_status_with_receipt", status: 200, commit: true, complete: true, body: `{"usage":{"input_tokens":1200,"output_tokens":30}}`, want: "unknown"},
		{name: "sse_completed_with_receipt", status: 200, commit: true, complete: true, mediaType: "text/event-stream", body: "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1200,\"output_tokens\":30}}}\n\ndata: [DONE]\n\n", want: "success"},
		{name: "sse_failed_with_receipt", status: 200, commit: true, complete: true, mediaType: "text/event-stream", body: "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"usage\":{\"input_tokens\":1200,\"output_tokens\":30}}}\n\n", want: "unknown"},
		{name: "receipt_not_yet_committed", status: 200, complete: true, want: "unknown"},
		{name: "missing_native_terminal_with_receipt", status: 200, commit: true, want: "unknown"},
		{name: "truncated_with_receipt", status: 200, commit: true, complete: true, truncated: true, want: "unknown"},
		{name: "receipt_wrong_attempt", status: 200, commit: true, complete: true, mutateReceipt: "attempt_id=attempt_id||'-other'", want: "unknown"},
		{name: "receipt_wrong_reservation", status: 200, commit: true, complete: true, mutateReceipt: "reservation_id=reservation_id||'-other'", want: "unknown"},
		{name: "receipt_wrong_core_request", status: 200, commit: true, complete: true, mutateReceipt: "core_request_id=core_request_id||'-other'", want: "unknown"},
		{name: "receipt_wrong_internal_key", status: 200, commit: true, complete: true, mutateReceipt: "internal_key_id=internal_key_id+1000000", want: "unknown"},
		{name: "receipt_wrong_account", status: 200, commit: true, complete: true, mutateReceipt: "account_id=account_id+1000000", want: "unknown"},
		{name: "receipt_wrong_model", status: 200, commit: true, complete: true, mutateReceipt: `billing=jsonb_set(billing,'{model}','"other"'::jsonb)`, want: "unknown"},
		{name: "provider_auth", status: 401, want: "upstream_auth"},
		{name: "provider_rate", status: 429, want: "upstream_rate_limit"},
		{name: "provider_5xx", status: 503, want: "upstream_failure"},
		{name: "transport_not_provider", status: 503, transport: true, want: "unknown"},
		{name: "rotated_credential_not_provider", status: 401, badCredentials: true, want: "unknown"},
		{name: "wrong_account_not_provider", status: 401, badAccount: true, want: "unknown"},
		{name: "client_cancel_provider_failure", status: 503, canceled: true, want: "client_canceled"},
		{name: "client_cancel_committed", status: 200, commit: true, complete: true, canceled: true, want: "client_canceled"},
		{name: "local_401", status: 401, noSend: true, want: "local_rejection"},
		{name: "local_503", status: 503, noSend: true, want: "local_rejection"},
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
			if !tc.noSend {
				require.NoError(t, attempts.ClaimSend(ctx, envelope.InternalKeyID, envelope.AttemptID))
			}
			observed, attempt := producer.Begin(ctx, envelope, cmd.RequestID)
			require.NotNil(t, attempt)
			if !tc.noSend {
				if tc.badCredentials {
					credentials["api_key"] = "synthetic-rotated-key"
				}
				accountID := cmd.AccountID
				if tc.badAccount {
					accountID++
				}
				hook := custombridge.PrepareTelemetryHTTP(observed, accountID, credentials)
				body, mediaType := tc.body, tc.mediaType
				if body == "" {
					body = `{"status":"completed","usage":{"input_tokens":1200,"output_tokens":30}}`
				}
				if mediaType == "" {
					mediaType = "application/json"
				}
				response := &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": {"120"}, "Content-Type": {mediaType}}, Body: io.NopCloser(strings.NewReader(body))}
				if tc.truncated {
					response.Body = producerTelemetryErrorBody{}
				}
				var transportErr error
				if tc.transport {
					transportErr = errors.New("do not persist this sensitive upstream error")
				}
				hook.Record(response, transportErr, 25*time.Millisecond)
				_, readErr := io.ReadAll(response.Body)
				if tc.truncated {
					require.ErrorIs(t, readErr, io.ErrUnexpectedEOF)
				} else {
					require.NoError(t, readErr)
				}
				require.NoError(t, response.Body.Close())
				if tc.complete {
					hook.Complete(tc.canceled)
				}
			}
			if tc.commit {
				result, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
				require.NoError(t, err)
				require.NotNil(t, result.BridgeReceipt)
			}
			if tc.mutateReceipt != "" {
				// Only fixed test-owned clauses, never external values.
				_, err := integrationDB.ExecContext(ctx, `UPDATE bridge.receipt_outbox SET `+tc.mutateReceipt+` WHERE attempt_id=$1`, envelope.AttemptID)
				require.NoError(t, err)
			}
			before, err := attempts.Get(ctx, envelope.InternalKeyID, envelope.AttemptID)
			require.NoError(t, err)
			var clientErr error
			if tc.canceled {
				clientErr = context.Canceled
			}
			localStatus := 200
			if tc.noSend {
				localStatus = tc.status
			}
			var wg sync.WaitGroup
			results := make(chan error, 8)
			for range 8 {
				wg.Go(func() { results <- attempt.Finish(ctx, localStatus, clientErr) })
			}
			wg.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			var outcome string
			var evidence bool
			var retry, count int
			var latency int64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT outcome,provider_evidence,retry_after_seconds,latency_millis FROM bridge.dispatch_observation WHERE internal_key_id=$1 AND attempt_id=$2`, envelope.InternalKeyID, envelope.AttemptID).Scan(&outcome, &evidence, &retry, &latency))
			require.Equal(t, tc.want, outcome)
			require.Equal(t, strings.HasPrefix(tc.want, "upstream_") || tc.want == "success", evidence)
			if tc.want == "upstream_rate_limit" {
				require.Equal(t, 120, retry)
			} else {
				require.Zero(t, retry)
			}
			require.GreaterOrEqual(t, latency, int64(0))
			require.LessOrEqual(t, latency, int64(900000))
			_, retryAttempt := producer.Begin(ctx, envelope, cmd.RequestID)
			require.NoError(t, retryAttempt.Finish(ctx, 503, nil), "a duplicate observer cannot replace a finalized event")
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM bridge.dispatch_observation WHERE internal_key_id=$1 AND attempt_id=$2`, envelope.InternalKeyID, envelope.AttemptID).Scan(&count))
			require.Equal(t, 1, count)
			after, err := attempts.Get(ctx, envelope.InternalKeyID, envelope.AttemptID)
			require.NoError(t, err)
			require.Equal(t, before, after, "observations must not change attempt or billing state")
			if tc.mutateReceipt == "" {
				requireBridgeReceiptState(t, cmd, tc.commit)
			}
		})
	}
}

type producerTelemetryErrorBody struct{}

func (producerTelemetryErrorBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (producerTelemetryErrorBody) Close() error             { return nil }

func TestBridgeTelemetryGenerationFencing(t *testing.T) {
	resetTelemetrySchema(t)
	ctx := context.Background()
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	cmd := newBridgeReceiptCommand(t)
	cfg, _ := producerTelemetryConfig(t, cmd, 10)
	first, err := custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.NoError(t, err)
	digest, err := cfg.ActivationDigest(900)
	require.NoError(t, err)
	require.Equal(t, digest, first.Source().ConfigDigest)
	require.Equal(t, 900, first.Source().SlotTTLSeconds)
	second, err := custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.NoError(t, err)
	require.NotEqual(t, first.Source().ProducerGeneration, second.Source().ProducerGeneration)
	envelope := bridgeForwardEnvelope(cmd)
	require.NoError(t, custombridge.NewForwardAttemptRepository(integrationDB).Receive(ctx, envelope, cmd.RequestID))
	_, attempt := first.Begin(ctx, envelope, cmd.RequestID)
	require.ErrorIs(t, attempt.Finish(ctx, 503, nil), custombridge.ErrTelemetryFenced)
	for _, tc := range []struct {
		name   string
		change func(*custombridge.TelemetryConfig)
		ttl    int
	}{
		{"old_revision", func(c *custombridge.TelemetryConfig) { c.ConfigRevision-- }, 900},
		{"same_revision_changed_identity", func(c *custombridge.TelemetryConfig) { c.Targets[0].Region = "other" }, 900},
		{"wrong_core", func(c *custombridge.TelemetryConfig) { c.CoreInstance = "other-core"; c.ConfigRevision++ }, 900},
		{"same_revision_changed_ttl", func(*custombridge.TelemetryConfig) {}, 1800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := cfg
			copy.Targets = append([]custombridge.TelemetryTarget(nil), cfg.Targets...)
			tc.change(&copy)
			_, err := custombridge.ActivateTelemetry(ctx, integrationDB, copy, tc.ttl)
			require.ErrorIs(t, err, custombridge.ErrTelemetryFenced)
		})
	}
	var generation string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT producer_generation::text FROM bridge.dispatch_telemetry_source WHERE singleton`).Scan(&generation))
	require.Equal(t, second.Source().ProducerGeneration, generation)
	_, attempt = second.Begin(ctx, envelope, cmd.RequestID)
	require.NoError(t, attempt.Finish(ctx, 503, nil))
	cfg.ConfigRevision++
	third, err := custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 1800)
	require.NoError(t, err)
	require.Equal(t, 1800, third.Source().SlotTTLSeconds)
	require.NotEqual(t, digest, third.Source().ConfigDigest)
	_, attempt = second.Begin(ctx, envelope, cmd.RequestID)
	require.ErrorIs(t, attempt.Finish(ctx, 503, nil), custombridge.ErrTelemetryFenced)
	// This attempt already has an event; use a fresh identity to verify missing
	// attempts cannot create observations or leak raw SQL/DSNs.
	envelope.AttemptID = "not-received"
	_, attempt = third.Begin(ctx, envelope, cmd.RequestID)
	require.ErrorIs(t, attempt.Finish(ctx, 503, nil), custombridge.ErrTelemetryStorage)
	_, err = integrationDB.ExecContext(ctx, `ALTER TABLE bridge.dispatch_observation RENAME TO unavailable_observation`)
	require.NoError(t, err)
	defer func() {
		_, err := integrationDB.ExecContext(ctx, `ALTER TABLE bridge.unavailable_observation RENAME TO dispatch_observation`)
		require.NoError(t, err)
	}()
	_, attempt = third.Begin(ctx, bridgeForwardEnvelope(cmd), cmd.RequestID)
	err = attempt.Finish(ctx, 503, nil)
	require.ErrorIs(t, err, custombridge.ErrTelemetryStorage)
	require.Equal(t, "bridge telemetry storage unavailable", err.Error())
}

func TestBridgeTelemetrySchemaCommandCompatibility(t *testing.T) {
	resetTelemetrySchema(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "bridge-schema")
	build := exec.CommandContext(ctx, "go", "build", "-p", "2", "-o", binary, "../../cmd/bridge-schema")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	dsnPath := filepath.Join(t.TempDir(), "dsn")
	require.NoError(t, os.WriteFile(dsnPath, []byte(integrationDSN), 0600))
	run := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(ctx, binary, append(args, "--dsn-file", dsnPath)...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	ctxSQL := context.Background()
	var receipt, forward, telemetry sql.NullString
	check := func() {
		t.Helper()
		require.NoError(t, integrationDB.QueryRowContext(ctxSQL, `SELECT to_regclass('bridge.receipt_outbox')::text,to_regclass('bridge.forward_attempt')::text,to_regclass('bridge.dispatch_observation')::text`).Scan(&receipt, &forward, &telemetry))
	}
	run("--apply-telemetry")
	check()
	require.True(t, telemetry.Valid)
	require.False(t, receipt.Valid)
	require.False(t, forward.Valid)
	_, err = integrationDB.ExecContext(ctxSQL, "DROP SCHEMA bridge CASCADE")
	require.NoError(t, err)
	run("--apply")
	check()
	require.False(t, telemetry.Valid)
	require.True(t, receipt.Valid)
	require.True(t, forward.Valid)
}
