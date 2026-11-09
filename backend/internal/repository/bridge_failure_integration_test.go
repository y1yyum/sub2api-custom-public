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

func bridgeFailureRules() []custombridge.ErrorRule {
	return []custombridge.ErrorRule{
		{Status: 429, Code: "operator-quota", FailureClass: "quota_exhausted"},
		{Status: 403, Code: "operator-ban", Type: "operator-account", FailureClass: "credential_banned"},
		{Status: 403, Code: "operator-model", FailureClass: "capability_denied"},
	}
}

func TestBridgeFailureAdditiveSchemaCompatibility(t *testing.T) {
	resetTelemetrySchema(t)
	ctx := t.Context()
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	var table sql.NullString
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_regclass('bridge.dispatch_observation')::text`).Scan(&table))
	require.False(t, table.Valid, "ordinary schemas must not activate telemetry")
	for _, migration := range []struct{ path, hash string }{
		{"005_telemetry.sql", "330e54c006cd28c27f2f7464e44b1e6e85cd14bfb8e35c581bf7ae4aa08a52b4"},
		{"006_telemetry_ttft.sql", "dce6139690cf858978b345bedea05e2cd5e221e9894988fdb53acc59dcea4018"},
	} {
		raw, err := os.ReadFile("../custombridge/migrations/" + migration.path)
		require.NoError(t, err)
		require.Equal(t, migration.hash, fmt.Sprintf("%x", sha256.Sum256(raw)), "existing migrations are immutable")
		_, err = integrationDB.ExecContext(ctx, string(raw))
		require.NoError(t, err)
	}
	legacyColumns := bridgeTTFTLegacyColumns + ", ttft_millis"
	insertLegacy := func(attempt string) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO bridge.dispatch_observation (`+legacyColumns+`)
		 VALUES ($1,'legacy-core','legacy-target',1,42,'credential',1,'provider','pool','p1','gpt-5.1','region',
		 'upstream_rate_limit',true,120,42,'2026-11-07T00:00:00Z',12,$2,'legacy-request',$3,0)`, uuid.NewString(), attempt, uuid.NewString())
		require.NoError(t, err)
	}
	insertLegacy("before-007")
	cmd := newBridgeReceiptCommand(t)
	cfg, _ := producerTelemetryConfig(t, cmd, 1)
	digest, err := cfg.ActivationDigest(900)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO bridge.dispatch_telemetry_source
	 (singleton,core_instance,config_revision,config_digest,slot_ttl_seconds,activated_at,producer_generation)
	 VALUES (TRUE,$1,$2,$3,900,clock_timestamp(),$4)`, cfg.CoreInstance, cfg.ConfigRevision, digest, uuid.NewString())
	require.NoError(t, err)
	var sourceBefore, rowBefore string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_jsonb(s)::text FROM bridge.dispatch_telemetry_source s`).Scan(&sourceBefore))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_jsonb(o)::text FROM bridge.dispatch_observation o WHERE attempt_id='before-007'`).Scan(&rowBefore))
	_, err = custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.ErrorIs(t, err, custombridge.ErrTelemetryStorage, "the new producer requires explicit 007 even with no rules")
	var sourceAfter string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_jsonb(s)::text FROM bridge.dispatch_telemetry_source s`).Scan(&sourceAfter))
	require.JSONEq(t, sourceBefore, sourceAfter, "failed activation cannot fence the old producer")
	for range 2 {
		require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	}
	var rowAfter, class string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT (to_jsonb(o)-'failure_class'-'timed_out'-'phase'-'protocol_fact'-'usage_missing')::text,failure_class
	 FROM bridge.dispatch_observation o WHERE attempt_id='before-007'`).Scan(&rowAfter, &class))
	require.JSONEq(t, rowBefore, rowAfter)
	require.Empty(t, class)
	insertLegacy("old-writer-after-007")
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT failure_class FROM bridge.dispatch_observation WHERE attempt_id='old-writer-after-007'`).Scan(&class))
	require.Empty(t, class, "old writers omit the new defaulted column")
	var typ, nullable string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT data_type,is_nullable FROM information_schema.columns
	 WHERE table_schema='bridge' AND table_name='dispatch_observation' AND column_name='failure_class'`).Scan(&typ, &nullable))
	require.Equal(t, "text", typ)
	require.Equal(t, "NO", nullable)
	for _, invalid := range []struct{ outcome, class string }{
		{"upstream_rate_limit", "credential_banned"}, {"upstream_rate_limit", "capability_denied"},
		{"upstream_auth", "quota_exhausted"}, {"upstream_failure", "quota_exhausted"},
		{"upstream_auth", "operator-guess"}, {"upstream_auth", "Quota_Exhausted"},
	} {
		_, err := integrationDB.ExecContext(ctx, `UPDATE bridge.dispatch_observation SET outcome=$1,failure_class=$2`, invalid.outcome, invalid.class)
		var pgErr *pq.Error
		require.ErrorAs(t, err, &pgErr)
		require.Equal(t, pq.ErrorCode("23514"), pgErr.Code)
	}
	for _, valid := range []struct{ outcome, class string }{
		{"upstream_rate_limit", "quota_exhausted"}, {"upstream_auth", "credential_banned"},
		{"upstream_auth", "capability_denied"}, {"upstream_auth", ""},
	} {
		_, err := integrationDB.ExecContext(ctx, `UPDATE bridge.dispatch_observation SET outcome=$1,failure_class=$2,retry_after_seconds=0`, valid.outcome, valid.class)
		require.NoError(t, err)
	}
	rows, err := integrationDB.QueryContext(ctx, `SELECT `+legacyColumns+` FROM bridge.dispatch_observation`)
	require.NoError(t, err)
	columns, err := rows.Columns()
	require.NoError(t, err)
	require.Len(t, columns, 22, "old readers retain their shape")
	require.NoError(t, rows.Close())
	require.Equal(t, strings.Fields(legacyColumns+", failure_class, timed_out"), strings.Fields(custombridge.TelemetryEventColumns))
	_, err = custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.NoError(t, err)
}

func TestBridgeFailurePersistenceAndTrustedBoundaries(t *testing.T) {
	resetTelemetrySchema(t)
	ctx := t.Context()
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	for i, tc := range []struct {
		name, body, class, outcome, media                                       string
		status                                                                  int
		noRules, noEOF, canceled, multiple, badCredentials, readFailure, noSend bool
	}{
		{name: "quota", status: 429, body: `{"error":{"code":"operator-quota","message":"sensitive-raw-message"}}`, class: "quota_exhausted", outcome: "upstream_rate_limit"},
		{name: "ban", status: 403, body: `{"error":{"code":"operator-ban","type":"operator-account","message":"sensitive-raw-message"}}`, class: "credential_banned", outcome: "upstream_auth"},
		{name: "capability", status: 403, body: `{"code":"operator-model","message":"sensitive-raw-message"}`, class: "capability_denied", outcome: "upstream_auth"},
		{name: "unconfigured403", status: 403, noRules: true, outcome: "unknown"},
		{name: "unconfigured429", status: 429, noRules: true, body: `{"error":{"code":"operator-quota"}}`, outcome: "upstream_rate_limit"},
		{name: "401_legacy", status: 401, outcome: "upstream_auth"},
		{name: "incomplete403", status: 403, noEOF: true, outcome: "unknown"},
		{name: "incomplete429", status: 429, noEOF: true, body: `{"error":{"code":"operator-quota"}}`, outcome: "upstream_rate_limit"},
		{name: "cancel", status: 403, canceled: true, outcome: "client_canceled"},
		{name: "multiple", status: 403, multiple: true, outcome: "unknown"},
		{name: "credentials", status: 403, badCredentials: true, outcome: "unknown"},
		{name: "read_failure", status: 403, readFailure: true, outcome: "unknown"},
		{name: "plain", status: 403, media: "text/plain", outcome: "unknown"},
		{name: "message_only", status: 403, body: `{"error":{"message":"operator-model operator-ban"}}`, outcome: "unknown"},
		{name: "duplicate", status: 403, body: `{"error":{"code":"operator-model","code":"operator-model"}}`, outcome: "unknown"},
		{name: "escaped", status: 403, body: `{"error":{"c\u006fde":"operator-model"}}`, outcome: "unknown"},
		{name: "ambiguous", status: 403, body: `{"code":"operator-model","error":{"code":"operator-model"}}`, outcome: "unknown"},
		{name: "not_send_intent", status: 403, noSend: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newBridgeReceiptCommand(t)
			cfg, credentials := producerTelemetryConfig(t, cmd, int64(i+1))
			if !tc.noRules {
				cfg.Targets[0].ErrorRules = bridgeFailureRules()
			}
			producer, err := custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
			require.NoError(t, err)
			if len(cfg.Targets[0].ErrorRules) > 0 {
				for j := range cfg.Targets[0].ErrorRules {
					cfg.Targets[0].ErrorRules[j].Code = "mutated-after-activation"
				}
			}
			envelope := bridgeForwardEnvelope(cmd)
			attempts := custombridge.NewForwardAttemptRepository(integrationDB)
			require.NoError(t, attempts.Receive(ctx, envelope, cmd.RequestID))
			if !tc.noSend {
				require.NoError(t, attempts.ClaimSend(ctx, envelope.InternalKeyID, envelope.AttemptID))
			}
			observed, attempt := producer.Begin(ctx, envelope, cmd.RequestID)
			if tc.badCredentials {
				credentials["api_key"] = "rotated-test-key"
			}
			hook := custombridge.PrepareTelemetryHTTP(observed, cmd.AccountID, credentials)
			body, media := tc.body, tc.media
			if body == "" {
				body = `{"error":{"code":"operator-model","message":"sensitive-raw-message"}}`
			}
			if media == "" {
				media = "application/json"
			}
			response := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {media}, "Retry-After": {"120"}}, Body: io.NopCloser(strings.NewReader(body))}
			if tc.readFailure {
				response.Body = io.NopCloser(io.MultiReader(strings.NewReader(body), producerTelemetryErrorBody{}))
			}
			hook.Record(response, nil, 25*time.Millisecond)
			reader := io.Reader(response.Body)
			if tc.noEOF {
				reader = io.LimitReader(reader, int64(len(body)))
			}
			got, err := io.ReadAll(reader)
			if tc.readFailure {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, body, string(got))
			require.NoError(t, response.Body.Close())
			hook.Complete(tc.canceled)
			if tc.multiple {
				hook.Record(nil, nil, 0)
			}
			before, err := attempts.Get(ctx, envelope.InternalKeyID, envelope.AttemptID)
			require.NoError(t, err)
			if tc.noSend {
				require.ErrorIs(t, attempt.Finish(ctx, tc.status, nil), custombridge.ErrTelemetryStorage)
				var count int
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM bridge.dispatch_observation WHERE attempt_id=$1`, envelope.AttemptID).Scan(&count))
				require.Zero(t, count, "provider evidence requires a durable native send intent")
			} else {
				var wg sync.WaitGroup
				results := make(chan error, 8)
				for range 8 {
					wg.Go(func() { results <- attempt.Finish(ctx, tc.status, nil) })
				}
				wg.Wait()
				close(results)
				for err := range results {
					require.NoError(t, err)
				}
				freshDB, err := sql.Open("postgres", integrationDSN)
				require.NoError(t, err)
				defer func() { _ = freshDB.Close() }()
				var outcome, class, persisted string
				var evidence bool
				var retry int
				require.NoError(t, freshDB.QueryRowContext(ctx, `SELECT outcome,failure_class,provider_evidence,retry_after_seconds,to_jsonb(o)::text
				 FROM bridge.dispatch_observation o WHERE internal_key_id=$1 AND attempt_id=$2`, envelope.InternalKeyID, envelope.AttemptID).Scan(&outcome, &class, &evidence, &retry, &persisted))
				require.Equal(t, tc.outcome, outcome)
				require.Equal(t, tc.class, class)
				require.Equal(t, strings.HasPrefix(tc.outcome, "upstream_"), evidence)
				require.NotContains(t, persisted, "sensitive-raw-message")
				require.NotContains(t, persisted, "operator-model")
				if tc.status == 429 {
					require.Equal(t, 120, retry)
				} else {
					require.Zero(t, retry)
				}
				_, duplicate := producer.Begin(ctx, envelope, cmd.RequestID)
				require.NoError(t, duplicate.Finish(ctx, 503, nil))
				var count int
				var after string
				require.NoError(t, freshDB.QueryRowContext(ctx, `SELECT count(*),max(failure_class) FROM bridge.dispatch_observation
				 WHERE internal_key_id=$1 AND attempt_id=$2`, envelope.InternalKeyID, envelope.AttemptID).Scan(&count, &after))
				require.Equal(t, 1, count)
				require.Equal(t, class, after, "a duplicate observer cannot replace a class")
			}
			after, err := attempts.Get(ctx, envelope.InternalKeyID, envelope.AttemptID)
			require.NoError(t, err)
			require.Equal(t, before, after)
			requireBridgeReceiptState(t, cmd, false)
			requireBridgeReceiptCount(t, cmd, 0)
		})
	}
}

