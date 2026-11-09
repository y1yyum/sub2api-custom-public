package custombridge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrAttemptReplay   = errors.New("bridge attempt already exists; forwarding is not replayed")
	ErrAttemptConflict = errors.New("bridge attempt conflicts with its original request")
)

//go:embed migrations/004_forward_attempt.sql
var forwardAttemptSchema string

func ApplyForwardSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, forwardAttemptSchema); err != nil {
		return fmt.Errorf("apply bridge forward schema: %w", err)
	}
	return tx.Commit()
}

type AttemptStore interface {
	Receive(context.Context, ForwardEnvelope, string) error
	ClaimSend(context.Context, int64, string) error
	Finish(context.Context, int64, string) error
}

type ForwardAttemptRepository struct{ db *sql.DB }

func NewForwardAttemptRepository(db *sql.DB) *ForwardAttemptRepository {
	return &ForwardAttemptRepository{db: db}
}

// ForwardDigest 排除每次签名的时间、kid 和外侧请求 ID，保留正文与授权快照。
func ForwardDigest(e ForwardEnvelope) string {
	e.IssuedAt, e.ExpiresAt = 0, 0
	e.Deadline = 0
	e.KID, e.RequestID = "", ""
	encoded, _ := json.Marshal(e)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (r *ForwardAttemptRepository) Receive(ctx context.Context, envelope ForwardEnvelope, coreRequestID string) error {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	digest := ForwardDigest(envelope)
	result, err := r.db.ExecContext(ctx, `INSERT INTO bridge.forward_attempt
		(internal_key_id, attempt_id, reservation_id, account_id, core_request_id, request_digest, envelope)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb) ON CONFLICT DO NOTHING`,
		envelope.InternalKeyID, envelope.AttemptID, envelope.ReservationID, envelope.AccountID, coreRequestID, digest, string(raw))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	var existing string
	err = r.db.QueryRowContext(ctx, `SELECT request_digest FROM bridge.forward_attempt WHERE internal_key_id=$1 AND attempt_id=$2`, envelope.InternalKeyID, envelope.AttemptID).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAttemptConflict
	}
	if err != nil {
		return err
	}
	if existing != digest {
		return ErrAttemptConflict
	}
	return ErrAttemptReplay
}

// ClaimSend 提交后不允许自动接管；进程崩溃也不重置这份出站意图。
func (r *ForwardAttemptRepository) ClaimSend(ctx context.Context, internalKeyID int64, attemptID string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE bridge.forward_attempt SET state='send_intent', send_intent_at=NOW()
		WHERE internal_key_id=$1 AND attempt_id=$2 AND state='received'`, internalKeyID, attemptID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrAttemptReplay
	}
	return nil
}

func (r *ForwardAttemptRepository) Finish(ctx context.Context, internalKeyID int64, attemptID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE bridge.forward_attempt SET state='rejected', finished_at=NOW()
		WHERE internal_key_id=$1 AND attempt_id=$2 AND state='received'`, internalKeyID, attemptID)
	return err
}

type ForwardAttemptStatus struct {
	Envelope      ForwardEnvelope
	CoreRequestID string
	State         string
	ReceiptID     string
	ReceivedAt    time.Time
	SendIntentAt  sql.NullTime
}

func (r *ForwardAttemptRepository) Get(ctx context.Context, internalKeyID int64, attemptID string) (*ForwardAttemptStatus, error) {
	status := &ForwardAttemptStatus{}
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT a.envelope, a.core_request_id,
		CASE WHEN o.receipt_id IS NOT NULL THEN 'core_committed'
		     WHEN a.state='send_intent' THEN 'reconciliation_required' ELSE a.state END,
		COALESCE(o.receipt_id::text, ''), a.received_at, a.send_intent_at
		FROM bridge.forward_attempt a LEFT JOIN bridge.receipt_outbox o
		ON o.core_request_id=a.core_request_id AND o.internal_key_id=a.internal_key_id
		WHERE a.internal_key_id=$1 AND a.attempt_id=$2`, internalKeyID, attemptID).
		Scan(&raw, &status.CoreRequestID, &status.State, &status.ReceiptID, &status.ReceivedAt, &status.SendIntentAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &status.Envelope); err != nil {
		return nil, err
	}
	return status, nil
}

type sendGuardKey struct{}
type SendGuard func(context.Context) error

func WithSendGuard(ctx context.Context, guard SendGuard) context.Context {
	return context.WithValue(ctx, sendGuardKey{}, guard)
}

func ClaimSendIntent(ctx context.Context) error {
	guard, _ := ctx.Value(sendGuardKey{}).(SendGuard)
	if guard == nil {
		return nil
	}
	return guard(ctx)
}
