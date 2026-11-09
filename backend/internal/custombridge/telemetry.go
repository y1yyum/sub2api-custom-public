package custombridge

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrTelemetryFenced  = errors.New("bridge telemetry source fenced")
	ErrTelemetryStorage = errors.New("bridge telemetry storage unavailable")
)

//go:embed migrations/005_telemetry.sql
var telemetrySchema string

//go:embed migrations/006_telemetry_ttft.sql
var telemetryTTFTSchema string

//go:embed migrations/007_telemetry_failure.sql
var telemetryFailureSchema string

//go:embed migrations/008_telemetry_timeout.sql
var telemetryTimeoutSchema string

//go:embed migrations/009_telemetry_protocol.sql
var telemetryProtocolSchema string

func ApplyTelemetrySchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ErrTelemetryStorage
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, telemetrySchema); err != nil {
		return ErrTelemetryStorage
	}
	if _, err := tx.ExecContext(ctx, telemetryTTFTSchema); err != nil {
		return ErrTelemetryStorage
	}
	if _, err := tx.ExecContext(ctx, telemetryFailureSchema); err != nil {
		return ErrTelemetryStorage
	}
	if _, err := tx.ExecContext(ctx, telemetryTimeoutSchema); err != nil {
		return ErrTelemetryStorage
	}
	if _, err := tx.ExecContext(ctx, telemetryProtocolSchema); err != nil {
		return ErrTelemetryStorage
	}
	if err := tx.Commit(); err != nil {
		return ErrTelemetryStorage
	}
	return nil
}

type TelemetrySource struct {
	CoreInstance       string    `json:"core_instance"`
	ConfigRevision     int64     `json:"config_revision"`
	ConfigDigest       string    `json:"config_digest"`
	SlotTTLSeconds     int       `json:"slot_ttl_seconds"`
	ActivatedAt        time.Time `json:"activated_at"`
	ProducerGeneration string    `json:"producer_generation"`
}

type TelemetryEvent struct {
	EventID            string    `json:"event_id"`
	CoreInstance       string    `json:"core_instance"`
	TargetRef          string    `json:"target_ref"`
	ConfigRevision     int64     `json:"config_revision"`
	AccountID          int64     `json:"account_id"`
	CredentialRef      string    `json:"credential_ref"`
	CredentialRevision int64     `json:"credential_revision"`
	ProviderRef        string    `json:"provider_ref"`
	PoolRef            string    `json:"pool_ref"`
	PolicyRevision     string    `json:"policy_revision"`
	Model              string    `json:"model"`
	Region             string    `json:"region"`
	Outcome            string    `json:"outcome"`
	ProviderEvidence   bool      `json:"provider_evidence"`
	RetryAfterSeconds  int       `json:"retry_after_seconds"`
	LatencyMillis      int64     `json:"latency_millis"`
	ObservedAt         time.Time `json:"observed_at"`
	InternalKeyID      int64     `json:"internal_key_id"`
	AttemptID          string    `json:"attempt_id"`
	CoreRequestID      string    `json:"core_request_id"`
	ProducerGeneration string    `json:"producer_generation"`
	TTFTMillis         int64     `json:"ttft_millis,omitempty"`
	FailureClass       string    `json:"failure_class,omitempty"`
	TimedOut           bool      `json:"timed_out,omitempty"`
	Phase              string    `json:"phase,omitempty"`
	ProtocolFact       string    `json:"protocol_fact,omitempty"`
	UsageMissing       bool      `json:"usage_missing,omitempty"`
}

const TelemetryEventColumns = `event_id, core_instance, target_ref, config_revision,
	account_id, credential_ref, credential_revision, provider_ref, pool_ref,
	policy_revision, model, region, outcome, provider_evidence,
	retry_after_seconds, latency_millis, observed_at, internal_key_id,
	attempt_id, core_request_id, producer_generation, ttft_millis, failure_class, timed_out`

// TelemetryProtocolColumns are appended by 009; TelemetryEventColumns stays
// the column order that older writers and readers already depend on.
const TelemetryProtocolColumns = `phase, protocol_fact, usage_missing`

const telemetryWriteColumns = TelemetryEventColumns + `, ` + TelemetryProtocolColumns

type TelemetryProducer struct {
	db      *sql.DB
	source  TelemetrySource
	targets map[telemetryRoute]TelemetryTarget
}

