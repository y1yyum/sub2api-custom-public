package custombridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrReceiptAccountMismatch    = errors.New("bridge receipt account does not match bound account")
	ErrReceiptConflict           = errors.New("bridge receipt is missing or conflicts with billing retry")
	ErrReceiptBillingUnavailable = errors.New("bridge receipt requires transactional usage billing")
)

const ReceiptSchemaVersion = 1

type ReceiptBilling struct {
	Model               string  `json:"model"`
	ServiceTier         string  `json:"service_tier"`
	ReasoningEffort     string  `json:"reasoning_effort"`
	BillingType         int8    `json:"billing_type"`
	SubscriptionID      *int64  `json:"subscription_id,omitempty"`
	BalanceCost         float64 `json:"balance_cost"`
	SubscriptionCost    float64 `json:"subscription_cost"`
	APIKeyQuotaCost     float64 `json:"api_key_quota_cost"`
	APIKeyRateLimitCost float64 `json:"api_key_rate_limit_cost"`
	AccountQuotaCost    float64 `json:"account_quota_cost"`
}

// Receipt 只证明 Core 扣账事务与这份回执一同提交，不代表外部账本已入账。
type Receipt struct {
	ID                 string          `json:"receipt_id"`
	SchemaVersion      int             `json:"schema_version"`
	ReservationID      string          `json:"reservation_id"`
	AttemptID          string          `json:"attempt_id"`
	CoreRequestID      string          `json:"core_request_id"`
	InternalKeyID      int64           `json:"internal_key_id"`
	AccountID          int64           `json:"account_id"`
	UserID             int64           `json:"user_id"`
	RequestFingerprint string          `json:"request_fingerprint"`
	RawUsage           json.RawMessage `json:"raw_usage"`
	Billing            ReceiptBilling  `json:"billing"`
	CreatedAt          time.Time       `json:"created_at"`
}

type ReceiptRepository struct {
	db *sql.DB
}

func NewReceiptRepository(db *sql.DB) *ReceiptRepository {
	return &ReceiptRepository{db: db}
}

const receiptColumns = `receipt_id, reservation_id, attempt_id, core_request_id,
	internal_key_id, account_id, user_id, request_fingerprint, raw_usage, billing, created_at, schema_version`

func (r *ReceiptRepository) Get(ctx context.Context, coreRequestID string, internalKeyID int64) (*Receipt, error) {
	return scanReceipt(r.db.QueryRowContext(ctx, `SELECT `+receiptColumns+`
		FROM bridge.receipt_outbox WHERE core_request_id = $1 AND internal_key_id = $2`, coreRequestID, internalKeyID))
}

// InsertReceipt 必须使用扣账事务；这里不创建事务，也不进行外部投递。
func InsertReceipt(ctx context.Context, tx *sql.Tx, receipt *Receipt) error {
	billing, err := json.Marshal(receipt.Billing)
	if err != nil {
		return fmt.Errorf("encode bridge receipt billing: %w", err)
	}
	receipt.ID = uuid.NewString()
	receipt.SchemaVersion = ReceiptSchemaVersion
	err = tx.QueryRowContext(ctx, `INSERT INTO bridge.receipt_outbox
		(receipt_id, reservation_id, attempt_id, core_request_id, internal_key_id,
		 account_id, user_id, request_fingerprint, raw_usage, billing, schema_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11)
		RETURNING created_at`, receipt.ID, receipt.ReservationID, receipt.AttemptID,
		receipt.CoreRequestID, receipt.InternalKeyID, receipt.AccountID, receipt.UserID,
		receipt.RequestFingerprint, string(receipt.RawUsage), string(billing), ReceiptSchemaVersion).Scan(&receipt.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert bridge receipt: %w", err)
	}
	return nil
}

func ReceiptForRetry(ctx context.Context, tx *sql.Tx, expected *Receipt) (*Receipt, error) {
	billing, err := json.Marshal(expected.Billing)
	if err != nil {
		return nil, fmt.Errorf("encode bridge receipt billing: %w", err)
	}
	receipt, err := scanReceipt(tx.QueryRowContext(ctx, `SELECT `+receiptColumns+`
		FROM bridge.receipt_outbox
		WHERE core_request_id = $1 AND internal_key_id = $2
		  AND reservation_id = $3 AND attempt_id = $4 AND account_id = $5
		  AND user_id = $6 AND request_fingerprint = $7
		  AND raw_usage = $8::jsonb AND billing = $9::jsonb AND schema_version = $10`,
		expected.CoreRequestID, expected.InternalKeyID, expected.ReservationID, expected.AttemptID,
		expected.AccountID, expected.UserID, expected.RequestFingerprint, string(expected.RawUsage), string(billing), ReceiptSchemaVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrReceiptConflict
	}
	return receipt, err
}

func scanReceipt(row *sql.Row) (*Receipt, error) {
	receipt := &Receipt{}
	var billing []byte
	if err := row.Scan(&receipt.ID, &receipt.ReservationID, &receipt.AttemptID,
		&receipt.CoreRequestID, &receipt.InternalKeyID, &receipt.AccountID, &receipt.UserID,
		&receipt.RequestFingerprint, &receipt.RawUsage, &billing, &receipt.CreatedAt, &receipt.SchemaVersion); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(billing, &receipt.Billing); err != nil {
		return nil, fmt.Errorf("decode bridge receipt billing: %w", err)
	}
	return receipt, nil
}
