//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func newBridgeReceiptCommand(t *testing.T) *service.UsageBillingCommand {
	t.Helper()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{
		Email: "bridge-receipt-" + uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 100,
	})
	key := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID, Key: "bridge-fixture-" + uuid.NewString(), Name: "bridge receipt", Quota: 100,
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "bridge-receipt-" + uuid.NewString(), Type: service.AccountTypeAPIKey,
		Extra: map[string]any{"quota_limit": 100.0},
	})
	return &service.UsageBillingCommand{
		RequestID: uuid.NewString(), APIKeyID: key.ID, UserID: user.ID, AccountID: account.ID,
		AccountType: service.AccountTypeAPIKey, Model: "gpt-5.1", InputTokens: 900, OutputTokens: 30,
		CacheReadTokens: 200, CacheCreationTokens: 100,
		BalanceCost: 1.25, APIKeyQuotaCost: 1.25, APIKeyRateLimitCost: 1.25, AccountQuotaCost: 1.25,
		BridgeRequest: &custombridge.Request{ReservationID: uuid.NewString(), AttemptID: uuid.NewString(), AccountID: account.ID},
		BridgeUsage:   []byte(`{"input_tokens":1200,"output_tokens":30,"cache_read_input_tokens":200,"cache_creation_input_tokens":100}`),
	}
}

func requireBridgeReceiptState(t *testing.T, cmd *service.UsageBillingCommand, charged bool) {
	t.Helper()
	ctx := context.Background()
	var balance, quota, window, accountQuota float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", cmd.UserID).Scan(&balance))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used, usage_5h FROM api_keys WHERE id = $1", cmd.APIKeyID).Scan(&quota, &window))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COALESCE((extra->>'quota_used')::numeric, 0) FROM accounts WHERE id = $1", cmd.AccountID).Scan(&accountQuota))
	wantBalance, wantQuota := 100.0, 0.0
	wantDedup := 0
	if charged {
		wantBalance -= cmd.BalanceCost
		wantQuota = cmd.APIKeyQuotaCost
		wantDedup = 1
	}
	require.Equal(t, wantBalance, balance)
	require.Equal(t, wantQuota, quota)
	if charged {
		require.Equal(t, cmd.APIKeyRateLimitCost, window)
		require.Equal(t, cmd.AccountQuotaCost, accountQuota)
	} else {
		require.Zero(t, window)
		require.Zero(t, accountQuota)
	}
	var dedup int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_billing_dedup
		WHERE request_id = $1 AND api_key_id = $2`, cmd.RequestID, cmd.APIKeyID).Scan(&dedup))
	require.Equal(t, wantDedup, dedup)
}

func requireBridgeReceiptCount(t *testing.T, cmd *service.UsageBillingCommand, count int) {
	t.Helper()
	var actual int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM bridge.receipt_outbox
		WHERE reservation_id = $1 AND attempt_id = $2`, cmd.BridgeRequest.ReservationID, cmd.BridgeRequest.AttemptID).Scan(&actual))
	require.Equal(t, count, actual)
}

