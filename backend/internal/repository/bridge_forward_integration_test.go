//go:build integration

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func bridgeForwardEnvelope(cmd *service.UsageBillingCommand) custombridge.ForwardEnvelope {
	return custombridge.ForwardEnvelope{Version: 1, KID: "fixture", Audience: "core", RequestID: "rid", ReservationID: cmd.BridgeRequest.ReservationID,
		AttemptID: cmd.BridgeRequest.AttemptID, InternalKeyID: cmd.APIKeyID, GroupID: 1, AccountID: cmd.AccountID, Endpoint: "/v1/responses", CanonicalModel: cmd.Model,
		PayloadSHA256: "fixture-hash", IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), Deadline: time.Now().Add(5 * time.Minute).Unix(), PolicyRevision: "p1", PoolRef: "pool", PayerRef: "payer", ActorRef: "actor"}
}

func TestBridgeForwardAttemptPersistence(t *testing.T) {
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, "DROP SCHEMA IF EXISTS bridge CASCADE")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS bridge CASCADE")
		require.NoError(t, err)
	})
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, integrationDB))
	repo := custombridge.NewForwardAttemptRepository(integrationDB)
	t.Run("concurrent_receive_and_send", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		e := bridgeForwardEnvelope(cmd)
		receives, sends := 0, 0
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for range 8 {
			wg.Go(func() { results <- repo.Receive(ctx, e, cmd.RequestID) })
		}
		wg.Wait()
		close(results)
		for err := range results {
			if err == nil {
				receives++
			} else {
				require.ErrorIs(t, err, custombridge.ErrAttemptReplay)
			}
		}
		require.Equal(t, 1, receives)
		results = make(chan error, 8)
		for range 8 {
			wg.Go(func() { results <- repo.ClaimSend(ctx, e.InternalKeyID, e.AttemptID) })
		}
		wg.Wait()
		close(results)
		for err := range results {
			if err == nil {
				sends++
			} else {
				require.ErrorIs(t, err, custombridge.ErrAttemptReplay)
			}
		}
		require.Equal(t, 1, sends)
		require.NoError(t, repo.Finish(ctx, e.InternalKeyID, e.AttemptID))
		status, err := repo.Get(ctx, e.InternalKeyID, e.AttemptID)
		require.NoError(t, err)
		require.Equal(t, "reconciliation_required", status.State)
		require.Equal(t, cmd.RequestID, status.CoreRequestID)
		e.KID = "rotated"
		e.RequestID = "new-rid"
		e.IssuedAt++
		e.ExpiresAt++
		require.ErrorIs(t, repo.Receive(ctx, e, "different-core-id"), custombridge.ErrAttemptReplay)
		for _, change := range []func(*custombridge.ForwardEnvelope){func(e *custombridge.ForwardEnvelope) { e.AccountID++ }, func(e *custombridge.ForwardEnvelope) { e.PayloadSHA256 = "other" }, func(e *custombridge.ForwardEnvelope) { e.ActorRef = "other" }} {
			changed := e
			change(&changed)
			require.ErrorIs(t, repo.Receive(ctx, changed, cmd.RequestID), custombridge.ErrAttemptConflict)
		}
		first, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		status, err = repo.Get(ctx, e.InternalKeyID, e.AttemptID)
		require.NoError(t, err)
		require.Equal(t, "core_committed", status.State)
		require.Equal(t, first.BridgeReceipt.ID, status.ReceiptID)
		require.ErrorIs(t, repo.ClaimSend(ctx, e.InternalKeyID, e.AttemptID), custombridge.ErrAttemptReplay)
	})
	t.Run("pre_dispatch_rejection_stays_rejected", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		e := bridgeForwardEnvelope(cmd)
		require.NoError(t, repo.Receive(ctx, e, cmd.RequestID))
		require.NoError(t, repo.Finish(ctx, e.InternalKeyID, e.AttemptID))
		require.ErrorIs(t, repo.ClaimSend(ctx, e.InternalKeyID, e.AttemptID), custombridge.ErrAttemptReplay)
		status, err := repo.Get(ctx, e.InternalKeyID, e.AttemptID)
		require.NoError(t, err)
		require.Equal(t, "rejected", status.State)
	})
	t.Run("database_loss_fails_closed", func(t *testing.T) {
		_, err := integrationDB.ExecContext(ctx, "ALTER TABLE bridge.forward_attempt RENAME TO unavailable_forward_attempt")
		require.NoError(t, err)
		defer func() {
			_, err := integrationDB.ExecContext(ctx, "ALTER TABLE bridge.unavailable_forward_attempt RENAME TO forward_attempt")
			require.NoError(t, err)
		}()
		cmd := newBridgeReceiptCommand(t)
		e := bridgeForwardEnvelope(cmd)
		require.Error(t, repo.Receive(ctx, e, cmd.RequestID))
		require.Error(t, repo.ClaimSend(ctx, e.InternalKeyID, e.AttemptID))
	})
	t.Run("process_crash_never_replays_provider", testBridgeForwardProcessCrash)
}

