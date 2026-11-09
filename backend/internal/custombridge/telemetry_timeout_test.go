//go:build unit

package custombridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTelemetryTimeoutUsesOnlyTrustedUpstreamStatus(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		status               int
		local, canceled      bool
		transport, badKey    bool
		multiple, readFailed bool
		outcome              string
		timedOut             bool
	}{
		{name: "provider504", status: 504, outcome: "upstream_failure", timedOut: true},
		{name: "provider503", status: 503, outcome: "upstream_failure"},
		{name: "provider429", status: 429, outcome: "upstream_rate_limit"},
		{name: "local504", status: 504, local: true, outcome: "local_rejection"},
		{name: "local_deadline", status: 504, transport: true, outcome: "unknown"},
		{name: "credential_mismatch", status: 504, badKey: true, outcome: "unknown"},
		{name: "multiple", status: 504, multiple: true, outcome: "unknown"},
		{name: "client_cancel", status: 504, canceled: true, outcome: "client_canceled"},
		{name: "read_failure", status: 504, readFailed: true, outcome: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, attempt := telemetryTestAttempt(t)
			credentials := map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"}
			if tc.badKey {
				credentials["api_key"] = "different-synthetic-key"
			}
			hook := PrepareTelemetryHTTP(ctx, 42, credentials)
			spy := &telemetryReadSpy{reader: strings.NewReader(`{"error":{"message":"not retained"}}`)}
			response := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: spy}
			if !tc.local {
				if tc.transport {
					hook.Record(nil, context.DeadlineExceeded, time.Millisecond)
				} else {
					hook.Record(response, nil, time.Millisecond)
				}
			}
			require.Zero(t, spy.reads, "the observer cannot add reads")
			if tc.readFailed {
				response.Body = newTelemetryBody(telemetryErrorReader{}, attempt, "application/json", false)
				_, err := io.ReadAll(response.Body)
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			}
			if tc.multiple {
				hook.Record(response, nil, time.Millisecond)
			}
			var clientErr error
			if tc.canceled {
				clientErr = context.Canceled
			}
			event := attempt.event(attempt.facts, tc.status, clientErr)
			require.Equal(t, tc.outcome, event.Outcome)
			require.Equal(t, tc.timedOut, event.TimedOut)
			if tc.timedOut {
				require.True(t, event.ProviderEvidence)
			} else {
				raw, err := json.Marshal(event)
				require.NoError(t, err)
				require.NotContains(t, string(raw), "timed_out", "false preserves legacy JSON payloads")
			}
			require.Zero(t, spy.reads)
			require.NoError(t, response.Body.Close())
		})
	}
}