// ActivateTelemetry never applies migrations. Each activation fences all earlier
// process generations, including restarts using identical configuration.
func ActivateTelemetry(ctx context.Context, db *sql.DB, cfg TelemetryConfig, slotTTLSeconds int) (*TelemetryProducer, error) {
	digest, err := cfg.ActivationDigest(slotTTLSeconds)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, ErrTelemetryStorage
	}
	rows, err := db.QueryContext(ctx, `SELECT `+telemetryWriteColumns+` FROM bridge.dispatch_observation LIMIT 0`)
	if err != nil {
		return nil, ErrTelemetryStorage
	}
	if err := rows.Close(); err != nil {
		return nil, ErrTelemetryStorage
	}
	source := TelemetrySource{CoreInstance: cfg.CoreInstance, ConfigRevision: cfg.ConfigRevision,
		ConfigDigest: digest, SlotTTLSeconds: slotTTLSeconds, ProducerGeneration: uuid.NewString()}
	err = db.QueryRowContext(ctx, `INSERT INTO bridge.dispatch_telemetry_source
		(singleton,core_instance,config_revision,config_digest,activated_at,producer_generation,slot_ttl_seconds)
		VALUES (TRUE,$1,$2,$3,clock_timestamp(),$4,$5)
		ON CONFLICT (singleton) DO UPDATE SET config_revision=EXCLUDED.config_revision,
		config_digest=EXCLUDED.config_digest,activated_at=EXCLUDED.activated_at,
		producer_generation=EXCLUDED.producer_generation,slot_ttl_seconds=EXCLUDED.slot_ttl_seconds
		WHERE dispatch_telemetry_source.core_instance=EXCLUDED.core_instance
		AND (EXCLUDED.config_revision>dispatch_telemetry_source.config_revision
		OR (EXCLUDED.config_revision=dispatch_telemetry_source.config_revision
		AND EXCLUDED.config_digest=dispatch_telemetry_source.config_digest))
		RETURNING activated_at`, source.CoreInstance, source.ConfigRevision, source.ConfigDigest, source.ProducerGeneration, source.SlotTTLSeconds).Scan(&source.ActivatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTelemetryFenced
	}
	if err != nil {
		return nil, ErrTelemetryStorage
	}
	producer := &TelemetryProducer{db: db, source: source, targets: make(map[telemetryRoute]TelemetryTarget, len(cfg.Targets))}
	for _, target := range cfg.Targets {
		target.ErrorRules = canonicalTelemetryErrorRules(target.ErrorRules)
		producer.targets[target.route()] = target
	}
	return producer, nil
}

func (p *TelemetryProducer) Source() TelemetrySource { return p.source }

type telemetryProducerKey struct{}
type telemetryAttemptKey struct{}

func WithTelemetryProducer(ctx context.Context, producer *TelemetryProducer) context.Context {
	return context.WithValue(ctx, telemetryProducerKey{}, producer)
}

func TelemetryProducerFromContext(ctx context.Context) *TelemetryProducer {
	producer, _ := ctx.Value(telemetryProducerKey{}).(*TelemetryProducer)
	return producer
}

// Begin must only receive the envelope authenticated by BridgeForward.
func (p *TelemetryProducer) Begin(ctx context.Context, envelope ForwardEnvelope, coreRequestID string) (context.Context, *TelemetryAttempt) {
	if p == nil {
		return ctx, nil
	}
	target, ok := p.targets[telemetryRoute{envelope.AccountID, envelope.PoolRef, envelope.PolicyRevision, envelope.CanonicalModel}]
	if !ok {
		return ctx, nil
	}
	attempt := &TelemetryAttempt{producer: p, target: target, envelope: envelope, coreRequestID: coreRequestID}
	return context.WithValue(ctx, telemetryAttemptKey{}, attempt), attempt
}

type telemetryHTTPFacts struct {
	count             int
	matched           bool
	status            int
	retryAfter        int
	headerDuration    time.Duration
	ttft              int64
	recordedAt        time.Time
	readFailed        bool
	complete          bool
	responseCompleted bool
	clientCanceled    bool
	failureClass      string
	// Protocol facts describe the same single outbound for reports only; none of
	// them can turn a response into success or into provider evidence.
	credentialMatched bool
	transportFailed   bool
	timeout           bool
	streamed          bool
	firstContent      bool
	streamBreak       bool
	terminal          string
	usagePresent      bool
}

type TelemetryAttempt struct {
	producer      *TelemetryProducer
	target        TelemetryTarget
	envelope      ForwardEnvelope
	coreRequestID string
	mu            sync.Mutex
	facts         telemetryHTTPFacts
	finished      bool
	once          sync.Once
	finishErr     error
}

type TelemetryHTTP struct {
	attempt *TelemetryAttempt
	matched bool
}

// PrepareTelemetryHTTP snapshots credentials before the actual outbound call.
// Neither credentials nor raw errors/headers are retained by the observer.
func PrepareTelemetryHTTP(ctx context.Context, accountID int64, credentials map[string]any) *TelemetryHTTP {
	attempt, _ := ctx.Value(telemetryAttemptKey{}).(*TelemetryAttempt)
	if attempt == nil {
		return nil
	}
	digest, err := TelemetryCredentialSHA256(credentials)
	return &TelemetryHTTP{attempt: attempt, matched: err == nil && accountID == attempt.target.AccountID && digest == attempt.target.CredentialSHA256}
}

