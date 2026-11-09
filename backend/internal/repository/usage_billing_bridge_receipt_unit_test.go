//go:build unit

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageBillingBridgeReceiptTransaction(t *testing.T) {
	for _, mode := range []string{"ordinary", "commit", "insert_failure", "commit_failure"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			cmd := &service.UsageBillingCommand{RequestID: "core", APIKeyID: 2, UserID: 1, AccountID: 3, BalanceCost: 1.25}
			if mode != "ordinary" {
				cmd.BridgeRequest = &custombridge.Request{ReservationID: "r", AttemptID: "a", AccountID: 3}
				cmd.BridgeUsage = json.RawMessage(`{"input_tokens":1200}`)
			}
			cmd.Normalize()
			mock.ExpectBegin()
			mock.ExpectQuery("INSERT INTO usage_billing_dedup").WithArgs("core", int64(2), cmd.RequestFingerprint).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			mock.ExpectQuery("SELECT request_fingerprint.*usage_billing_dedup_archive").WithArgs("core", int64(2)).
				WillReturnError(sql.ErrNoRows)
			mock.ExpectQuery(conditionalBalanceDeductSQL).WithArgs(1.25, int64(1)).
				WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
			failure := errors.New("injected bridge receipt failure")
			if mode != "ordinary" {
				insert := mock.ExpectQuery("INSERT INTO bridge.receipt_outbox").WithArgs(sqlmock.AnyArg(), "r", "a", "core",
					int64(2), int64(3), int64(1), cmd.RequestFingerprint, string(cmd.BridgeUsage), sqlmock.AnyArg(), custombridge.ReceiptSchemaVersion)
				if mode == "insert_failure" {
					insert.WillReturnError(failure)
				} else {
					insert.WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
				}
			}
			switch mode {
			case "insert_failure":
				mock.ExpectRollback()
			case "commit_failure":
				mock.ExpectCommit().WillReturnError(failure)
			default:
				mock.ExpectCommit()
			}
			result, err := NewUsageBillingRepository(nil, db).Apply(context.Background(), cmd)
			if mode == "insert_failure" || mode == "commit_failure" {
				require.ErrorIs(t, err, failure)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.True(t, result.Applied)
				if mode == "ordinary" {
					require.Nil(t, result.BridgeReceipt)
				} else {
					require.NotEmpty(t, result.BridgeReceipt.ID)
					require.Equal(t, 1.25, result.BridgeReceipt.Billing.BalanceCost)
				}
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageBillingBridgeReceiptDuplicateReadsWithoutSecondEvent(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing_receipt", true: "conflicting_or_missing_receipt"}[conflict], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			cmd := &service.UsageBillingCommand{RequestID: "core", APIKeyID: 2, UserID: 1, AccountID: 3,
				BridgeRequest: &custombridge.Request{ReservationID: "r", AttemptID: "a", AccountID: 3},
				BridgeUsage:   json.RawMessage(`{"input_tokens":1200}`)}
			cmd.Normalize()
			mock.ExpectBegin()
			mock.ExpectQuery("INSERT INTO usage_billing_dedup").WillReturnError(sql.ErrNoRows)
			mock.ExpectQuery("SELECT request_fingerprint.*usage_billing_dedup").
				WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow(cmd.RequestFingerprint))
			read := mock.ExpectQuery("SELECT .*FROM bridge.receipt_outbox").WithArgs("core", int64(2), "r", "a",
				int64(3), int64(1), cmd.RequestFingerprint, string(cmd.BridgeUsage), sqlmock.AnyArg(), custombridge.ReceiptSchemaVersion)
			if conflict {
				read.WillReturnError(sql.ErrNoRows)
			} else {
				read.WillReturnRows(sqlmock.NewRows([]string{"receipt_id", "reservation_id", "attempt_id", "core_request_id",
					"internal_key_id", "account_id", "user_id", "request_fingerprint", "raw_usage", "billing", "created_at", "schema_version"}).
					AddRow("persisted-id", "r", "a", "core", 2, 3, 1, cmd.RequestFingerprint, []byte(cmd.BridgeUsage), []byte(`{}`), time.Now(), 1))
			}
			mock.ExpectRollback()
			result, err := NewUsageBillingRepository(nil, db).Apply(context.Background(), cmd)
			if conflict {
				require.ErrorIs(t, err, custombridge.ErrReceiptConflict)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.False(t, result.Applied)
				require.Equal(t, "persisted-id", result.BridgeReceipt.ID)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageBillingBridgeReceiptAccountMismatchBeforeTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	result, err := NewUsageBillingRepository(nil, db).Apply(context.Background(), &service.UsageBillingCommand{
		RequestID: "core", AccountID: 4, BridgeRequest: &custombridge.Request{AccountID: 3},
	})
	require.ErrorIs(t, err, custombridge.ErrReceiptAccountMismatch)
	require.Nil(t, result)
	require.NoError(t, mock.ExpectationsWereMet())
}