type bridgeForwardCrashFixture struct {
	DSN, Mode, ProviderURL, CoreRequestID string
	Envelope                              custombridge.ForwardEnvelope
}
type bridgeForwardCrashEvent struct {
	PID           int    `json:"pid"`
	State         string `json:"state"`
	CoreRequestID string `json:"core_request_id"`
}

func testBridgeForwardProcessCrash(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SIGKILL recovery requires Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "bridge-forward-worker")
	build := exec.CommandContext(ctx, "go", "build", "-p", "2", "-o", binary, "./testdata/bridge_forward_worker")
	build.WaitDelay = 5 * time.Second
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build forward helper: %s", output)
	for _, mode := range []string{"after_receive", "after_claim", "after_provider"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
			defer provider.Close()
			cmd := newBridgeReceiptCommand(t)
			fixture := bridgeForwardCrashFixture{DSN: integrationDSN, Mode: mode, ProviderURL: provider.URL, CoreRequestID: cmd.RequestID, Envelope: bridgeForwardEnvelope(cmd)}
			worker, decoder, stderr := startBridgeForwardCrashWorker(t, ctx, binary, fixture)
			var ready bridgeForwardCrashEvent
			require.NoError(t, decoder.Decode(&ready))
			require.Equal(t, worker.Process.Pid, ready.PID)
			require.NoError(t, worker.Process.Kill())
			err := worker.Wait()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, stderr.String())
			signal, ok := exit.Sys().(syscall.WaitStatus)
			require.True(t, ok)
			require.Equal(t, syscall.SIGKILL, signal.Signal())
			fixture.Mode = "retry"
			restarted, decoder, stderr := startBridgeForwardCrashWorker(t, ctx, binary, fixture)
			var recovered bridgeForwardCrashEvent
			require.NoError(t, decoder.Decode(&recovered))
			require.NoError(t, restarted.Wait(), stderr.String())
			require.NotEqual(t, ready.PID, recovered.PID)
			require.Equal(t, cmd.RequestID, recovered.CoreRequestID)
			wantState, wantCalls := "reconciliation_required", int64(0)
			if mode == "after_receive" {
				wantState = "received"
			}
			if mode == "after_provider" {
				wantCalls = 1
			}
			require.Equal(t, wantState, recovered.State)
			require.Equal(t, wantCalls, calls.Load())
			requireBridgeCrashState(t, cmd, false)
			t.Logf("mode=%s killed_pid=%d signal=SIGKILL fresh_pid=%d state=%s provider_calls=%d automatic_resends=0", mode, ready.PID, recovered.PID, recovered.State, calls.Load())
		})
	}
}

func startBridgeForwardCrashWorker(t *testing.T, ctx context.Context, binary string, fixture bridgeForwardCrashFixture) (*exec.Cmd, *json.Decoder, *bytes.Buffer) {
	t.Helper()
	encoded, err := json.Marshal(fixture)
	require.NoError(t, err)
	command := exec.CommandContext(ctx, binary)
	command.WaitDelay = 5 * time.Second
	command.Stdin = bytes.NewReader(encoded)
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		_ = stdout.Close()
	})
	return command, json.NewDecoder(io.LimitReader(stdout, 8192)), stderr
}
