//go:build unit

package custombridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type protocolTimeoutError struct{}

func (protocolTimeoutError) Error() string   { return "sensitive-timeout-detail" }
func (protocolTimeoutError) Timeout() bool   { return true }
func (protocolTimeoutError) Temporary() bool { return true }

type protocolReader struct {
	reader *strings.Reader
	err    error
}

func (r *protocolReader) Read(raw []byte) (int, error) {
	if r.reader.Len() == 0 {
		return 0, r.err
	}
	return r.reader.Read(raw)
}

func (r *protocolReader) Close() error { return nil }

const (
	protocolCompletedNoUsageSSE = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"error\":null,\"incomplete_details\":null}}\n\n"
	protocolFailedSSE           = "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"context_length_exceeded\",\"message\":\"sensitive-raw-message\"}}}\n\n"
	protocolErrorSSE            = "event: error\ndata: {\"type\":\"error\",\"code\":\"content_policy\",\"message\":\"sensitive-raw-message\"}\n\n"
	protocolIncompleteSSE       = "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"
	protocolRefusalDelta        = "data: {\"type\":\"response.refusal.delta\",\"delta\":\"sensitive-refusal\"}\n\n"
)

func TestTelemetryProtocolFactsClassifySingleOutbound(t *testing.T) {
	for _, tc := range []struct {
		name, media, body string
		status            int
		readErr           error
		outcome, phase    string
		fact              string
		usageMissing      bool
		ttft              bool
	}{
		{name: "text_completed", media: "text/event-stream", body: telemetryTextDelta + telemetryCompletedSSE, status: 200, outcome: "success", phase: "terminal", ttft: true},
		{name: "text_completed_without_usage", media: "text/event-stream", body: telemetryTextDelta + protocolCompletedNoUsageSSE, status: 200, outcome: "success", phase: "terminal", usageMissing: true, ttft: true},
		{name: "refusal_is_non_text_success", media: "text/event-stream", body: protocolRefusalDelta + telemetryCompletedSSE, status: 200, outcome: "success", phase: "terminal", fact: "non_text_terminal"},
		{name: "heartbeat_only_non_text_without_usage", media: "text/event-stream", body: ": heartbeat\n\n" + protocolCompletedNoUsageSSE, status: 200, outcome: "success", phase: "terminal", fact: "non_text_terminal", usageMissing: true},
		{name: "sse_failed_after_content", media: "text/event-stream", body: telemetryTextDelta + protocolFailedSSE, status: 200, outcome: "unknown", phase: "terminal", fact: "semantic_failure"},
		{name: "sse_error_event", media: "text/event-stream", body: protocolErrorSSE, status: 200, outcome: "unknown", phase: "terminal", fact: "semantic_failure"},
		{name: "sse_incomplete", media: "text/event-stream", body: protocolIncompleteSSE, status: 200, outcome: "unknown", phase: "terminal", fact: "incomplete"},
		{name: "frame_after_completion_is_unknown", media: "text/event-stream", body: telemetryCompletedSSE + protocolFailedSSE, status: 200, outcome: "unknown", phase: "terminal"},
		{name: "mismatched_failure_event_is_not_fact", media: "text/event-stream", body: "event: response.incomplete\n" + strings.TrimPrefix(protocolFailedSSE, "event: response.failed\n"), status: 200, outcome: "unknown", phase: "headers"},
		{name: "completion_after_failure_ignored", media: "text/event-stream", body: protocolFailedSSE + telemetryCompletedSSE, status: 200, outcome: "unknown", phase: "terminal", fact: "semantic_failure"},
		{name: "json_failed", media: "application/json", body: `{"status":"failed","error":{"code":"server_error","message":"sensitive-raw-message"}}`, status: 200, outcome: "unknown", phase: "terminal", fact: "semantic_failure"},
		{name: "json_incomplete", media: "application/json", body: `{"status":"incomplete","incomplete_details":{"reason":"content_filter"}}`, status: 200, outcome: "unknown", phase: "terminal", fact: "incomplete"},
		{name: "json_status_alias_is_not_fact", media: "application/json", body: `{"Status":"failed"}`, status: 200, outcome: "unknown", phase: "headers"},
		{name: "json_completed_without_usage", media: "application/json", body: `{"status":"completed"}`, status: 200, outcome: "success", phase: "terminal", usageMissing: true},
		{name: "json_completed_with_usage", media: "application/json", body: `{"status":"completed","usage":{"input_tokens":1}}`, status: 200, outcome: "success", phase: "terminal"},
		{name: "eof_before_content", media: "text/event-stream", body: ": heartbeat\n\n", status: 200, outcome: "unknown", phase: "headers", fact: "stream_break"},
		{name: "eof_after_content", media: "text/event-stream", body: telemetryTextDelta, status: 200, outcome: "unknown", phase: "content", fact: "stream_break"},
		{name: "eof_inside_frame", media: "text/event-stream", body: telemetryTextDelta + "data: {\"type\":", status: 200, outcome: "unknown", phase: "content", fact: "stream_break"},
		{name: "read_error_before_content", media: "text/event-stream", body: ": heartbeat\n\n", readErr: io.ErrUnexpectedEOF, status: 200, outcome: "unknown", phase: "headers", fact: "stream_break"},
		{name: "read_error_after_content", media: "text/event-stream", body: telemetryTextDelta, readErr: io.ErrUnexpectedEOF, status: 200, outcome: "unknown", phase: "content", fact: "stream_break"},
		{name: "read_timeout_before_content", media: "text/event-stream", body: "", readErr: protocolTimeoutError{}, status: 200, outcome: "unknown", phase: "headers", fact: "local_timeout"},
		{name: "read_timeout_after_content", media: "text/event-stream", body: telemetryTextDelta, readErr: protocolTimeoutError{}, status: 200, outcome: "unknown", phase: "content", fact: "local_timeout"},
		{name: "read_deadline_after_content", media: "text/event-stream", body: telemetryTextDelta, readErr: context.DeadlineExceeded, status: 200, outcome: "unknown", phase: "content", fact: "local_timeout"},
		{name: "read_error_after_abandoned_observation", media: "text/event-stream", body: "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"x\"}\n\n", readErr: io.ErrUnexpectedEOF, status: 200, outcome: "unknown", phase: "headers"},
		{name: "read_error_after_completion", media: "text/event-stream", body: telemetryTextDelta + telemetryCompletedSSE, readErr: protocolTimeoutError{}, status: 200, outcome: "unknown", phase: "terminal"},
		{name: "json_read_error", media: "application/json", body: `{"status":`, readErr: io.ErrUnexpectedEOF, status: 200, outcome: "unknown", phase: "headers", fact: "stream_break"},
		{name: "provider_failure_sse_not_observed", media: "text/event-stream", body: ": x\n\n", status: 503, outcome: "upstream_failure", phase: "headers"},
		{name: "provider_failure_read_error_not_break", media: "application/json", body: `{"error":`, readErr: protocolTimeoutError{}, status: 503, outcome: "unknown", phase: "headers"},
		{name: "provider_rate_limit", media: "application/json", body: `{"status":"failed"}`, status: 429, outcome: "upstream_rate_limit", phase: "headers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, attempt := telemetryTestAttempt(t)
			hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
			readErr := tc.readErr
			if readErr == nil {
				readErr = io.EOF
			}
			response := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.media}}, Body: &protocolReader{reader: strings.NewReader(tc.body), err: readErr}}
			hook.Record(response, nil, 25*time.Millisecond)
			_, err := io.Copy(io.Discard, response.Body)
			if tc.readErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.readErr)
			}
			require.NoError(t, response.Body.Close())
			hook.Complete(false)
			event := attempt.event(attempt.facts, tc.status, nil)
			require.Equal(t, tc.outcome, event.Outcome)
			require.Equal(t, tc.phase, event.Phase)
			require.Equal(t, tc.fact, event.ProtocolFact)
			require.Equal(t, tc.usageMissing, event.UsageMissing)
			require.Equal(t, tc.ttft, event.TTFTMillis > 0)
			raw, err := json.Marshal(event)
			require.NoError(t, err)
			for _, secret := range []string{"sensitive", "context_length", "content_policy", "server_error", "max_output_tokens", "content_filter", "synthetic-test-key"} {
				require.NotContains(t, string(raw), secret)
			}
		})
	}
}

