//go:build unit

package custombridge

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

const telemetryTextDelta = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"你好\"}\n\n"

func telemetryTTFTResponse(t *testing.T, body io.ReadCloser, media string, headerDuration time.Duration) (*TelemetryAttempt, *TelemetryHTTP, *http.Response) {
	t.Helper()
	ctx, attempt := telemetryTestAttempt(t)
	hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {media}}, Body: body}
	hook.Record(response, nil, headerDuration)
	t.Cleanup(func() { _ = response.Body.Close() })
	return attempt, hook, response
}

func TestTelemetryTTFTWaitsForCompleteMeaningfulFrame(t *testing.T) {
	attempt, hook, response := telemetryTTFTResponse(t, io.NopCloser(strings.NewReader("")), "text/event-stream", 25*time.Millisecond)
	body := telemetryTestBody(t, response)
	start := time.Now().Add(-time.Second)
	attempt.facts.recordedAt = start
	for _, frame := range []string{
		": heartbeat\n\n",
		"data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n",
		"data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"thinking\"}\n\n",
		"data: {\"type\":\"response.refusal.delta\",\"delta\":\"refusal\"}\n\n",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\" \\t\\n\\u3000\"}\n\n",
		"data: {\"type\":\"response.output_text.done\",\"text\":\"full text\"}\n\n",
	} {
		body.observe([]byte(frame), false, start.Add(10*time.Millisecond))
		require.Zero(t, attempt.facts.ttft, frame)
		require.False(t, body.firstOutput, frame)
	}
	split := strings.Index(telemetryTextDelta, "你好") + 1
	body.observe([]byte(telemetryTextDelta[:split]), false, start.Add(20*time.Millisecond))
	body.observe([]byte(telemetryTextDelta[split:len(telemetryTextDelta)-1]), false, start.Add(30*time.Millisecond))
	require.Zero(t, attempt.facts.ttft, "a complete JSON line still lacks the frame delimiter")
	body.observe([]byte("\n"), false, start.Add(40*time.Millisecond))
	require.Equal(t, int64(65), attempt.facts.ttft)
	require.Zero(t, attempt.event(attempt.facts, 200, nil).TTFTMillis, "native and upstream completion are still required")
	body.observe([]byte(telemetryTextDelta), false, start.Add(80*time.Millisecond))
	require.Equal(t, int64(65), attempt.facts.ttft, "later text must not move the first frame time")
	body.observe([]byte(telemetryCompletedSSE), false, start.Add(100*time.Millisecond))
	hook.Complete(false)
	event := attempt.event(attempt.facts, 200, nil)
	require.Equal(t, "success", event.Outcome)
	require.Equal(t, int64(65), event.TTFTMillis)
	require.GreaterOrEqual(t, event.LatencyMillis, event.TTFTMillis)
}

func TestTelemetryTTFTSplitReadsPreserveNativeBody(t *testing.T) {
	for _, chunk := range []int{1, 2, 7, 31, 4096} {
		t.Run(strconv.Itoa(chunk), func(t *testing.T) {
			data := ": heartbeat\r\n\r\n" + strings.ReplaceAll(telemetryTextDelta+telemetryCompletedSSE, "\n", "\r\n")
			spy := &telemetryReadSpy{reader: strings.NewReader(data), maxChunk: chunk, noEOF: true}
			attempt, hook, response := telemetryTTFTResponse(t, spy, "text/event-stream; charset=utf-8", 25*time.Millisecond)
			got := make([]byte, len(data))
			_, err := io.ReadFull(response.Body, got)
			require.NoError(t, err)
			require.Equal(t, data, string(got))
			hook.Complete(false)
			event := attempt.event(attempt.facts, 200, nil)
			require.Equal(t, "success", event.Outcome)
			require.GreaterOrEqual(t, event.TTFTMillis, int64(25))
			require.LessOrEqual(t, event.TTFTMillis, event.LatencyMillis)
			require.Equal(t, (len(data)+chunk-1)/chunk, spy.reads, "observer must not read ahead")
		})
	}
}

func TestTelemetryTTFTQuantizationAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		header, first time.Duration
		want          int64
	}{
		{"sub_millisecond", 200 * time.Microsecond, 300 * time.Microsecond, 0},
		{"fractional_header", 750 * time.Microsecond, 750 * time.Microsecond, 1},
		{"fractional_total", 1750 * time.Microsecond, 750 * time.Microsecond, 2},
		{"upper_bound", 20 * time.Minute, time.Second, 900000},
		{"negative_header", -time.Second, 2 * time.Millisecond, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt, hook, response := telemetryTTFTResponse(t, io.NopCloser(strings.NewReader("")), "text/event-stream", tc.header)
			body := telemetryTestBody(t, response)
			start := time.Now().Add(-time.Second)
			attempt.facts.recordedAt = start
			body.observe([]byte(telemetryTextDelta), false, start.Add(tc.first))
			require.Equal(t, tc.want, attempt.facts.ttft)
			body.observe([]byte(telemetryTextDelta+telemetryCompletedSSE), false, start.Add(time.Second))
			hook.Complete(false)
			event := attempt.event(attempt.facts, 200, nil)
			require.Equal(t, "success", event.Outcome)
			require.Equal(t, tc.want, event.TTFTMillis, "even a rounded-zero first frame is fixed")
			require.LessOrEqual(t, event.TTFTMillis, event.LatencyMillis)
		})
	}
	for range 100 {
		attempt, hook, response := telemetryTTFTResponse(t, io.NopCloser(strings.NewReader(telemetryTextDelta+telemetryCompletedSSE)), "text/event-stream", 750*time.Microsecond)
		_, err := io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		hook.Complete(false)
		event := attempt.event(attempt.facts, 200, nil)
		require.Equal(t, "success", event.Outcome)
		require.LessOrEqual(t, event.TTFTMillis, event.LatencyMillis)
	}
}

func TestTelemetryTTFTMalformedOrOversizeRevokesEarlyTime(t *testing.T) {
	for _, tc := range []struct{ name, suffix string }{
		{"duplicate_delta", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\",\"delta\":\"y\"}\n\n"},
		{"case_delta", "data: {\"type\":\"response.output_text.delta\",\"Delta\":\"x\"}\n\n"},
		{"escaped_delta", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\",\"delt\\u0061\":\"y\"}\n\n"},
		{"duplicate_type", "data: {\"type\":\"response.output_text.delta\",\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"},
		{"case_type", "data: {\"Type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"},
		{"missing_delta", "data: {\"type\":\"response.output_text.delta\"}\n\n"},
		{"null_delta", "data: {\"type\":\"response.output_text.delta\",\"delta\":null}\n\n"},
		{"object_delta", "data: {\"type\":\"response.output_text.delta\",\"delta\":{}}\n\n"},
		{"conflicting_event", "event: response.created\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"},
		{"duplicate_data", strings.TrimSuffix(telemetryTextDelta, "\n") + "data: {}\n\n"},
		{"unknown_event", "data: {\"type\":\"response.failed\"}\n\n"},
		{"invalid_json", "data: {\n\n"},
		{"invalid_utf8", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"\xff\"}\n\n"},
		{"oversize_line", "data: " + strings.Repeat("x", maxTelemetryResponseBytes) + "\n\n"},
		{"oversize_stream", strings.Repeat(": heartbeat\n\n", maxTelemetryStreamBytes/13+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt, hook, response := telemetryTTFTResponse(t, io.NopCloser(strings.NewReader(telemetryTextDelta+tc.suffix+telemetryCompletedSSE)), "text/event-stream", 25*time.Millisecond)
			_, err := io.ReadFull(response.Body, make([]byte, len(telemetryTextDelta)))
			require.NoError(t, err)
			require.Positive(t, attempt.facts.ttft)
			got, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, tc.suffix+telemetryCompletedSSE, string(got))
			hook.Complete(false)
			require.Zero(t, attempt.facts.ttft)
			event := attempt.event(attempt.facts, 200, nil)
			require.Equal(t, "unknown", event.Outcome)
			require.Zero(t, event.TTFTMillis)
			require.Nil(t, telemetryTestBody(t, response).pending)
		})
	}
}

