package custombridge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrConsumerBinding = errors.New("bridge consumer source or sink identity mismatch")
	ErrDeliveryLease   = errors.New("bridge delivery lease expired or superseded")
	ErrDeliveryState   = errors.New("bridge delivery is not a dead letter")
)

type ReceiptEnvelope struct {
	Source        string          `json:"source"`
	EventID       string          `json:"event_id"`
	PayloadDigest string          `json:"payload_digest"`
	Receipt       json.RawMessage `json:"receipt"`
}

type ConsumerBinding struct {
	ConsumerID string
	SourceID   string
	SinkID     string
}

type RelayConfig struct {
	Binding       ConsumerBinding
	LeaseDuration time.Duration
	MaxAttempts   int
	RetryMin      time.Duration
	RetryMax      time.Duration
}

func DefaultRelayConfig(binding ConsumerBinding) RelayConfig {
	return RelayConfig{Binding: binding, LeaseDuration: 30 * time.Second, MaxAttempts: 8,
		RetryMin: time.Second, RetryMax: time.Minute}
}

type DeliveryLease struct {
	ConsumerID string
	EventID    string
	Token      string
	Generation int64
	Until      time.Time
}

type Delivery struct {
	Lease    DeliveryLease
	Envelope ReceiptEnvelope
}

type RelayRepository struct {
	db     *sql.DB
	config RelayConfig
}

func NewRelayRepository(db *sql.DB, config RelayConfig) (*RelayRepository, error) {
	if db == nil || !validRelayName(config.Binding.ConsumerID) || !validRelayName(config.Binding.SourceID) ||
		uuid.Validate(config.Binding.SinkID) != nil || config.LeaseDuration < time.Millisecond ||
		config.LeaseDuration > time.Hour || config.MaxAttempts < 1 || config.MaxAttempts > 1000 ||
		config.RetryMin < time.Millisecond || config.RetryMax < config.RetryMin || config.RetryMax > 24*time.Hour {
		return nil, errors.New("invalid bridge relay configuration")
	}
	return &RelayRepository{db: db, config: config}, nil
}