type protocolJointReader struct {
	data []byte
	err  error
}

func (r *protocolJointReader) Read(raw []byte) (int, error) {
	n := copy(raw, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func (r *protocolJointReader) Close() error { return nil }

func TestTelemetryProtocolFactsIgnoreReadChunking(t *testing.T) {
	unsupported := "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"x\"}\n\n"
	for _, tc := range []struct {
		name, media, body string
		err               error
	}{
		{"text_and_timeout", "text/event-stream", telemetryTextDelta, protocolTimeoutError{}},
		{"text_and_unexpected_eof", "text/event-stream", telemetryTextDelta, io.ErrUnexpectedEOF},
		{"failed_terminal_and_read_error", "text/event-stream", telemetryTextDelta + protocolFailedSSE, io.ErrUnexpectedEOF},
		{"abandoned_parse_and_read_error", "text/event-stream", unsupported, io.ErrUnexpectedEOF},
		{"completed_and_timeout", "text/event-stream", telemetryTextDelta + telemetryCompletedSSE, protocolTimeoutError{}},
		{"completed_and_eof", "text/event-stream", telemetryTextDelta + telemetryCompletedSSE, io.EOF},
		{"json_failed_and_read_error", "application/json", `{"status":"failed"}`, io.ErrUnexpectedEOF},
		{"json_partial_and_read_error", "application/json", `{"status":`, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observe := func(body io.ReadCloser) TelemetryEvent {
				ctx, attempt := telemetryTestAttempt(t)
				hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
				response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.media}}, Body: body}
				hook.Record(response, nil, 25*time.Millisecond)
				_, _ = io.Copy(io.Discard, response.Body)
				require.NoError(t, response.Body.Close())
				hook.Complete(false)
				return attempt.event(attempt.facts, 200, nil)
			}
			split := observe(&protocolReader{reader: strings.NewReader(tc.body), err: tc.err})
			joint := observe(&protocolJointReader{data: []byte(tc.body), err: tc.err})
			require.Equal(t, split.Outcome, joint.Outcome)
			require.Equal(t, split.Phase, joint.Phase)
			require.Equal(t, split.ProtocolFact, joint.ProtocolFact)
			require.Equal(t, split.UsageMissing, joint.UsageMissing)
			require.Equal(t, split.ProviderEvidence, joint.ProviderEvidence)
			require.Equal(t, split.TTFTMillis > 0, joint.TTFTMillis > 0)
		})
	}
}