func TestUsageBillingBridgeReceipt(t *testing.T) {
	ctx := context.Background()
	// 此 DB 由 integration_harness_test.go 的临时容器独占创建。
	_, err := integrationDB.ExecContext(ctx, "DROP SCHEMA IF EXISTS bridge CASCADE")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(ctx, "DROP SCHEMA IF EXISTS bridge CASCADE")
		require.NoError(t, cleanupErr)
	})

	t.Run("ordinary_without_bridge_schema", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		cmd.BridgeRequest = nil
		repo := NewUsageBillingRepository(nil, integrationDB)
		first, err := repo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.True(t, first.Applied)
		require.Nil(t, first.BridgeReceipt)
		second, err := repo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.False(t, second.Applied)
		require.Nil(t, second.BridgeReceipt)
		requireBridgeReceiptState(t, cmd, true)
	})

	t.Run("managed_without_schema_rolls_back", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		result, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.Error(t, err)
		require.Nil(t, result)
		var pgError *pq.Error
		require.ErrorAs(t, err, &pgError)
		require.Contains(t, []pq.ErrorCode{"3F000", "42P01"}, pgError.Code)
		requireBridgeReceiptState(t, cmd, false)
	})

	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, integrationDB))

	t.Run("commit_duplicate_and_new_repository_read", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		first, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		require.True(t, first.Applied)
		require.NotNil(t, first.BridgeReceipt)
		requireBridgeReceiptState(t, cmd, true)

		freshDB, err := sql.Open("postgres", integrationDSN)
		require.NoError(t, err)
		defer func() { _ = freshDB.Close() }()
		persisted, err := custombridge.NewReceiptRepository(freshDB).Get(ctx, cmd.RequestID, cmd.APIKeyID)
		require.NoError(t, err)
		require.Equal(t, first.BridgeReceipt.ID, persisted.ID)
		require.Equal(t, custombridge.ReceiptSchemaVersion, persisted.SchemaVersion)
		require.Equal(t, cmd.BridgeRequest.ReservationID, persisted.ReservationID)
		require.Equal(t, cmd.BridgeRequest.AttemptID, persisted.AttemptID)
		require.Equal(t, cmd.AccountID, persisted.AccountID)
		require.Equal(t, cmd.UserID, persisted.UserID)
		require.Equal(t, cmd.APIKeyID, persisted.InternalKeyID)
		require.Equal(t, cmd.RequestID, persisted.CoreRequestID)
		require.Equal(t, 1.25, persisted.Billing.BalanceCost)
		require.Equal(t, cmd.RequestFingerprint, persisted.RequestFingerprint)
		require.JSONEq(t, string(cmd.BridgeUsage), string(persisted.RawUsage))
		require.False(t, persisted.CreatedAt.IsZero())
		second, err := NewUsageBillingRepository(nil, freshDB).Apply(ctx, cmd)
		require.NoError(t, err)
		require.False(t, second.Applied)
		require.Equal(t, persisted, second.BridgeReceipt)
		requireBridgeReceiptCount(t, cmd, 1)
		requireBridgeReceiptState(t, cmd, true)
	})

	t.Run("receipt_insert_failure_rolls_back_all_effects", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		_, err := integrationDB.ExecContext(ctx, fmt.Sprintf(`
			CREATE FUNCTION bridge.reject_receipt_for_test() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'injected receipt persistence failure'; END; $$;
			CREATE TRIGGER reject_receipt_for_test BEFORE INSERT ON bridge.receipt_outbox
			FOR EACH ROW WHEN (NEW.core_request_id = '%s') EXECUTE FUNCTION bridge.reject_receipt_for_test();`, cmd.RequestID))
		require.NoError(t, err)
		defer func() {
			_, cleanupErr := integrationDB.ExecContext(ctx, "DROP FUNCTION bridge.reject_receipt_for_test() CASCADE")
			require.NoError(t, cleanupErr)
		}()
		result, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.ErrorContains(t, err, "injected receipt persistence failure")
		require.Nil(t, result)
		requireBridgeReceiptState(t, cmd, false)
		requireBridgeReceiptCount(t, cmd, 0)
		_, err = custombridge.NewReceiptRepository(integrationDB).Get(ctx, cmd.RequestID, cmd.APIKeyID)
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, err = integrationDB.ExecContext(ctx, "DROP TRIGGER reject_receipt_for_test ON bridge.receipt_outbox")
		require.NoError(t, err)
		result, err = NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		require.True(t, result.Applied)
		requireBridgeReceiptState(t, cmd, true)
		requireBridgeReceiptCount(t, cmd, 1)
	})

	t.Run("duplicate_conflicts", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		first, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		for _, field := range []string{"reservation", "attempt", "raw_usage", "cost", "account", "second_core_request"} {
			t.Run(field, func(t *testing.T) {
				changed := *cmd
				request := *cmd.BridgeRequest
				changed.BridgeRequest = &request
				want := custombridge.ErrReceiptConflict
				switch field {
				case "reservation":
					request.ReservationID = uuid.NewString()
				case "attempt":
					request.AttemptID = uuid.NewString()
				case "raw_usage":
					changed.BridgeUsage = []byte(`{"input_tokens":1201,"output_tokens":30}`)
				case "cost":
					changed.BalanceCost = 2.5
					changed.RequestFingerprint = ""
					want = service.ErrUsageBillingRequestConflict
				case "account":
					changed.AccountID++
					want = custombridge.ErrReceiptAccountMismatch
				case "second_core_request":
					changed.RequestID = uuid.NewString()
					want = nil
				}
				result, applyErr := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, &changed)
				if want != nil {
					require.ErrorIs(t, applyErr, want)
				} else {
					var pgError *pq.Error
					require.ErrorAs(t, applyErr, &pgError)
					require.Equal(t, pq.ErrorCode("23505"), pgError.Code)
					var dedup int
					require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1", changed.RequestID).Scan(&dedup))
					require.Zero(t, dedup)
				}
				require.Nil(t, result)
				requireBridgeReceiptCount(t, cmd, 1)
				requireBridgeReceiptState(t, cmd, true)
			})
		}
		persisted, err := custombridge.NewReceiptRepository(integrationDB).Get(ctx, cmd.RequestID, cmd.APIKeyID)
		require.NoError(t, err)
		require.Equal(t, first.BridgeReceipt.ID, persisted.ID)
	})

	t.Run("zero_cost_keeps_raw_metering", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		cmd.BalanceCost, cmd.APIKeyQuotaCost, cmd.APIKeyRateLimitCost, cmd.AccountQuotaCost = 0, 0, 0, 0
		result, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		require.True(t, result.Applied)
		require.Zero(t, result.BridgeReceipt.Billing.BalanceCost)
		require.JSONEq(t, string(cmd.BridgeUsage), string(result.BridgeReceipt.RawUsage))
		requireBridgeReceiptCount(t, cmd, 1)
		requireBridgeReceiptState(t, cmd, true)
	})

	t.Run("ordinary_dedup_does_not_fabricate_receipt", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		request := cmd.BridgeRequest
		cmd.BridgeRequest = nil
		_, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		cmd.BridgeRequest = request
		result, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.ErrorIs(t, err, custombridge.ErrReceiptConflict)
		require.Nil(t, result)
		requireBridgeReceiptState(t, cmd, true)
		requireBridgeReceiptCount(t, cmd, 0)
	})

	t.Run("concurrent_retries_write_one_receipt", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		const workers = 6
		results := make(chan *service.UsageBillingApplyResult, workers)
		errors := make(chan error, workers)
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				copy := *cmd
				result, applyErr := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, &copy)
				results <- result
				errors <- applyErr
			})
		}
		wg.Wait()
		close(results)
		close(errors)
		for applyErr := range errors {
			require.NoError(t, applyErr)
		}
		applied, receiptID := 0, ""
		for result := range results {
			if result.Applied {
				applied++
			}
			if receiptID == "" {
				receiptID = result.BridgeReceipt.ID
			}
			require.Equal(t, receiptID, result.BridgeReceipt.ID)
		}
		require.Equal(t, 1, applied)
		requireBridgeReceiptState(t, cmd, true)
		requireBridgeReceiptCount(t, cmd, 1)
	})

	t.Run("archived_dedup_returns_existing_receipt", func(t *testing.T) {
		cmd := newBridgeReceiptCommand(t)
		first, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `WITH moved AS (
			DELETE FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2
			RETURNING request_id, api_key_id, request_fingerprint, created_at)
			INSERT INTO usage_billing_dedup_archive (request_id, api_key_id, request_fingerprint, created_at)
			SELECT request_id, api_key_id, request_fingerprint, created_at FROM moved`, cmd.RequestID, cmd.APIKeyID)
		require.NoError(t, err)
		second, err := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		require.False(t, second.Applied)
		require.Equal(t, first.BridgeReceipt.ID, second.BridgeReceipt.ID)
		requireBridgeReceiptCount(t, cmd, 1)
		var hotRows int
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", cmd.RequestID, cmd.APIKeyID).Scan(&hotRows))
		require.Zero(t, hotRows)
	})

	t.Run("connection_loss_before_commit_and_recreated_repository", testBridgeReceiptConnectionLoss)
}

func testBridgeReceiptConnectionLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := newBridgeReceiptCommand(t)
	const lockID = 2026110503
	locker, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = locker.Close() }()
	_, err = locker.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockID)
	require.NoError(t, err)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, unlockErr := locker.ExecContext(cleanupCtx, "SELECT pg_advisory_unlock($1)", lockID)
		require.NoError(t, unlockErr)
		_, cleanupErr := integrationDB.ExecContext(cleanupCtx, "DROP FUNCTION IF EXISTS bridge.wait_receipt_commit_for_test() CASCADE")
		require.NoError(t, cleanupErr)
	}()
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION bridge.wait_receipt_commit_for_test() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END; $$;
		CREATE CONSTRAINT TRIGGER wait_receipt_commit_for_test
		AFTER INSERT ON bridge.receipt_outbox DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW WHEN (NEW.core_request_id = '%s') EXECUTE FUNCTION bridge.wait_receipt_commit_for_test();`, lockID, cmd.RequestID))
	require.NoError(t, err)
	failed := make(chan error, 1)
	go func() {
		copy := *cmd
		_, applyErr := NewUsageBillingRepository(nil, integrationDB).Apply(ctx, &copy)
		failed <- applyErr
	}()
	var backendPID int
	require.Eventually(t, func() bool {
		return integrationDB.QueryRowContext(ctx, `SELECT pid FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted AND classid = 0 AND objid = $1 LIMIT 1`, lockID).Scan(&backendPID) == nil
	}, 5*time.Second, 20*time.Millisecond)
	// 真实 Apply 已进入 Commit 的延迟触发器；独立连接仍看不到未提交回执或扣款。
	_, err = custombridge.NewReceiptRepository(integrationDB).Get(ctx, cmd.RequestID, cmd.APIKeyID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	requireBridgeReceiptState(t, cmd, false)
	var terminated bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT pg_terminate_backend($1)", backendPID).Scan(&terminated))
	require.True(t, terminated)
	select {
	case applyErr := <-failed:
		require.Error(t, applyErr)
	case <-ctx.Done():
		t.Fatal("billing did not return after terminating its PostgreSQL connection")
	}
	_, err = integrationDB.ExecContext(ctx, "DROP TRIGGER wait_receipt_commit_for_test ON bridge.receipt_outbox")
	require.NoError(t, err)
	freshDB, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	defer func() { _ = freshDB.Close() }()
	_, err = custombridge.NewReceiptRepository(freshDB).Get(ctx, cmd.RequestID, cmd.APIKeyID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	requireBridgeReceiptState(t, cmd, false)
	result, err := NewUsageBillingRepository(nil, freshDB).Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	requireBridgeReceiptState(t, cmd, true)
	requireBridgeReceiptCount(t, cmd, 1)
}