func (h *TelemetryHTTP) Record(response *http.Response, transportErr error, latency time.Duration) {
	if h == nil {
		return
	}
	a := h.attempt
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.facts.count++
	a.facts.matched = h.matched && transportErr == nil && response != nil
	a.facts.credentialMatched = h.matched
	a.facts.transportFailed = transportErr != nil || response == nil
	a.facts.timeout = telemetryTimeout(transportErr)
	a.facts.headerDuration = min(max(latency, 0), 900000*time.Millisecond)
	a.facts.recordedAt = time.Now()
	if response == nil || transportErr != nil {
		return
	}
	a.facts.status = response.StatusCode
	a.facts.retryAfter = telemetryRetryAfter(response.Header.Get("Retry-After"), a.facts.recordedAt)
	if response.Body != nil {
		observe := response.StatusCode >= 200 && response.StatusCode <= 299 ||
			len(a.target.ErrorRules) > 0 && (response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests)
		response.Body = newTelemetryBody(response.Body, a, response.Header.Get("Content-Type"), a.facts.matched && observe)
	}
}

// Complete marks native processing only; upstream completion and the committed
// receipt are independent requirements for success.
func (h *TelemetryHTTP) Complete(clientCanceled bool) {
	if h == nil {
		return
	}
	h.attempt.mu.Lock()
	defer h.attempt.mu.Unlock()
	if h.attempt.finished {
		return
	}
	h.attempt.facts.complete = true
	h.attempt.facts.clientCanceled = h.attempt.facts.clientCanceled || clientCanceled
}

func telemetryTimeout(err error) bool {
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout()
}

