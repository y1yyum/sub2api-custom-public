//go:build unit

package custombridge

import (
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

func telemetryFailureAttempt(t *testing.T, status int, media string, reader io.ReadCloser, configured bool) (*TelemetryHTTP, *TelemetryAttempt, *http.Response) {
	t.Helper()
	ctx, attempt := telemetryTestAttempt(t)
	if configured {
		attempt.target.ErrorRules = telemetryTestErrorRules()
	}
	hook := PrepareTelemetryHTTP(ctx, 42, map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
	response := &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {media}, "Retry-After": {"120"}}, Body: reader}
	hook.Record(response, nil, time.Millisecond)
	return hook, attempt, response
}

func TestTelemetryFailureRequiresExactConfiguredJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body, class string
		status            int
	}{
		{"quota", `{"error":{"code":"operator-quota","type":"operator-account","message":"sensitive-raw-message"}}`, "quota_exhausted", 429},
		{"credential", `{"error":{"code":"operator-banned","type":"operator-account"}}`, "credential_banned", 403},
		{"capability", `{"code":"operator-model","message":"sensitive-raw-message"}`, "capability_denied", 403},
		{"root", ` { "type" : "operator-account", "code" : "operator-banned" } `, "credential_banned", 403},
		{"code_case", `{"error":{"code":"Operator-Banned","type":"operator-account"}}`, "", 403},
		{"type_case", `{"error":{"code":"operator-banned","type":"Operator-Account"}}`, "", 403},
		{"code_prefix", `{"error":{"code":"operator-banned-other","type":"operator-account"}}`, "", 403},
		{"missing_type", `{"error":{"code":"operator-banned"}}`, "", 403},
		{"wrong_status", `{"error":{"code":"operator-quota"}}`, "", 403},
		{"message_only", `{"error":{"message":"operator-banned operator-model quota exhausted"}}`, "", 403},
		{"unknown_code", `{"error":{"code":"quota_exhausted","message":"operator-quota"}}`, "", 429},
		{"bare403", `{}`, "", 403},
		{"array", `[{"code":"operator-model"}]`, "", 403},
		{"nested_error", `{"error":{"error":{"code":"operator-model"}}}`, "", 403},
		{"root_and_error", `{"code":"operator-model","error":{"code":"operator-model"}}`, "", 403},
		{"root_type_and_error", `{"type":"operator-account","error":{"code":"operator-model"}}`, "", 403},
		{"duplicate_root_error", `{"error":{"code":"operator-model"},"error":{"code":"operator-model"}}`, "", 403},
		{"duplicate_root_code", `{"code":"operator-model","code":"operator-model"}`, "", 403},
		{"duplicate_code", `{"error":{"code":"operator-model","code":"operator-model"}}`, "", 403},
		{"duplicate_type", `{"error":{"code":"operator-model","type":"x","type":"x"}}`, "", 403},
		{"code_alias", `{"error":{"Code":"operator-model"}}`, "", 403},
		{"code_escaped_alias", `{"error":{"c\u006fde":"operator-model"}}`, "", 403},
		{"type_alias", `{"error":{"code":"operator-model","Type":"x"}}`, "", 403},
		{"type_escaped_alias", `{"error":{"code":"operator-model","t\u0079pe":"x"}}`, "", 403},
		{"error_alias", `{"Error":{"code":"operator-model"}}`, "", 403},
		{"error_escaped_alias", `{"err\u006fr":{"code":"operator-model"}}`, "", 403},
		{"duplicate_escaped_alias", `{"error":{"code":"operator-model","c\u006fde":"operator-model"}}`, "", 403},
		{"root_null", `{"error":null}`, "", 403},
		{"null_code", `{"error":{"code":null}}`, "", 403},
		{"number_code", `{"error":{"code":123}}`, "", 403},
		{"null_type", `{"error":{"code":"operator-model","type":null}}`, "", 403},
		{"blank_type", `{"error":{"code":"operator-model","type":" "}}`, "", 403},
		{"trailing", `{"error":{"code":"operator-model"}} {}`, "", 403},
		{"truncated", `{"error":{"code":"operator-model"}`, "", 403},
		{"invalid_utf8", "{\"error\":{\"code\":\"operator-model\",\"message\":\"\xff\"}}", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, chunk := range []int{1, 7, 4096} {
				for _, configured := range []bool{false, true} {
					spy := &telemetryReadSpy{reader: strings.NewReader(tc.body), maxChunk: chunk}
					_, attempt, response := telemetryFailureAttempt(t, tc.status, "application/json; charset=utf-8", spy, configured)
					body, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.Equal(t, tc.body, string(body))
					require.NoError(t, response.Body.Close())
					event := attempt.event(attempt.facts, tc.status, nil)
					wantClass := ""
					if configured {
						wantClass = tc.class
					}
					require.Equal(t, wantClass, event.FailureClass)
					wantOutcome := "unknown"
					if tc.status == 429 {
						wantOutcome = "upstream_rate_limit"
					} else if wantClass != "" {
						wantOutcome = "upstream_auth"
					}
					require.Equal(t, wantOutcome, event.Outcome)
					require.Equal(t, wantOutcome != "unknown", event.ProviderEvidence)
					require.Zero(t, event.TTFTMillis)
					raw, err := json.Marshal(event)
					require.NoError(t, err)
					require.NotContains(t, string(raw), "sensitive-raw-message")
					require.NotContains(t, string(raw), "operator-model")
					require.NotContains(t, string(raw), "operator-banned")
					require.Nil(t, telemetryTestBody(t, response).pending)
				}
			}
		})
	}
}