func validRelayName(value string) bool {
	return len(value) > 0 && len(value) <= 200 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

// RegisterConsumer 固定来源和目标身份；更换目标必须使用新的消费者 ID。
func (r *RelayRepository) RegisterConsumer(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	b := r.config.Binding
	if _, err := tx.ExecContext(ctx, `INSERT INTO bridge.relay_source_identity(singleton, source_id)
		VALUES (true, $1) ON CONFLICT (singleton) DO NOTHING`, b.SourceID); err != nil {
		return err
	}
	var source string
	if err := tx.QueryRowContext(ctx, `SELECT source_id FROM bridge.relay_source_identity WHERE singleton`).Scan(&source); err != nil {
		return err
	}
	if source != b.SourceID {
		return ErrConsumerBinding
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO bridge.receipt_consumers(consumer_id, source_id, sink_id)
		VALUES ($1, $2, $3) ON CONFLICT (consumer_id) DO NOTHING`, b.ConsumerID, b.SourceID, b.SinkID); err != nil {
		return err
	}
	if err := r.checkBinding(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *RelayRepository) checkBinding(ctx context.Context, tx *sql.Tx) error {
	var source, sink string
	err := tx.QueryRowContext(ctx, `SELECT c.source_id, c.sink_id FROM bridge.receipt_consumers c
		JOIN bridge.relay_source_identity s ON s.singleton AND s.source_id = c.source_id
		WHERE c.consumer_id = $1`, r.config.Binding.ConsumerID).Scan(&source, &sink)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConsumerBinding
	}
	if err != nil {
		return err
	}
	if source != r.config.Binding.SourceID || sink != r.config.Binding.SinkID {
		return ErrConsumerBinding
	}
	return nil
}

// Claim 每次只领取一条，提交 lease 后返回，调用方不在持锁时访问目标库。
func (r *RelayRepository) Claim(ctx context.Context) (*Delivery, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '2s'; SET LOCAL statement_timeout = '5s'`); err != nil {
		return nil, err
	}
	if err := r.checkBinding(ctx, tx); err != nil {
		return nil, err
	}
	consumer := r.config.Binding.ConsumerID
	// 每轮扫描缺失行，不用时间或序号高水位，较早开始而迟提交的事实仍会被发现。
	if _, err := tx.ExecContext(ctx, `INSERT INTO bridge.receipt_delivery(consumer_id, receipt_id)
		SELECT $1, o.receipt_id FROM bridge.receipt_outbox o
		WHERE NOT EXISTS (SELECT 1 FROM bridge.receipt_delivery d WHERE d.consumer_id = $1 AND d.receipt_id = o.receipt_id)
		ORDER BY o.created_at, o.receipt_id LIMIT 64 ON CONFLICT DO NOTHING`, consumer); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `WITH exhausted AS (
		SELECT consumer_id, receipt_id FROM bridge.receipt_delivery
		WHERE consumer_id = $1 AND attempts >= $2 AND state IN ('pending', 'leased')
		AND (state = 'pending' OR lease_until <= clock_timestamp())
		ORDER BY receipt_id LIMIT 64 FOR UPDATE SKIP LOCKED
	), changed AS (
		UPDATE bridge.receipt_delivery d SET state = 'dead_letter', lease_token = NULL, lease_until = NULL,
		dead_letter_at = clock_timestamp(), last_error_code = 'attempts_exhausted'
		FROM exhausted e WHERE d.consumer_id = e.consumer_id AND d.receipt_id = e.receipt_id
		RETURNING d.consumer_id, d.receipt_id, d.lease_generation
	) INSERT INTO bridge.receipt_delivery_audit(consumer_id, receipt_id, lease_generation, action, reason)
		SELECT consumer_id, receipt_id, lease_generation, 'dead_letter', 'attempts_exhausted' FROM changed`, consumer, r.config.MaxAttempts); err != nil {
		return nil, err
	}
	delivery := &Delivery{}
	lease := &delivery.Lease
	lease.ConsumerID = consumer
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT d.receipt_id, d.lease_generation,
		(to_jsonb(o) || jsonb_build_object('created_at', to_char(o.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')))::text
		FROM bridge.receipt_delivery d JOIN bridge.receipt_outbox o ON o.receipt_id = d.receipt_id
		WHERE d.consumer_id = $1 AND d.attempts < $2 AND
		((d.state = 'pending' AND d.next_retry_at <= clock_timestamp()) OR
		 (d.state = 'leased' AND d.lease_until <= clock_timestamp()))
		ORDER BY d.next_retry_at, o.created_at, d.receipt_id LIMIT 1 FOR UPDATE OF d SKIP LOCKED`, consumer, r.config.MaxAttempts).
		Scan(&lease.EventID, &lease.Generation, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	lease.Generation++
	lease.Token = uuid.NewString()
	err = tx.QueryRowContext(ctx, `UPDATE bridge.receipt_delivery SET state = 'leased', lease_token = $3,
		lease_generation = $4, lease_until = clock_timestamp() + $5::bigint * interval '1 microsecond',
		attempts = attempts + 1, total_attempts = total_attempts + 1
		WHERE consumer_id = $1 AND receipt_id = $2 RETURNING lease_until`, consumer, lease.EventID, lease.Token,
		lease.Generation, r.config.LeaseDuration.Microseconds()).Scan(&lease.Until)
	if err != nil {
		return nil, err
	}
	if err := writeDeliveryAudit(ctx, tx, *lease, "claimed", ""); err != nil {
		return nil, err
	}
	delivery.Envelope, err = NewReceiptEnvelope(r.config.Binding.SourceID, lease.EventID, raw)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return delivery, nil
}

func (r *RelayRepository) Ack(ctx context.Context, lease DeliveryLease) error {
	return r.finish(ctx, lease, "", false)
}

func (r *RelayRepository) Fail(ctx context.Context, lease DeliveryLease, code string, permanent bool) error {
	if !validRelayName(code) {
		return errors.New("invalid bridge failure code")
	}
	return r.finish(ctx, lease, code, permanent)
}

func (r *RelayRepository) finish(ctx context.Context, lease DeliveryLease, code string, permanent bool) error {
	if lease.ConsumerID != r.config.Binding.ConsumerID {
		return ErrDeliveryLease
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.checkBinding(ctx, tx); err != nil {
		return err
	}
	var attempts int
	err = tx.QueryRowContext(ctx, `SELECT attempts FROM bridge.receipt_delivery WHERE consumer_id = $1 AND receipt_id = $2
		AND state = 'leased' AND lease_token = $3 AND lease_generation = $4 AND lease_until > clock_timestamp() FOR UPDATE`,
		lease.ConsumerID, lease.EventID, lease.Token, lease.Generation).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDeliveryLease
	}
	if err != nil {
		return err
	}
	state, action := "acked", "acked"
	if code != "" {
		state, action = "pending", "retry"
		if permanent || attempts >= r.config.MaxAttempts {
			state, action = "dead_letter", "dead_letter"
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE bridge.receipt_delivery SET state = $5, lease_token = NULL, lease_until = NULL,
		next_retry_at = clock_timestamp() + $6::bigint * interval '1 microsecond', last_error_code = $7,
		acked_at = CASE WHEN $5 = 'acked' THEN clock_timestamp() ELSE NULL END,
		dead_letter_at = CASE WHEN $5 = 'dead_letter' THEN clock_timestamp() ELSE NULL END
		WHERE consumer_id = $1 AND receipt_id = $2 AND lease_token = $3 AND lease_generation = $4
		AND state = 'leased' AND lease_until > clock_timestamp()`, lease.ConsumerID, lease.EventID, lease.Token,
		lease.Generation, state, r.backoff(attempts).Microseconds(), code)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrDeliveryLease
	}
	if err := writeDeliveryAudit(ctx, tx, lease, action, code); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *RelayRepository) backoff(attempts int) time.Duration {
	delay := r.config.RetryMin
	for n := 1; n < attempts && delay < r.config.RetryMax; n++ {
		if delay > r.config.RetryMax/2 {
			return r.config.RetryMax
		}
		delay *= 2
	}
	return min(delay, r.config.RetryMax)
}

func (r *RelayRepository) Replay(ctx context.Context, eventID, reason string) error {
	if uuid.Validate(eventID) != nil || !validRelayName(reason) {
		return errors.New("invalid bridge replay event or reason")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.checkBinding(ctx, tx); err != nil {
		return err
	}
	lease := DeliveryLease{ConsumerID: r.config.Binding.ConsumerID, EventID: eventID}
	err = tx.QueryRowContext(ctx, `UPDATE bridge.receipt_delivery SET state = 'pending', attempts = 0,
		replay_count = replay_count + 1, next_retry_at = clock_timestamp(), dead_letter_at = NULL,
		lease_generation = lease_generation + 1
		WHERE consumer_id = $1 AND receipt_id = $2 AND state = 'dead_letter' RETURNING lease_generation`,
		lease.ConsumerID, eventID).Scan(&lease.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDeliveryState
	}
	if err != nil {
		return err
	}
	if err := writeDeliveryAudit(ctx, tx, lease, "replay", reason); err != nil {
		return err
	}
	return tx.Commit()
}

func writeDeliveryAudit(ctx context.Context, tx *sql.Tx, lease DeliveryLease, action, reason string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO bridge.receipt_delivery_audit(consumer_id, receipt_id, lease_generation, action, reason)
		VALUES ($1, $2, $3, $4, $5)`, lease.ConsumerID, lease.EventID, lease.Generation, action, reason)
	return err
}

func NewReceiptEnvelope(source, eventID string, receipt json.RawMessage) (ReceiptEnvelope, error) {
	canonical, err := canonicalReceipt(receipt)
	if err != nil {
		return ReceiptEnvelope{}, err
	}
	return ReceiptEnvelope{Source: source, EventID: eventID, Receipt: canonical,
		PayloadDigest: fmt.Sprintf("%x", sha256.Sum256(canonical))}, nil
}

type ReceiptSink interface {
	Identity() string
	Consume(context.Context, ReceiptEnvelope) (InboxResult, error)
}

type Relay struct {
	Source *RelayRepository
	Sink   ReceiptSink
}

type DeliveryResult struct {
	EventID   string
	Delivered bool
	Duplicate bool
	Failed    bool
}

func (r *Relay) DeliverOnce(ctx context.Context) (DeliveryResult, error) {
	if r.Source == nil || r.Sink == nil || r.Source.config.Binding.SinkID != r.Sink.Identity() {
		return DeliveryResult{}, ErrConsumerBinding
	}
	delivery, err := r.Source.Claim(ctx)
	if err != nil || delivery == nil {
		return DeliveryResult{}, err
	}
	result := DeliveryResult{EventID: delivery.Envelope.EventID}
	consumed, err := r.Sink.Consume(ctx, delivery.Envelope)
	if err != nil {
		code, permanent := "sink_unavailable", false
		if errors.Is(err, ErrInboxConflict) {
			code, permanent = "payload_conflict", true
		} else if errors.Is(err, ErrReceiptEnvelope) {
			code, permanent = "invalid_receipt", true
		} else if errors.Is(err, ErrConsumerBinding) {
			code, permanent = "sink_identity_mismatch", true
		}
		result.Failed = true
		if failErr := r.Source.Fail(ctx, delivery.Lease, code, permanent); failErr != nil {
			return result, errors.Join(err, failErr)
		}
		return result, err
	}
	if err := r.Source.Ack(ctx, delivery.Lease); err != nil {
		return result, err
	}
	result.Delivered, result.Duplicate = true, consumed.Duplicate
	return result, nil
}