func TestTelemetryProtocolFactsNeverAttributeLocalOrAmbiguousOutcomes(t *testing.T) {
	credentials := map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"}
	for _, tc := range []struct {
		name         string
		transportErr error
		badCreds     bool
		duplicate    bool
		clientErr    error
		canceled     bool
		phase, fact  string
		outcome      string
	}{
		{name: "connect_timeout", transportErr: protocolTimeoutError{}, phase: "connect", fact: "local_timeout", outcome: "unknown"},
		{name: "connect_deadline", transportErr: context.DeadlineExceeded, phase: "connect", fact: "local_timeout", outcome: "unknown"},
		{name: "connect_failure_unproven", transportErr: errors.New("sensitive-dial-error"), phase: "connect", outcome: "unknown"},
		{name: "credential_mismatch", transportErr: protocolTimeoutError{}, badCreds: true, outcome: "unknown"},
		{name: "multiple_outbound", transportErr: protocolTimeoutError{}, duplicate: true, outcome: "unknown"},
		{name: "bridge_deadline", transportErr: protocolTimeoutError{}, clientErr: context.DeadlineExceeded, phase: "connect", fact: "bridge_deadline", outcome: "client_canceled"},
		{name: "bridge_deadline_multiple_outbound", transportErr: protocolTimeoutError{}, duplicate: true, clientErr: context.DeadlineExceeded, fact: "bridge_deadline", outcome: "client_canceled"},
		{name: "client_cancel", transportErr: protocolTimeoutError{}, clientErr: context.Canceled, phase: "connect", outcome: "client_canceled"},
		{name: "native_client_disconnect", transportErr: protocolTimeoutError{}, canceled: true, phase: "connect", outcome: "client_canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, attempt := telemetryTestAttempt(t)
			creds := map[string]any{"api_key": credentials["api_key"], "base_url": credentials["base_url"]}
			if tc.badCreds {
				creds["api_key"] = "rotated"
			}
			hook := PrepareTelemetryHTTP(ctx, 42, creds)
			hook.Record(nil, tc.transportErr, time.Millisecond)
			if tc.duplicate {
				hook.Record(nil, tc.transportErr, time.Millisecond)
			}
			hook.Complete(tc.canceled)
			event := attempt.event(attempt.facts, 502, tc.clientErr)
			require.Equal(t, tc.outcome, event.Outcome)
			require.Equal(t, tc.phase, event.Phase)
			require.Equal(t, tc.fact, event.ProtocolFact)
			require.False(t, event.UsageMissing)
			require.False(t, event.ProviderEvidence)
			raw, err := json.Marshal(event)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "sensitive")
		})
	}
	ctx, attempt := telemetryTestAttempt(t)
	_ = ctx
	for _, status := range []int{401, 503, 200} {
		event := attempt.event(attempt.facts, status, nil)
		require.Empty(t, event.Phase, "no outbound has no phase")
		require.Empty(t, event.ProtocolFact)
	}
	event := attempt.event(attempt.facts, 504, context.DeadlineExceeded)
	require.Equal(t, "client_canceled", event.Outcome)
	require.Equal(t, "bridge_deadline", event.ProtocolFact)
	require.Empty(t, event.Phase)
}

func TestTelemetryProtocolFactsKeepLegacyJSONWhenAbsent(t *testing.T) {
	ctx, attempt := telemetryTestAttempt(t)
	_ = ctx
	raw, err := json.Marshal(attempt.event(attempt.facts, 401, nil))
	require.NoError(t, err)
	for _, field := range []string{"phase", "protocol_fact", "usage_missing"} {
		require.NotContains(t, string(raw), field)
	}
}