func TestTelemetryFailureRequiresNativeEOFWithinBound(t *testing.T) {
	body := `{"error":{"code":"operator-model"}}`
	for _, size := range []int{len(body), maxTelemetryResponseBytes, maxTelemetryResponseBytes + 1} {
		for _, eof := range []bool{false, true} {
			padded := body + strings.Repeat(" ", size-len(body))
			spy := &telemetryReadSpy{reader: strings.NewReader(padded), noEOF: !eof}
			_, attempt, response := telemetryFailureAttempt(t, 403, "application/json", spy, true)
			_, err := io.ReadFull(response.Body, make([]byte, size))
			require.NoError(t, err)
			require.Equal(t, "", attempt.event(attempt.facts, 403, nil).FailureClass, "a valid prefix without EOF is not proof")
			wrapped := telemetryTestBody(t, response)
			scratch := wrapped.pending
			require.LessOrEqual(t, cap(scratch), maxTelemetryResponseBytes)
			reads := spy.reads
			if eof {
				_, err = response.Body.Read(make([]byte, 1))
				require.ErrorIs(t, err, io.EOF)
				reads++
			}
			require.NoError(t, response.Body.Close())
			require.Equal(t, reads, spy.reads, "no extra read on Record, parsing or Close")
			require.Nil(t, wrapped.pending)
			if len(scratch) > 0 {
				require.Equal(t, make([]byte, len(scratch)), scratch, "scratch must be scrubbed")
			}
			want := ""
			if eof && size <= maxTelemetryResponseBytes {
				want = "capability_denied"
			}
			require.Equal(t, want, attempt.event(attempt.facts, 403, nil).FailureClass)
		}
	}
	// Native error handling uses a limited reader. Hitting its exact limit can
	// produce a synthetic EOF outside the observer and must not count as proof.
	spy := &telemetryReadSpy{reader: strings.NewReader(body), noEOF: true}
	_, attempt, response := telemetryFailureAttempt(t, 403, "application/json", spy, true)
	got, err := io.ReadAll(io.LimitReader(response.Body, int64(len(body))))
	require.NoError(t, err)
	require.Equal(t, body, string(got))
	require.NoError(t, response.Body.Close())
	require.Equal(t, 1, spy.reads)
	require.Empty(t, attempt.event(attempt.facts, 403, nil).FailureClass)
}

type telemetryFailureLastRead struct {
	*strings.Reader
	err, closeErr error
}

func (r telemetryFailureLastRead) Read(raw []byte) (int, error) {
	n, err := r.Reader.Read(raw)
	if r.Len() == 0 {
		err = r.err
	}
	return n, err
}

func (r telemetryFailureLastRead) Close() error { return r.closeErr }

