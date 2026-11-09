package dispatch

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/stretchr/testify/require"
)

type receiptReaderFunc func(context.Context, string, string) (*custombridge.Receipt, error)

func (f receiptReaderFunc) ReadReceipt(ctx context.Context, reservation, attempt string) (*custombridge.Receipt, error) {
	return f(ctx, reservation, attempt)
}

const probeJSON = `{"model":"gpt-fixture","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`

func probeFixture(t *testing.T, core http.Handler, reader ReceiptReader) (*ProbeRunner, ProbeLease, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(core)
	t.Cleanup(server.Close)
	cfg := ProbeConfig{CoreOrigin: server.URL, AllowLoopbackHTTP: true, Audience: "core", CredentialsFile: "unused", ReceiptSource: "source", ReceiptWaitSeconds: 1}
	creds := ProbeCredentials{KID: "test-key", SecretBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("placeholder"), 4)), CoreAPIKey: "synthetic-service-token", InternalKeyID: 7, GroupID: 8}
	runner, err := NewProbeRunner(cfg, creds, reader)
	require.NoError(t, err)
	t.Cleanup(runner.Close)
	return runner, ProbeLease{CoreInstance: "core", Target: testTarget("a", 1), Token: "token", Generation: 1, ExpiresAt: time.Now().Add(30 * time.Second), ReservationID: "reservation", AttemptID: "attempt"}, server
}

func matchedReceipt(reservation, attempt string) *custombridge.Receipt {
	return &custombridge.Receipt{SchemaVersion: 1, ReservationID: reservation, AttemptID: attempt, AccountID: 1, InternalKeyID: 7, Billing: custombridge.ReceiptBilling{Model: "gpt-fixture"}}
}

func TestDispatchProbeRequiresSignedBindingAndCommittedReceipt(t *testing.T) {
	var calls atomic.Int32
	var coreErr atomic.Value
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			coreErr.Store(err.Error())
			w.WriteHeader(500)
			return
		}
		key := custombridge.SigningKey{ID: "test-key", Secret: bytes.Repeat([]byte("placeholder"), 4), InternalKeyID: 7, GroupID: 8}
		verifier, err := custombridge.NewForwardVerifier("core", []custombridge.SigningKey{key})
		if err != nil {
			coreErr.Store(err.Error())
			w.WriteHeader(500)
			return
		}
		envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 7, GroupID: 8}, time.Now())
		if err != nil {
			coreErr.Store(err.Error())
			w.WriteHeader(401)
			return
		}
		if envelope.AccountID != 1 || envelope.AttemptID != "attempt" || envelope.ReservationID != "reservation" || r.Header.Get("Authorization") != "Bearer synthetic-service-token" {
			coreErr.Store("binding mismatch")
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, probeJSON)
	})
	var receipts atomic.Int32
	reader := receiptReaderFunc(func(_ context.Context, r, a string) (*custombridge.Receipt, error) {
		if receipts.Add(1) == 1 {
			return nil, sql.ErrNoRows
		}
		return matchedReceipt(r, a), nil
	})
	runner, lease, _ := probeFixture(t, core, reader)
	o := runner.Execute(context.Background(), lease)
	require.Nil(t, coreErr.Load())
	require.Equal(t, Success, o.Outcome)
	require.True(t, o.ProviderEvidence)
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, int32(2), receipts.Load())
	require.Equal(t, lease.AttemptID, o.EventID)
}

func TestDispatchProbeNeverAttributesCoreErrorsOrFollowsRedirect(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
			})
			runner, lease, _ := probeFixture(t, core, receiptReaderFunc(func(context.Context, string, string) (*custombridge.Receipt, error) {
				t.Error("error response must not be treated as a successful probe")
				return nil, sql.ErrNoRows
			}))
			o := runner.Execute(context.Background(), lease)
			require.Equal(t, Unknown, o.Outcome)
			require.False(t, o.ProviderEvidence)
			require.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestDispatchProbeRejectsMissingConflictingOrWrongReceipt(t *testing.T) {
	for name, reader := range map[string]receiptReaderFunc{
		"missing":  func(context.Context, string, string) (*custombridge.Receipt, error) { return nil, sql.ErrNoRows },
		"conflict": func(context.Context, string, string) (*custombridge.Receipt, error) { return nil, ErrProbeReceipt },
		"wrong_account": func(_ context.Context, r, a string) (*custombridge.Receipt, error) {
			receipt := matchedReceipt(r, a)
			receipt.AccountID = 2
			return receipt, nil
		},
		"wrong_model": func(_ context.Context, r, a string) (*custombridge.Receipt, error) {
			receipt := matchedReceipt(r, a)
			receipt.Billing.Model = "other-model"
			return receipt, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			runner, lease, _ := probeFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, probeJSON) }), reader)
			o := runner.Execute(context.Background(), lease)
			require.Equal(t, Unknown, o.Outcome)
			require.False(t, o.ProviderEvidence)
		})
	}
}

func TestDispatchProbeBoundsBodyAndRequiresSemanticCompletion(t *testing.T) {
	for name, body := range map[string]string{
		"large": string(bytes.Repeat([]byte("x"), 65<<10)), "no_content": `{"model":"gpt-fixture","status":"completed","output":[]}`,
		"failed": `{"model":"gpt-fixture","status":"failed","output":[]}`, "invalid": `{`,
	} {
		t.Run(name, func(t *testing.T) {
			runner, lease, _ := probeFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }), receiptReaderFunc(func(context.Context, string, string) (*custombridge.Receipt, error) {
				t.Error("invalid content must not request a receipt")
				return nil, sql.ErrNoRows
			}))
			require.Equal(t, Unknown, runner.Execute(context.Background(), lease).Outcome)
		})
	}
}

func TestDispatchProbeExpiredLeaseAndCancellationDoNotResend(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	runner, lease, _ := probeFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}), receiptReaderFunc(func(context.Context, string, string) (*custombridge.Receipt, error) { return nil, sql.ErrNoRows }))
	expired := lease
	expired.ExpiresAt = time.Now()
	require.Equal(t, Unknown, runner.Execute(context.Background(), expired).Outcome)
	require.Zero(t, calls.Load())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Observation, 1)
	go func() { done <- runner.Execute(ctx, lease) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case o := <-done:
		require.Equal(t, Unknown, o.Outcome)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("probe did not stop")
	}
	close(release)
	require.Equal(t, int32(1), calls.Load())
}
