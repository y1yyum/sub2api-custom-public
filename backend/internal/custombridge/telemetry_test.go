//go:build unit

package custombridge

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

const telemetryCompletedSSE = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"error\":null,\"incomplete_details\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"

type telemetryReadSpy struct {
	reader                  *strings.Reader
	maxChunk, reads, closes int
	noEOF                   bool
}

func (s *telemetryReadSpy) Read(raw []byte) (int, error) {
	s.reads++
	if s.reader.Len() == 0 && s.noEOF {
		return 0, errors.New("unexpected extra read")
	}
	if s.maxChunk > 0 && len(raw) > s.maxChunk {
		raw = raw[:s.maxChunk]
	}
	return s.reader.Read(raw)
}

func (s *telemetryReadSpy) Close() error { s.closes++; return nil }

func TestTelemetrySSECompleteBeforeUpstreamEOF(t *testing.T) {
	for _, chunk := range []int{1, 2, 3, 7, 31, 4096} {
		for _, closeFirst := range []bool{false, true} {
			ctx, attempt := telemetryTestAttempt(t)
			hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
			spy := &telemetryReadSpy{reader: strings.NewReader(telemetryCompletedSSE), maxChunk: chunk, noEOF: true}
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: spy}
			hook.Record(response, nil, time.Millisecond)
			raw := make([]byte, len(telemetryCompletedSSE))
			_, err := io.ReadFull(response.Body, raw)
			require.NoError(t, err)
			require.Equal(t, telemetryCompletedSSE, string(raw))
			require.Equal(t, "unknown", attempt.event(attempt.facts, 200, nil).Outcome, "native completion is still required")
			if closeFirst {
				require.NoError(t, response.Body.Close())
			}
			hook.Complete(false)
			if !closeFirst {
				require.NoError(t, response.Body.Close())
			}
			require.Equal(t, "success", attempt.event(attempt.facts, 200, nil).Outcome, "complete SSE frame must not wait for another read/EOF")
			require.Equal(t, (len(telemetryCompletedSSE)+chunk-1)/chunk, spy.reads)
			require.Equal(t, 1, spy.closes)
			require.Nil(t, telemetryTestBody(t, response).pending)
			require.Equal(t, "client_canceled", attempt.event(attempt.facts, 200, context.Canceled).Outcome)
		}
	}
	ctx, attempt := telemetryTestAttempt(t)
	hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
	body := `{"status":"completed","usage":{"input_tokens":1}}`
	spy := &telemetryReadSpy{reader: strings.NewReader(body), noEOF: true}
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: spy}
	hook.Record(response, nil, 0)
	_, err := io.ReadFull(response.Body, make([]byte, len(body)))
	require.NoError(t, err)
	hook.Complete(false)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "unknown", attempt.event(attempt.facts, 200, nil).Outcome, "JSON requires EOF after a single complete object")
	require.Equal(t, 1, spy.reads)
}

func TestTelemetrySSETrailingFactsRevokeCompletion(t *testing.T) {
	for _, suffix := range []string{
		"data: {", "event: response.completed\n",
		"data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n",
		"data: {\"type\":\"response.done\",\"response\":{\"status\":\"completed\"}}\n\n",
	} {
		ctx, attempt := telemetryTestAttempt(t)
		hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
		spy := &telemetryReadSpy{reader: strings.NewReader(telemetryCompletedSSE + suffix), noEOF: true}
		response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: spy}
		hook.Record(response, nil, 0)
		_, err := io.ReadFull(response.Body, make([]byte, len(telemetryCompletedSSE)))
		require.NoError(t, err)
		hook.Complete(false)
		require.Equal(t, "success", attempt.event(attempt.facts, 200, nil).Outcome)
		_, err = io.ReadFull(response.Body, make([]byte, len(suffix)))
		require.NoError(t, err)
		require.Equal(t, "unknown", attempt.event(attempt.facts, 200, nil).Outcome, suffix)
		require.NoError(t, response.Body.Close())
	}
	ctx, attempt := telemetryTestAttempt(t)
	hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader(telemetryCompletedSSE), telemetryErrorReader{}))}
	hook.Record(response, nil, 0)
	_, err := io.ReadFull(response.Body, make([]byte, len(telemetryCompletedSSE)))
	require.NoError(t, err)
	hook.Complete(false)
	require.Equal(t, "success", attempt.event(attempt.facts, 200, nil).Outcome)
	_, err = response.Body.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, "unknown", attempt.event(attempt.facts, 200, nil).Outcome)
	require.NoError(t, response.Body.Close())
}