func telemetryRetryAfter(value string, now time.Time) int {
	if len(value) > 128 {
		return 0
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err == nil {
		return int(min(max(seconds, 0), 600))
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return int(min(max(int64(when.Sub(now).Seconds()), 0), 600))
}

func (a *TelemetryAttempt) Finish(ctx context.Context, localStatus int, clientErr error) error {
	if a == nil {
		return nil
	}
	a.once.Do(func() {
		a.mu.Lock()
		a.finished = true
		facts := a.facts
		a.mu.Unlock()
		event := a.event(facts, localStatus, clientErr)
		a.finishErr = a.producer.persist(ctx, a.envelope, event)
	})
	return a.finishErr
}

func (a *TelemetryAttempt) event(f telemetryHTTPFacts, localStatus int, clientErr error) TelemetryEvent {
	t := a.target
	e := TelemetryEvent{EventID: uuid.NewString(), CoreInstance: a.producer.source.CoreInstance, TargetRef: t.TargetRef,
		ConfigRevision: a.producer.source.ConfigRevision, AccountID: t.AccountID, CredentialRef: t.CredentialRef,
		CredentialRevision: t.CredentialRevision, ProviderRef: t.ProviderRef, PoolRef: t.PoolRef, PolicyRevision: t.PolicyRevision,
		Model: t.Model, Region: t.Region, Outcome: "unknown", ObservedAt: time.Now().UTC(),
		InternalKeyID: a.envelope.InternalKeyID, AttemptID: a.envelope.AttemptID, CoreRequestID: a.coreRequestID,
		ProducerGeneration: a.producer.source.ProducerGeneration}
	if !f.recordedAt.IsZero() {
		duration := f.headerDuration + min(max(time.Since(f.recordedAt), 0), 900000*time.Millisecond)
		e.LatencyMillis = min(duration.Milliseconds(), 900000)
	}
	single := f.count == 1 && f.credentialMatched
	if single {
		e.Phase = f.phase()
	}
	if clientErr != nil || f.clientCanceled {
		e.Outcome = "client_canceled"
		// Edge and Core share the signed absolute deadline; a plain cancellation
		// cannot be attributed to either side.
		if errors.Is(clientErr, context.DeadlineExceeded) {
			e.ProtocolFact = "bridge_deadline"
		}
		return e
	}
	if f.count == 0 {
		if localStatus >= 400 {
			e.Outcome = "local_rejection"
		}
		return e
	}
	if single {
		e.ProtocolFact, e.UsageMissing = f.protocolFact()
	}
	if f.count != 1 || !f.matched || f.readFailed {
		return e
	}
	switch {
	case f.status == http.StatusUnauthorized:
		e.Outcome = "upstream_auth"
	case f.status == http.StatusTooManyRequests:
		e.Outcome, e.RetryAfterSeconds = "upstream_rate_limit", f.retryAfter
		e.FailureClass = f.failureClass
	case f.status == http.StatusForbidden && f.failureClass != "":
		e.Outcome, e.FailureClass = "upstream_auth", f.failureClass
	case f.status >= 500 && f.status <= 599:
		e.Outcome = "upstream_failure"
		e.TimedOut = f.status == http.StatusGatewayTimeout
	case f.status >= 200 && f.status <= 299 && f.complete && f.responseCompleted && localStatus >= 200 && localStatus <= 299:
		e.Outcome = "success"
		e.TTFTMillis = f.ttft
	default:
		return e
	}
	e.ProviderEvidence = true
	return e
}

// phase is the furthest point the single outbound visibly reached.
func (f telemetryHTTPFacts) phase() string {
	switch {
	case f.transportFailed:
		return "connect"
	case f.terminal != "":
		return "terminal"
	case f.firstContent:
		return "content"
	default:
		return "headers"
	}
}

func (f telemetryHTTPFacts) protocolFact() (string, bool) {
	completed := f.terminal == "completed" && f.responseCompleted
	switch {
	case f.transportFailed || f.streamBreak:
		if f.timeout {
			return "local_timeout", false
		}
		if f.streamBreak {
			return "stream_break", false
		}
		return "", false
	case f.terminal == "failed":
		return "semantic_failure", false
	case f.terminal == "incomplete":
		return "incomplete", false
	case completed && f.streamed && !f.firstContent:
		return "non_text_terminal", !f.usagePresent
	default:
		return "", completed && !f.usagePresent
	}
}

func (p *TelemetryProducer) persist(ctx context.Context, envelope ForwardEnvelope, event TelemetryEvent) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrTelemetryStorage
	}
	defer func() { _ = tx.Rollback() }()
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT singleton FROM bridge.dispatch_telemetry_source
		WHERE singleton AND core_instance=$1 AND config_revision=$2 AND config_digest=$3
		AND producer_generation=$4 AND slot_ttl_seconds=$5 FOR SHARE`, p.source.CoreInstance, p.source.ConfigRevision,
		p.source.ConfigDigest, p.source.ProducerGeneration, p.source.SlotTTLSeconds).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTelemetryFenced
	}
	if err != nil {
		return ErrTelemetryStorage
	}
	if event.Outcome == "success" {
		var committed bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM bridge.receipt_outbox o
			JOIN bridge.forward_attempt a ON a.internal_key_id=o.internal_key_id AND a.core_request_id=o.core_request_id
			AND a.attempt_id=o.attempt_id AND a.reservation_id=o.reservation_id AND a.account_id=o.account_id
			WHERE o.internal_key_id=$1 AND o.attempt_id=$2 AND o.reservation_id=$3 AND o.core_request_id=$4
			AND o.account_id=$5 AND o.billing->>'model'=$6 AND o.schema_version=1 AND a.state='send_intent')`,
			event.InternalKeyID, event.AttemptID, envelope.ReservationID, event.CoreRequestID, event.AccountID, event.Model).Scan(&committed)
		if err != nil {
			return ErrTelemetryStorage
		}
		if !committed {
			event.Outcome, event.ProviderEvidence = "unknown", false
			event.TTFTMillis = 0
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO bridge.dispatch_observation (`+telemetryWriteColumns+`)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,clock_timestamp(),$17,$18,$19,$20,$22,$23,$24,$25,$26,$27
		FROM bridge.forward_attempt a WHERE a.internal_key_id=$17 AND a.attempt_id=$18 AND a.core_request_id=$19
		AND a.account_id=$5 AND a.reservation_id=$21 AND a.envelope->>'canonical_model'=$11
		AND a.envelope->>'pool_ref'=$9 AND a.envelope->>'policy_revision'=$10
		AND (NOT $14 OR a.state='send_intent')
		ON CONFLICT (internal_key_id,attempt_id) DO NOTHING`,
		event.EventID, event.CoreInstance, event.TargetRef, event.ConfigRevision, event.AccountID,
		event.CredentialRef, event.CredentialRevision, event.ProviderRef, event.PoolRef, event.PolicyRevision,
		event.Model, event.Region, event.Outcome, event.ProviderEvidence, event.RetryAfterSeconds, event.LatencyMillis,
		event.InternalKeyID, event.AttemptID, event.CoreRequestID, event.ProducerGeneration, envelope.ReservationID, event.TTFTMillis, event.FailureClass, event.TimedOut,
		event.Phase, event.ProtocolFact, event.UsageMissing)
	if err != nil {
		return ErrTelemetryStorage
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ErrTelemetryStorage
	}
	if count == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM bridge.dispatch_observation WHERE internal_key_id=$1 AND attempt_id=$2)`, event.InternalKeyID, event.AttemptID).Scan(&exists); err != nil || !exists {
			return ErrTelemetryStorage
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrTelemetryStorage
	}
	return nil
}