func TestTelemetryTTFTRequiresSuccessfulNativeCompletion(t *testing.T) {
	for _, name := range []string{"read_failure", "truncated_delta", "truncated_completion", "partial_after_completion", "cancel_callback", "cancel_error", "no_native_completion", "multiple_outbound", "local_failure"} {
		t.Run(name, func(t *testing.T) {
			data := telemetryTextDelta + telemetryCompletedSSE
			switch name {
			case "truncated_delta":
				data = strings.TrimSuffix(telemetryTextDelta, "\n")
			case "truncated_completion":
				data = telemetryTextDelta + strings.TrimSuffix(telemetryCompletedSSE, "\n")
			case "partial_after_completion":
				data += "data: {"
			}
			reader := io.NopCloser(strings.NewReader(data))
			if name == "read_failure" {
				reader = io.NopCloser(io.MultiReader(strings.NewReader(data), telemetryErrorReader{}))
			}
			attempt, hook, response := telemetryTTFTResponse(t, reader, "text/event-stream", 25*time.Millisecond)
			_, err := io.Copy(io.Discard, response.Body)
			if name == "read_failure" {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				require.Zero(t, attempt.facts.ttft)
			} else {
				require.NoError(t, err)
			}
			if name != "no_native_completion" {
				hook.Complete(name == "cancel_callback")
			}
			if name == "cancel_callback" {
				hook.Complete(false)
			}
			if name == "multiple_outbound" {
				hook.Record(nil, nil, 0)
			}
			status := 200
			if name == "local_failure" {
				status = 503
			}
			var clientErr error
			if name == "cancel_error" {
				clientErr = context.Canceled
			}
			event := attempt.event(attempt.facts, status, clientErr)
			require.NotEqual(t, "success", event.Outcome)
			require.Zero(t, event.TTFTMillis)
		})
	}
}

func TestTelemetryTTFTWithoutOutputTextRemainsZero(t *testing.T) {
	for _, tc := range []struct{ media, data string }{
		{"application/json", `{"status":"completed","output":[{"text":"full output"}]}`},
		{"text/event-stream", ": heartbeat\n\n" + telemetryCompletedSSE},
		{"text/event-stream", "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"reasoning\"}\n\n" + telemetryCompletedSSE},
		{"text/event-stream", "data: {\"type\":\"response.refusal.delta\",\"delta\":\"refusal\"}\n\n" + telemetryCompletedSSE},
	} {
		attempt, hook, response := telemetryTTFTResponse(t, io.NopCloser(strings.NewReader(tc.data)), tc.media, 25*time.Millisecond)
		_, err := io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		hook.Complete(false)
		event := attempt.event(attempt.facts, 200, nil)
		require.Equal(t, "success", event.Outcome)
		require.Zero(t, event.TTFTMillis)
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "ttft_millis")
	}
}

func TestTelemetryTTFTReceiptGateClearsCandidate(t *testing.T) {
	for _, committed := range []bool{false, true} {
		attempt, hook, response := telemetryTTFTResponse(t, io.NopCloser(strings.NewReader(telemetryTextDelta+telemetryCompletedSSE)), "text/event-stream", 25*time.Millisecond)
		_, err := io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		hook.Complete(false)
		candidate := attempt.event(attempt.facts, 200, nil)
		require.Equal(t, "success", candidate.Outcome)
		require.Positive(t, candidate.TTFTMillis)
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		attempt.producer.db = db
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT singleton FROM bridge.dispatch_telemetry_source").WillReturnRows(sqlmock.NewRows([]string{"singleton"}).AddRow(true))
		mock.ExpectQuery("SELECT EXISTS .*bridge.receipt_outbox").WillReturnRows(sqlmock.NewRows([]string{"committed"}).AddRow(committed))
		args := make([]driver.Value, 27)
		for i := range args {
			args[i] = sqlmock.AnyArg()
		}
		args[12], args[13], args[21] = "success", true, candidate.TTFTMillis
		args[22] = ""
		if !committed {
			args[12], args[13], args[21] = "unknown", false, int64(0)
		}
		args[23] = false
		mock.ExpectExec("INSERT INTO bridge.dispatch_observation").WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for range 8 {
			wg.Go(func() { hook.Complete(false); results <- attempt.Finish(context.Background(), 200, nil) })
		}
		wg.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err)
		}
		require.NoError(t, mock.ExpectationsWereMet())
	}
}
