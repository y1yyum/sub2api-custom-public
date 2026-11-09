//go:build integration

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/repository/testdata/bridge_crash_worker/protocol"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 只杀隔离 billing helper 的真实进程；不杀 Core 部署或数据库进程。
// SEND_INTENT 文件是演练夹具，不能证明运行时入口已经实现发送防重放。
func TestUsageBillingBridgeCrash(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("真实 SIGKILL 与连接探测演练要求 Linux")
	}
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer buildCancel()
	binary := filepath.Join(t.TempDir(), "bridge-crash-worker")
	build := exec.CommandContext(buildCtx, "go", "build", "-p", "2", "-o", binary, "./testdata/bridge_crash_worker")
	build.WaitDelay = 5 * time.Second
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build isolated worker: %s", output)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = integrationDB.ExecContext(ctx, "DROP SCHEMA IF EXISTS bridge CASCADE")
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, cleanupErr := integrationDB.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS bridge CASCADE")
		require.NoError(t, cleanupErr)
	})
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))

	for _, mode := range []string{"before_apply", "during_commit", "after_commit"} {
		t.Run(mode, func(t *testing.T) {
			testBridgeCrashWindow(t, binary, mode)
		})
	}
}

func testBridgeCrashWindow(t *testing.T, binary, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := newBridgeReceiptCommand(t)
	provider := newBridgeCrashProvider(t, command.BridgeUsage)
	directory := t.TempDir()
	config := protocol.Config{
		DSN: integrationDSN, Mode: mode, ProviderURL: provider.server.URL,
		IntentPath: filepath.Join(directory, "send-intent.json"), ContinuePath: filepath.Join(directory, "continue"),
		Acknowledgement: filepath.Join(directory, "ack.json"), Command: *command,
	}
	var lockID int64
	if mode == "during_commit" {
		lockID = 2026110507
		installBridgeCrashCommitBarrier(t, ctx, command, lockID)
	}

	worker := startBridgeCrashWorker(t, ctx, binary, config)
	ready := worker.event(t, ctx, "ready")
	require.Equal(t, worker.cmd.Process.Pid, ready.PID)
	require.NotEqual(t, os.Getpid(), ready.PID)
	require.Positive(t, ready.BackendPID)
	returned := worker.event(t, ctx, "provider_returned")
	require.Equal(t, command.BridgeRequest, &returned.Intent.Request)
	provider.requireCalls(t, command, ready.PID, 1)

	var committed *custombridge.Receipt
	switch mode {
	case "during_commit":
		worker.event(t, ctx, "billing_started")
		require.Eventually(t, func() bool {
			var blocked bool
			err := integrationDB.QueryRowContext(ctx, `SELECT EXISTS (
				SELECT 1 FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
				WHERE l.pid = $1 AND l.locktype = 'advisory' AND NOT l.granted
				AND l.classid = 0 AND l.objid = $2 AND a.query = 'COMMIT')`, ready.BackendPID, lockID).Scan(&blocked)
			return err == nil && blocked
		}, 5*time.Second, 20*time.Millisecond, "real Apply must enter its deferred COMMIT trigger")
		t.Logf("window=%s billing_pid=%d postgres_pid=%d barrier=deferred_commit mutations=validated_inside_transaction", mode, ready.PID, ready.BackendPID)
	case "after_commit":
		worker.event(t, ctx, "billing_started")
		event := worker.event(t, ctx, "committed_before_ack")
		require.True(t, event.Applied)
		require.NotNil(t, event.Receipt)
		committed = event.Receipt
	}
	requireBridgeCrashState(t, command, mode == "after_commit")
	require.NoFileExists(t, config.Acknowledgement, "the business acknowledgement must not have been written")
	worker.kill(t)
	// 不释放提交屏障、不杀 PostgreSQL 后端；必须由客户端进程消失触发断连回滚。
	require.Eventually(t, func() bool {
		var exists bool
		err := integrationDB.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1)", ready.BackendPID).Scan(&exists)
		return err == nil && !exists
	}, 5*time.Second, 20*time.Millisecond, "PostgreSQL must detect the killed helper without releasing the barrier")
	requireBridgeCrashState(t, command, mode == "after_commit")
	require.NoFileExists(t, config.Acknowledgement)
	provider.requireCalls(t, command, ready.PID, 1)
	if mode == "during_commit" {
		_, err := integrationDB.ExecContext(ctx, "DROP TRIGGER wait_bridge_crash_commit ON bridge.receipt_outbox")
		require.NoError(t, err)
	}

	config.Mode = "recover"
	config.Reconcile = false
	recovered := runBridgeCrashRecovery(t, ctx, binary, config, ready.PID)
	require.Equal(t, ready.PID, recovered.Intent.SenderPID)
	require.Equal(t, *command.BridgeRequest, recovered.Intent.Request)
	if committed != nil {
		require.Equal(t, "settled", recovered.State)
		require.Equal(t, committed, recovered.Receipt)
	} else {
		require.Equal(t, "pending_review", recovered.State)
		require.Nil(t, recovered.Receipt)
	}
	provider.requireCalls(t, command, ready.PID, 1)
	t.Logf("window=%s killed_pid=%d signal=SIGKILL fresh_read_pid=%d state=%s provider_calls=1", mode, ready.PID, recovered.PID, recovered.State)

	if mode == "before_apply" {
		second := runBridgeCrashRecovery(t, ctx, binary, config, ready.PID, recovered.PID)
		require.Equal(t, "pending_review", second.State)
		require.Nil(t, second.Receipt)
		requireBridgeCrashState(t, command, false)
		provider.requireCalls(t, command, ready.PID, 1)
		t.Logf("window=%s second_read_pid=%d automatic_provider_resends=0 charge=0 receipt=0", mode, second.PID)
		return
	}

	config.Reconcile = true
	firstRetry := runBridgeCrashReconcile(t, ctx, binary, config, ready.PID, recovered.PID)
	require.Equal(t, mode == "during_commit", firstRetry.Applied)
	require.NotNil(t, firstRetry.Receipt)
	if committed != nil {
		require.Equal(t, committed, firstRetry.Receipt)
	}
	secondRetry := runBridgeCrashReconcile(t, ctx, binary, config, ready.PID, recovered.PID, firstRetry.PID)
	require.False(t, secondRetry.Applied)
	require.Equal(t, firstRetry.Receipt, secondRetry.Receipt)
	requireBridgeCrashState(t, command, true)
	provider.requireCalls(t, command, ready.PID, 1)
	require.NoFileExists(t, config.Acknowledgement)
	t.Logf("window=%s reconcile_pid=%d applied=%t second_reconcile_pid=%d applied=false receipt=%s total_charge=1.25 provider_calls=1", mode, firstRetry.PID, firstRetry.Applied, secondRetry.PID, firstRetry.Receipt.ID)
}