func TestTelemetryResponseBoundsDoNotConsumeOrChangeBody(t *testing.T) {
	for _, tc := range []struct {
		media, body string
		want        string
	}{
		{"application/json", `{"status":"completed","output":"` + strings.Repeat("x", maxTelemetryResponseBytes) + `"}`, "unknown"},
		{"text/event-stream", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + strings.Repeat("x", maxTelemetryResponseBytes) + "\"}\n\n" + telemetryCompletedSSE, "unknown"},
		{"text/event-stream", ": " + strings.Repeat("x", maxTelemetryResponseBytes) + "\n\n" + telemetryCompletedSSE, "unknown"},
		{"text/event-stream", strings.ReplaceAll(telemetryCompletedSSE, "\n", "\r\n"), "success"},
		{"text/event-stream", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"status=failed 敏感示例\"}\n\n" + telemetryCompletedSSE + "data: [DONE]\n\n", "success"},
	} {
		for _, chunk := range []int{1, 37, 4096} {
			ctx, attempt := telemetryTestAttempt(t)
			hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
			spy := &telemetryReadSpy{reader: strings.NewReader(tc.body), maxChunk: chunk}
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.media}}, Body: spy}
			hook.Record(response, nil, 0)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, tc.body, string(body))
			hook.Complete(false)
			require.Equal(t, tc.want, attempt.event(attempt.facts, 200, nil).Outcome)
			wrapped := telemetryTestBody(t, response)
			require.LessOrEqual(t, cap(wrapped.pending), maxTelemetryResponseBytes)
			require.NoError(t, response.Body.Close())
			require.Nil(t, wrapped.pending)
			require.Equal(t, 1, spy.closes)
		}
	}
	// Include a large ignored output field without retaining its raw bytes.
	ctx, attempt := telemetryTestAttempt(t)
	hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
	body := []byte(`{"status":"completed","output":"sensitive-output"}`)
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
	hook.Record(response, nil, 0)
	_, err := io.ReadFull(response.Body, make([]byte, len(body)))
	require.NoError(t, err)
	scratch := telemetryTestBody(t, response).pending
	require.NoError(t, response.Body.Close())
	require.Equal(t, make([]byte, len(scratch)), scratch, "Close scrubs unparsed raw scratch")
	require.False(t, attempt.facts.responseCompleted)
}

type telemetryCloseUnblocksReader struct {
	first                *strings.Reader
	started, closed      chan struct{}
	startOnce, closeOnce sync.Once
}

func (r *telemetryCloseUnblocksReader) Read(raw []byte) (int, error) {
	if r.first.Len() > 0 {
		return r.first.Read(raw)
	}
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, errors.New("native close unblocked upstream read")
}

func (r *telemetryCloseUnblocksReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func TestTelemetryNativeSSECloseDoesNotReclassifyCompletedResponse(t *testing.T) {
	ctx, attempt := telemetryTestAttempt(t)
	hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
	reader := &telemetryCloseUnblocksReader{first: strings.NewReader(telemetryCompletedSSE), started: make(chan struct{}), closed: make(chan struct{})}
	t.Cleanup(func() { _ = reader.Close() })
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}
	hook.Record(response, nil, 0)
	_, err := io.ReadFull(response.Body, make([]byte, len(telemetryCompletedSSE)))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := response.Body.Read(make([]byte, 1)); done <- err }()
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("upstream read did not start")
	}
	require.NoError(t, response.Body.Close())
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("native close did not unblock upstream read")
	}
	hook.Complete(false)
	require.Equal(t, "success", attempt.event(attempt.facts, 200, nil).Outcome)
	require.False(t, attempt.facts.readFailed)
}