func TestTelemetryFailureLaterReadRevokesClass(t *testing.T) {
	for _, variant := range []string{"repeated_eof", "late_bytes", "late_error"} {
		reader := &telemetryFailureLastRead{Reader: strings.NewReader(`{"code":"operator-model"}`), err: io.EOF}
		_, attempt, response := telemetryFailureAttempt(t, 403, "application/json", reader, true)
		_, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, "capability_denied", attempt.event(attempt.facts, 403, nil).FailureClass)
		switch variant {
		case "late_bytes":
			reader.Reader = strings.NewReader(" ")
		case "late_error":
			reader.err = io.ErrUnexpectedEOF
		}
		_, err = response.Body.Read(make([]byte, 1))
		if variant == "late_error" {
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		} else {
			require.ErrorIs(t, err, io.EOF)
		}
		require.NoError(t, response.Body.Close())
		want := ""
		if variant == "repeated_eof" {
			want = "capability_denied"
		}
		require.Equal(t, want, attempt.event(attempt.facts, 403, nil).FailureClass)
	}
}

func TestTelemetryFailureRevokesUntrustedOrFailedObservation(t *testing.T) {
	body := `{"error":{"code":"operator-model"}}`
	for _, variant := range []string{"good_eof", "read_error", "close_error", "canceled", "multiple", "bad_credentials", "bad_account", "transport_error", "plain", "sse", "missing_body", "401", "200"} {
		t.Run(variant, func(t *testing.T) {
			ctx, attempt := telemetryTestAttempt(t)
			attempt.target.ErrorRules = telemetryTestErrorRules()
			credentials := map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"}
			account := int64(42)
			if variant == "bad_credentials" {
				credentials["api_key"] = "rotated"
			}
			if variant == "bad_account" {
				account++
			}
			hook := PrepareTelemetryHTTP(ctx, account, credentials)
			reader := telemetryFailureLastRead{Reader: strings.NewReader(body), err: io.EOF}
			status, media := 403, "application/json"
			if variant == "read_error" {
				reader.err = io.ErrUnexpectedEOF
			}
			if variant == "close_error" {
				reader.closeErr = errors.New("close failed")
			}
			if variant == "plain" {
				media = "text/plain"
			}
			if variant == "sse" {
				media = "text/event-stream"
			}
			if variant == "401" {
				status = 401
			}
			if variant == "200" {
				status = 200
			}
			response := &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {media}}, Body: reader}
			if variant == "missing_body" {
				response.Body = nil
			}
			var transportErr error
			if variant == "transport_error" {
				transportErr = errors.New("transport failed")
			}
			hook.Record(response, transportErr, 0)
			if response.Body != nil {
				_, err := io.ReadAll(response.Body)
				if variant == "read_error" {
					require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				} else {
					require.NoError(t, err)
				}
				closeErr := response.Body.Close()
				require.Equal(t, variant == "close_error", closeErr != nil)
			}
			if variant == "canceled" {
				hook.Complete(true)
			}
			if variant == "multiple" {
				hook.Record(nil, nil, 0)
			}
			event := attempt.event(attempt.facts, status, nil)
			if variant == "good_eof" {
				require.Equal(t, "capability_denied", event.FailureClass)
				require.Equal(t, "upstream_auth", event.Outcome)
				require.Empty(t, attempt.event(attempt.facts, status, context.Canceled).FailureClass)
			} else {
				require.Empty(t, event.FailureClass)
			}
		})
	}
}

func TestTelemetryFailurePersistsOnlyClassOnce(t *testing.T) {
	_, attempt, response := telemetryFailureAttempt(t, 403, "application/json", io.NopCloser(strings.NewReader(`{"error":{"code":"operator-model","message":"private provider text"}}`)), true)
	_, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	attempt.producer.db = db
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT singleton FROM bridge.dispatch_telemetry_source").WillReturnRows(sqlmock.NewRows([]string{"singleton"}).AddRow(true))
	args := make([]driver.Value, 27)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	args[12], args[13], args[21], args[22] = "upstream_auth", true, int64(0), "capability_denied"
	args[23] = false
	mock.ExpectExec("INSERT INTO bridge.dispatch_observation").WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() { results <- attempt.Finish(context.Background(), 403, nil) })
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