func installBridgeCrashCommitBarrier(t *testing.T, ctx context.Context, command *service.UsageBillingCommand, lockID int64) {
	t.Helper()
	locker, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	_, err = locker.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockID)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, unlockErr := locker.ExecContext(cleanupCtx, "SELECT pg_advisory_unlock($1)", lockID)
		require.NoError(t, unlockErr)
		require.NoError(t, locker.Close())
		_, dropErr := integrationDB.ExecContext(cleanupCtx, "DROP FUNCTION IF EXISTS bridge.wait_bridge_crash_commit() CASCADE")
		require.NoError(t, dropErr)
	})
	// 延迟触发器在 Commit 执行，且先验证同事务的余额、配额、幂等键与回执。
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION bridge.wait_bridge_crash_commit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF (SELECT balance FROM users WHERE id = NEW.user_id) IS DISTINCT FROM 98.75
				OR (SELECT quota_used FROM api_keys WHERE id = NEW.internal_key_id) IS DISTINCT FROM 1.25
				OR (SELECT usage_5h FROM api_keys WHERE id = NEW.internal_key_id) IS DISTINCT FROM 1.25
				OR (SELECT (extra->>'quota_used')::numeric FROM accounts WHERE id = NEW.account_id) IS DISTINCT FROM 1.25
				OR NOT EXISTS (SELECT 1 FROM usage_billing_dedup
					WHERE request_id = NEW.core_request_id AND api_key_id = NEW.internal_key_id)
				OR NOT EXISTS (SELECT 1 FROM bridge.receipt_outbox WHERE receipt_id = NEW.receipt_id)
			THEN RAISE EXCEPTION 'billing mutations missing before crash barrier'; END IF;
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NEW;
		END; $$;
		CREATE CONSTRAINT TRIGGER wait_bridge_crash_commit
		AFTER INSERT ON bridge.receipt_outbox DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW WHEN (NEW.core_request_id = '%s') EXECUTE FUNCTION bridge.wait_bridge_crash_commit();`, lockID, command.RequestID))
	require.NoError(t, err)
}

func requireBridgeCrashState(t *testing.T, command *service.UsageBillingCommand, charged bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var balance, quota, window, accountQuota float64
	var dedup, receipts int
	err := integrationDB.QueryRowContext(ctx, `SELECT u.balance, k.quota_used, k.usage_5h,
		COALESCE((a.extra->>'quota_used')::numeric, 0),
		(SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $4 AND api_key_id = k.id),
		(SELECT COUNT(*) FROM bridge.receipt_outbox WHERE reservation_id = $5 AND attempt_id = $6)
		FROM users u, api_keys k, accounts a WHERE u.id = $1 AND k.id = $2 AND a.id = $3`,
		command.UserID, command.APIKeyID, command.AccountID, command.RequestID,
		command.BridgeRequest.ReservationID, command.BridgeRequest.AttemptID).Scan(&balance, &quota, &window, &accountQuota, &dedup, &receipts)
	require.NoError(t, err)
	if charged {
		require.Equal(t, 98.75, balance)
		require.Equal(t, 1.25, quota)
		require.Equal(t, 1.25, window)
		require.Equal(t, 1.25, accountQuota)
		require.Equal(t, 1, dedup)
		require.Equal(t, 1, receipts)
	} else {
		require.Equal(t, 100.0, balance)
		require.Zero(t, quota)
		require.Zero(t, window)
		require.Zero(t, accountQuota)
		require.Zero(t, dedup)
		require.Zero(t, receipts)
	}
}

type bridgeCrashWorker struct {
	cmd      *exec.Cmd
	events   chan protocol.Event
	done     chan struct{}
	waitErr  error
	parseErr error
	stderr   bytes.Buffer
	cancel   context.CancelFunc
}

func startBridgeCrashWorker(t *testing.T, ctx context.Context, binary string, config protocol.Config) *bridgeCrashWorker {
	t.Helper()
	encoded, err := json.Marshal(config)
	require.NoError(t, err)
	workerCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	worker := &bridgeCrashWorker{
		cmd: exec.CommandContext(workerCtx, binary), events: make(chan protocol.Event, 16),
		done: make(chan struct{}), cancel: cancel,
	}
	worker.cmd.Stdin = bytes.NewReader(encoded)
	worker.cmd.Stderr = &worker.stderr
	stdout, err := worker.cmd.StdoutPipe()
	require.NoError(t, err)
	if err := worker.cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start isolated billing worker: %v", err)
	}
	go func() {
		decoder := json.NewDecoder(stdout)
		for {
			var event protocol.Event
			if err := decoder.Decode(&event); err != nil {
				if !errors.Is(err, io.EOF) {
					worker.parseErr = err
					_ = worker.cmd.Process.Kill()
				}
				break
			}
			worker.events <- event
		}
		worker.waitErr = worker.cmd.Wait()
		close(worker.events)
		close(worker.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-worker.done:
		case <-time.After(10 * time.Second):
			_ = worker.cmd.Process.Kill()
			t.Errorf("isolated billing worker pid=%d did not exit during cleanup", worker.cmd.Process.Pid)
		}
	})
	return worker
}

func (worker *bridgeCrashWorker) event(t *testing.T, ctx context.Context, stage string) protocol.Event {
	t.Helper()
	select {
	case event, ok := <-worker.events:
		if !ok {
			worker.wait(t)
			t.Fatalf("worker pid=%d exited before %s", worker.cmd.Process.Pid, stage)
		}
		require.Equal(t, stage, event.Stage)
		require.Equal(t, worker.cmd.Process.Pid, event.PID)
		return event
	case <-ctx.Done():
		t.Fatalf("worker pid=%d timed out waiting for %s: %v", worker.cmd.Process.Pid, stage, ctx.Err())
		return protocol.Event{}
	}
}

func (worker *bridgeCrashWorker) wait(t *testing.T) {
	t.Helper()
	select {
	case <-worker.done:
		require.NoError(t, worker.parseErr, "decode worker output")
		require.NoError(t, worker.waitErr, "worker stderr: %s", worker.stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatalf("worker pid=%d did not finish", worker.cmd.Process.Pid)
	}
	worker.cancel()
}

func (worker *bridgeCrashWorker) kill(t *testing.T) {
	t.Helper()
	require.NoError(t, worker.cmd.Process.Signal(syscall.SIGKILL))
	select {
	case <-worker.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("worker pid=%d did not exit after SIGKILL", worker.cmd.Process.Pid)
	}
	require.NoError(t, worker.parseErr)
	var exitErr *exec.ExitError
	require.ErrorAs(t, worker.waitErr, &exitErr)
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	require.True(t, status.Signaled())
	require.Equal(t, syscall.SIGKILL, status.Signal())
	require.Empty(t, worker.stderr.String())
	worker.cancel()
}

func runBridgeCrashRecovery(t *testing.T, ctx context.Context, binary string, config protocol.Config, previousPIDs ...int) protocol.Event {
	t.Helper()
	worker := startBridgeCrashWorker(t, ctx, binary, config)
	ready := worker.event(t, ctx, "ready")
	require.NotContains(t, previousPIDs, ready.PID)
	event := worker.event(t, ctx, "recovered")
	worker.wait(t)
	return event
}

func runBridgeCrashReconcile(t *testing.T, ctx context.Context, binary string, config protocol.Config, previousPIDs ...int) protocol.Event {
	t.Helper()
	worker := startBridgeCrashWorker(t, ctx, binary, config)
	ready := worker.event(t, ctx, "ready")
	require.NotContains(t, previousPIDs, ready.PID)
	worker.event(t, ctx, "recovered")
	event := worker.event(t, ctx, "reconciled")
	worker.wait(t)
	return event
}

type bridgeCrashProvider struct {
	server *httptest.Server
	path   string
	mu     sync.Mutex
	err    error
	calls  []protocol.Intent
}

func newBridgeCrashProvider(t *testing.T, usage json.RawMessage) *bridgeCrashProvider {
	t.Helper()
	provider := &bridgeCrashProvider{path: filepath.Join(t.TempDir(), "provider-calls.jsonl")}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var intent protocol.Intent
		if request.Method != http.MethodPost || json.NewDecoder(io.LimitReader(request.Body, 65536)).Decode(&intent) != nil {
			http.Error(w, "invalid synthetic provider request", http.StatusBadRequest)
			return
		}
		provider.mu.Lock()
		defer provider.mu.Unlock()
		file, err := os.OpenFile(provider.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			err = json.NewEncoder(file).Encode(intent)
			if err == nil {
				err = file.Sync()
			}
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			provider.err = err
			http.Error(w, "persist fake provider record failed", http.StatusInternalServerError)
			return
		}
		provider.calls = append(provider.calls, intent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(usage)
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func (provider *bridgeCrashProvider) requireCalls(t *testing.T, command *service.UsageBillingCommand, senderPID, count int) {
	t.Helper()
	provider.mu.Lock()
	defer provider.mu.Unlock()
	require.NoError(t, provider.err)
	require.Len(t, provider.calls, count)
	file, err := os.Open(provider.path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	for range count {
		var persisted protocol.Intent
		require.NoError(t, decoder.Decode(&persisted))
		require.Equal(t, senderPID, persisted.SenderPID)
		require.Equal(t, command.RequestID, persisted.CoreRequestID)
		require.Equal(t, command.APIKeyID, persisted.InternalKeyID)
		require.Equal(t, *command.BridgeRequest, persisted.Request)
	}
	var extra protocol.Intent
	require.ErrorIs(t, decoder.Decode(&extra), io.EOF)
}