func TestTelemetryResponseLimitBoundaries(t *testing.T) {
	jsonBody := `{"status":"completed"}`
	jsonBody += strings.Repeat(" ", maxTelemetryResponseBytes-len(jsonBody))
	for _, tc := range []struct{ media, body, want string }{
		{"application/json", jsonBody, "success"},
		{"application/json", jsonBody + " ", "unknown"},
		{"text/event-stream", strings.Repeat(": keepalive\n\n", maxTelemetryStreamBytes/13) + telemetryCompletedSSE, "unknown"},
	} {
		ctx, attempt := telemetryTestAttempt(t)
		hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
		reader := &telemetryReadSpy{reader: strings.NewReader(tc.body), maxChunk: 8192}
		response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.media}}, Body: reader}
		hook.Record(response, nil, 0)
		n, err := io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.Equal(t, int64(len(tc.body)), n)
		hook.Complete(false)
		require.NoError(t, response.Body.Close())
		require.Equal(t, tc.want, attempt.event(attempt.facts, 200, nil).Outcome)
	}
}

func telemetryTestBody(t *testing.T, response *http.Response) *telemetryBody {
	t.Helper()
	body, ok := response.Body.(*telemetryBody)
	require.True(t, ok)
	return body
}

func telemetryTestAttempt(t *testing.T) (context.Context, *TelemetryAttempt) {
	t.Helper()
	cfg := telemetryTestConfig(t)
	producer := &TelemetryProducer{source: TelemetrySource{CoreInstance: cfg.CoreInstance, ConfigRevision: cfg.ConfigRevision, ProducerGeneration: "generation"}, targets: map[telemetryRoute]TelemetryTarget{cfg.Targets[0].route(): cfg.Targets[0]}}
	target := cfg.Targets[0]
	envelope := ForwardEnvelope{AccountID: target.AccountID, PoolRef: target.PoolRef, PolicyRevision: target.PolicyRevision, CanonicalModel: target.Model, InternalKeyID: 12, AttemptID: "attempt", ReservationID: "reservation"}
	return producer.Begin(context.Background(), envelope, "core-request")
}