func TestBridgeFailureRulesFenceConfigurationChanges(t *testing.T) {
	resetTelemetrySchema(t)
	ctx := context.Background()
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, integrationDB))
	cmd := newBridgeReceiptCommand(t)
	cfg, _ := producerTelemetryConfig(t, cmd, 1)
	producer, err := custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.NoError(t, err)
	cfg.Targets[0].ErrorRules = bridgeFailureRules()
	_, err = custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.ErrorIs(t, err, custombridge.ErrTelemetryFenced)
	var generation string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT producer_generation::text FROM bridge.dispatch_telemetry_source`).Scan(&generation))
	require.Equal(t, producer.Source().ProducerGeneration, generation)
	cfg.ConfigRevision++
	_, err = custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.NoError(t, err)
	envelope := bridgeForwardEnvelope(cmd)
	require.NoError(t, custombridge.NewForwardAttemptRepository(integrationDB).Receive(ctx, envelope, cmd.RequestID))
	_, attempt := producer.Begin(ctx, envelope, cmd.RequestID)
	require.ErrorIs(t, attempt.Finish(ctx, 503, nil), custombridge.ErrTelemetryFenced)
	cfg.Targets[0].ErrorRules = nil
	_, err = custombridge.ActivateTelemetry(ctx, integrationDB, cfg, 900)
	require.ErrorIs(t, err, custombridge.ErrTelemetryFenced, "removing a policy also requires a new revision")
}
