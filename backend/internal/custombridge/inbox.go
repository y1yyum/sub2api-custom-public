package custombridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
)

var (
	ErrInboxConflict   = errors.New("bridge inbox event conflicts with committed receipt")
	ErrReceiptEnvelope = errors.New("invalid bridge receipt envelope")
	ErrInboxIncomplete = errors.New("bridge inbox is missing its committed shadow effect")
)

type InboxResult struct {
	Duplicate bool
}

type InboxRepository struct {
	db       *sql.DB
	identity string
}

func NewInboxRepository(ctx context.Context, db *sql.DB) (*InboxRepository, error) {
	if err := checkShadowDatabase(ctx, db); err != nil {
		return nil, err
	}
	r := &InboxRepository{db: db}
	if err := db.QueryRowContext(ctx, `SELECT sink_id FROM bridge_shadow.identity WHERE singleton`).Scan(&r.identity); err != nil {
		return nil, err
	}
	return r, nil
}

type shadowDatabaseQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkShadowDatabase(ctx context.Context, db shadowDatabaseQuerier) error {
	var hasCoreReceipts bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('bridge.receipt_outbox') IS NOT NULL`).Scan(&hasCoreReceipts); err != nil {
		return err
	}
	if hasCoreReceipts {
		return ErrConsumerBinding
	}
	return nil
}

func (r *InboxRepository) Identity() string { return r.identity }

func canonicalReceipt(raw json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, ErrReceiptEnvelope
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, ErrReceiptEnvelope
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, ErrReceiptEnvelope
	}
	return canonical, nil
}

func validateReceiptEnvelope(envelope ReceiptEnvelope) ([]byte, error) {
	if !validRelayName(envelope.Source) || uuid.Validate(envelope.EventID) != nil {
		return nil, ErrReceiptEnvelope
	}
	canonical, err := canonicalReceipt(envelope.Receipt)
	if err != nil || envelope.PayloadDigest != fmt.Sprintf("%x", sha256.Sum256(canonical)) {
		return nil, ErrReceiptEnvelope
	}
	var identity struct {
		ID      string `json:"receipt_id"`
		Version int    `json:"schema_version"`
	}
	if json.Unmarshal(canonical, &identity) != nil || identity.ID != envelope.EventID || identity.Version != ReceiptSchemaVersion {
		return nil, ErrReceiptEnvelope
	}
	return canonical, nil
}

// Consume 只写影子事实；返回成功之前 inbox 与 ledger 已在目标库同事务提交。
func (r *InboxRepository) Consume(ctx context.Context, envelope ReceiptEnvelope) (InboxResult, error) {
	canonical, err := validateReceiptEnvelope(envelope)
	if err != nil {
		return InboxResult{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return InboxResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkShadowDatabase(ctx, tx); err != nil {
		return InboxResult{}, err
	}
	var sinkID string
	if err := tx.QueryRowContext(ctx, `SELECT sink_id FROM bridge_shadow.identity WHERE singleton`).Scan(&sinkID); err != nil {
		return InboxResult{}, err
	}
	if sinkID != r.identity {
		return InboxResult{}, ErrConsumerBinding
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO bridge_shadow.inbox(source, event_id, payload_digest)
		VALUES ($1, $2, $3) ON CONFLICT (source, event_id) DO NOTHING`, envelope.Source, envelope.EventID, envelope.PayloadDigest)
	if err != nil {
		return InboxResult{}, err
	}
	count, err := inserted.RowsAffected()
	if err != nil {
		return InboxResult{}, err
	}
	if count == 0 {
		// ON CONFLICT 可能等候另一个提交；另起 SELECT 获取 READ COMMITTED 的新快照。
		var digest string
		if err := tx.QueryRowContext(ctx, `SELECT payload_digest FROM bridge_shadow.inbox WHERE source = $1 AND event_id = $2`,
			envelope.Source, envelope.EventID).Scan(&digest); err != nil {
			return InboxResult{}, err
		}
		if digest != envelope.PayloadDigest {
			if _, err := tx.ExecContext(ctx, `INSERT INTO bridge_shadow.inbox_conflicts
				(source, event_id, existing_digest, received_digest, receipt_raw) VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (source, event_id, received_digest) DO UPDATE
				SET last_seen_at = clock_timestamp(), occurrences = inbox_conflicts.occurrences + 1`,
				envelope.Source, envelope.EventID, digest, envelope.PayloadDigest, string(canonical)); err != nil {
				return InboxResult{}, err
			}
			if err := tx.Commit(); err != nil {
				return InboxResult{}, err
			}
			return InboxResult{}, ErrInboxConflict
		}
		var complete bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM bridge_shadow.shadow_receipt_ledger
			WHERE source = $1 AND event_id = $2 AND payload_digest = $3 AND receipt = $4::jsonb)`,
			envelope.Source, envelope.EventID, envelope.PayloadDigest, string(canonical)).Scan(&complete); err != nil {
			return InboxResult{}, err
		}
		if !complete {
			return InboxResult{}, ErrInboxIncomplete
		}
		return InboxResult{Duplicate: true}, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO bridge_shadow.shadow_receipt_ledger
		(source, event_id, payload_digest, receipt_raw, receipt) VALUES ($1, $2, $3, $4::text, ($4::text)::jsonb)`,
		envelope.Source, envelope.EventID, envelope.PayloadDigest, string(canonical)); err != nil {
		return InboxResult{}, err
	}
	return InboxResult{}, tx.Commit()
}