func TestTelemetryCapturesOnlyTrustedOutboundFacts(t *testing.T) {
	for _, tc := range []struct {
		name                                                                    string
		status                                                                  int
		transport, readErr, complete, canceled, badCreds, badAccount, duplicate bool
		want                                                                    string
		evidence                                                                bool
	}{
		{name: "provider_auth", status: 401, want: "upstream_auth", evidence: true},
		{name: "provider_rate", status: 429, want: "upstream_rate_limit", evidence: true},
		{name: "provider_failure", status: 503, want: "upstream_failure", evidence: true},
		{name: "provider_other", status: 403, want: "unknown"},
		{name: "native_complete_candidate", status: 200, complete: true, want: "success", evidence: true},
		{name: "truncated_stream", status: 200, want: "unknown"},
		{name: "read_error", status: 200, complete: true, readErr: true, want: "unknown"},
		{name: "provider_error_truncated", status: 503, readErr: true, want: "unknown"},
		{name: "transport_response_not_evidence", status: 503, transport: true, want: "unknown"},
		{name: "credential_changed", status: 401, badCreds: true, want: "unknown"},
		{name: "account_changed", status: 401, badAccount: true, want: "unknown"},
		{name: "client_canceled_overrides_5xx", status: 503, canceled: true, want: "client_canceled"},
		{name: "duplicate_send_is_unknown", status: 401, duplicate: true, want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, attempt := telemetryTestAttempt(t)
			credentials := map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"}
			if tc.badCreds {
				credentials["api_key"] = "rotated"
			}
			accountID := int64(42)
			if tc.badAccount {
				accountID++
			}
			hook := PrepareTelemetryHTTP(ctx, accountID, credentials)
			// Hashing must freeze the identity before a later account mutation.
			credentials["api_key"] = "mutated-after-prepare"
			response := &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": {"120"}, "Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}`))}
			if tc.readErr {
				response.Body = telemetryErrorReader{}
			}
			var transportErr error
			if tc.transport {
				transportErr = errors.New("sensitive-transport-error")
			}
			hook.Record(response, transportErr, 20*time.Millisecond)
			if tc.duplicate {
				hook.Record(response, nil, time.Millisecond)
			}
			_, readErr := io.ReadAll(response.Body)
			if tc.readErr {
				require.ErrorIs(t, readErr, io.ErrUnexpectedEOF)
			} else {
				require.NoError(t, readErr)
			}
			require.NoError(t, response.Body.Close())
			if tc.complete {
				hook.Complete(tc.canceled)
			}
			var clientErr error
			if tc.canceled {
				clientErr = context.Canceled
			}
			event := attempt.event(attempt.facts, 200, clientErr)
			require.Equal(t, tc.want, event.Outcome)
			require.Equal(t, tc.evidence, event.ProviderEvidence)
			if event.Outcome == "upstream_rate_limit" {
				require.Equal(t, 120, event.RetryAfterSeconds)
			} else {
				require.Zero(t, event.RetryAfterSeconds)
			}
			raw, err := json.Marshal(event)
			require.NoError(t, err)
			for _, secret := range []string{"synthetic-test-key", "mutated-after-prepare", "sensitive-transport-error", "base_url", "Retry-After"} {
				require.NotContains(t, string(raw), secret)
			}
		})
	}
	ctx, attempt := telemetryTestAttempt(t)
	require.Equal(t, "local_rejection", attempt.event(attempt.facts, 401, nil).Outcome)
	require.Equal(t, "local_rejection", attempt.event(attempt.facts, 503, nil).Outcome)
	require.Equal(t, "unknown", attempt.event(attempt.facts, 200, nil).Outcome)
	require.Nil(t, PrepareTelemetryHTTP(context.Background(), 42, nil))
	var disabled *TelemetryHTTP
	disabled.Record(nil, nil, 0)
	disabled.Complete(false)
	var producer *TelemetryProducer
	unchanged, missing := producer.Begin(ctx, ForwardEnvelope{}, "")
	require.Equal(t, ctx, unchanged)
	require.Nil(t, missing)
	_, missing = attempt.producer.Begin(ctx, ForwardEnvelope{AccountID: 43}, "other")
	require.Nil(t, missing)
}

type telemetryErrorReader struct{}

func (telemetryErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (telemetryErrorReader) Close() error             { return nil }

func TestTelemetryReadFailureAndConcurrentCallbacks(t *testing.T) {
	ctx, attempt := telemetryTestAttempt(t)
	hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
	response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: telemetryErrorReader{}}
	hook.Record(response, nil, time.Millisecond)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, _ = response.Body.Read(make([]byte, 4)); hook.Complete(false) })
	}
	wg.Wait()
	require.True(t, attempt.facts.readFailed)
	require.Equal(t, "unknown", attempt.event(attempt.facts, 200, nil).Outcome)
	attempt.mu.Lock()
	attempt.finished = true
	attempt.mu.Unlock()
	hook.Record(response, nil, 0)
	hook.Complete(true)
	require.Equal(t, 1, attempt.facts.count)
	require.False(t, attempt.facts.clientCanceled)
}

func TestTelemetryRetryAfterIsBounded(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for value, want := range map[string]int{"120": 120, "999999": 600, "-1": 0, "nonsense": 0, "9223372036854775808": 0, strings.Repeat("1", 129): 0, now.Add(2 * time.Minute).UTC().Format(http.TimeFormat): 120, now.Add(-time.Minute).UTC().Format(http.TimeFormat): 0} {
		require.Equal(t, want, telemetryRetryAfter(value, now), value)
	}
}

type telemetryStoredOutcome struct{ value *string }

func (o telemetryStoredOutcome) Match(value driver.Value) bool {
	*o.value, _ = value.(string)
	return true
}

// Force the native nil-return marker and a committed receipt even for mutations
// of an otherwise successful response. The raw upstream terminal must still win.
func TestTelemetryResponseTerminalMutation(t *testing.T) {
	for _, tc := range []struct{ name, mediaType, body, want string }{
		{"json_completed", "application/json", `{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}`, "success"},
		{"json_failed", "application/json", `{"status":"failed","usage":{"input_tokens":1,"output_tokens":1}}`, "unknown"},
		{"json_incomplete", "application/json", `{"status":"incomplete","usage":{"input_tokens":1,"output_tokens":1}}`, "unknown"},
		{"json_missing_status", "application/json", `{"usage":{"input_tokens":1,"output_tokens":1}}`, "unknown"},
		{"json_duplicate_status", "application/json", `{"status":"completed","status":"failed","usage":{"input_tokens":1}}`, "unknown"},
		{"json_duplicate_completed", "application/json", `{"status":"completed","status":"completed","usage":{"input_tokens":1}}`, "unknown"},
		{"json_case_alias", "application/json", `{"status":"completed","Status":"failed","usage":{"input_tokens":1}}`, "unknown"},
		{"json_unicode_case_alias", "application/json", `{"status":"completed","\u017Ftatus":"failed","usage":{"input_tokens":1}}`, "unknown"},
		{"json_escaped_duplicate", "application/json", `{"status":"completed","st\u0061tus":"failed","usage":{"input_tokens":1}}`, "unknown"},
		{"json_error", "application/json", `{"status":"completed","error":{"message":"sensitive-error"},"usage":{"input_tokens":1}}`, "unknown"},
		{"json_incomplete_details", "application/json", `{"status":"completed","incomplete_details":{"reason":"limit"},"usage":{"input_tokens":1}}`, "unknown"},
		{"json_null_details", "application/json; charset=utf-8", `{"status":"completed","error":null,"incomplete_details":null,"usage":{"input_tokens":1}}`, "success"},
		{"json_error_alias", "application/json", `{"status":"completed","Error":null,"usage":{"input_tokens":1}}`, "unknown"},
		{"json_details_alias", "application/json", `{"status":"completed","Incomplete_Details":null,"usage":{"input_tokens":1}}`, "unknown"},
		{"json_truncated", "application/json", `{"status":"completed","usage":{"input_tokens":1}`, "unknown"},
		{"json_trailing", "application/json", `{"status":"completed"} {"status":"failed"}`, "unknown"},
		{"json_nested_status", "application/json", `{"response":{"status":"completed"},"usage":{"input_tokens":1}}`, "unknown"},
		{"json_untrusted_media", "text/plain", `{"status":"completed","usage":{"input_tokens":1}}`, "unknown"},
		{"json_invalid_utf8", "application/json", "{\"status\":\"completed\",\"output\":\"\xff\",\"usage\":{\"input_tokens\":1}}", "unknown"},
		{"sse_completed", "text/event-stream", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n", "success"},
		{"sse_failed", "text/event-stream", "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n", "unknown"},
		{"sse_completed_failed_status", "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"failed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n", "unknown"},
		{"sse_incomplete", "text/event-stream", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"usage\":{\"input_tokens\":1}}}\n\n", "unknown"},
		{"sse_event_type_conflict", "text/event-stream", "event: response.created\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1}}}\n\n", "unknown"},
		{"sse_unknown_terminal", "text/event-stream", "data: {\"type\":\"response.done\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1}}}\n\n", "unknown"},
		{"sse_status_alias", "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"Status\":\"failed\",\"usage\":{\"input_tokens\":1}}}\n\n", "unknown"},
		{"sse_nonnull_error", "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"error\":{\"message\":\"sensitive-error\"},\"usage\":{\"input_tokens\":1}}}\n\n", "unknown"},
		{"sse_no_status", "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1}}}\n\n", "unknown"},
		{"sse_truncated_frame", "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, attempt := telemetryTestAttempt(t)
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			attempt.producer.db = db
			hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.mediaType}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			hook.Record(response, nil, time.Millisecond)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, tc.body, string(body))
			require.NoError(t, response.Body.Close())
			hook.Complete(false)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT singleton FROM bridge.dispatch_telemetry_source").WillReturnRows(sqlmock.NewRows([]string{"singleton"}).AddRow(true))
			if attempt.event(attempt.facts, 200, nil).Outcome == "success" {
				mock.ExpectQuery("SELECT EXISTS .*bridge.receipt_outbox").WillReturnRows(sqlmock.NewRows([]string{"committed"}).AddRow(true))
			}
			var outcome string
			args := make([]driver.Value, 27)
			for i := range args {
				args[i] = sqlmock.AnyArg()
			}
			args[12] = telemetryStoredOutcome{&outcome}
			args[22] = ""
			args[23] = false
			mock.ExpectExec("INSERT INTO bridge.dispatch_observation").WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			require.NoError(t, attempt.Finish(ctx, 200, nil))
			require.NoError(t, mock.ExpectationsWereMet())
			require.Equal(t, tc.want, outcome)
		})
	}
}
