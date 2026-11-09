package dispatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/google/uuid"
)

var ErrProbeReceipt = errors.New("probe receipt does not match the bound attempt")

type ReceiptReader interface {
	ReadReceipt(context.Context, string, string) (*custombridge.Receipt, error)
}

type ShadowReceiptReader struct {
	db     *sql.DB
	source string
	sinkID string
}

func NewShadowReceiptReader(ctx context.Context, db *sql.DB, source string) (*ShadowReceiptReader, error) {
	if !validRef(source) {
		return nil, errors.New("invalid probe receipt source")
	}
	inbox, err := custombridge.NewInboxRepository(ctx, db)
	if err != nil {
		return nil, errors.New("probe shadow receipt database unavailable")
	}
	return &ShadowReceiptReader{db: db, source: source, sinkID: inbox.Identity()}, nil
}

func (r *ShadowReceiptReader) ReadReceipt(ctx context.Context, reservationID, attemptID string) (*custombridge.Receipt, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT l.receipt_raw, l.payload_digest, l.event_id::text
		FROM bridge_shadow.shadow_receipt_ledger l
		JOIN bridge_shadow.inbox i ON i.source=l.source AND i.event_id=l.event_id AND i.payload_digest=l.payload_digest
		JOIN bridge_shadow.identity identity ON identity.singleton AND identity.sink_id=$4::uuid
		WHERE l.source=$1 AND l.receipt->>'reservation_id'=$2 AND l.receipt->>'attempt_id'=$3 LIMIT 2`, r.source, reservationID, attemptID, r.sinkID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var receipt *custombridge.Receipt
	for rows.Next() {
		if receipt != nil {
			return nil, ErrProbeReceipt
		}
		var raw, digest, eventID string
		if err := rows.Scan(&raw, &digest, &eventID); err != nil {
			return nil, err
		}
		actual := sha256.Sum256([]byte(raw))
		if hex.EncodeToString(actual[:]) != digest {
			return nil, ErrProbeReceipt
		}
		var value custombridge.Receipt
		if json.Unmarshal([]byte(raw), &value) != nil || value.ID != eventID || value.SchemaVersion != custombridge.ReceiptSchemaVersion || value.ReservationID != reservationID || value.AttemptID != attemptID {
			return nil, ErrProbeReceipt
		}
		receipt = &value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, sql.ErrNoRows
	}
	return receipt, nil
}

type ProbeRunner struct {
	cfg         ProbeConfig
	credentials ProbeCredentials
	key         custombridge.SigningKey
	origin      *url.URL
	client      *http.Client
	transport   *http.Transport
	receipts    ReceiptReader
}

func NewProbeRunner(cfg ProbeConfig, credentials ProbeCredentials, receipts ReceiptReader) (*ProbeRunner, error) {
	normalized, err := normalizeConfig(Config{Version: 1, CoreInstance: "probe", ConfigRevision: 1, SnapshotFile: "unused", Probe: &cfg})
	if err != nil {
		return nil, err
	}
	cfg = *normalized.Probe
	if receipts == nil {
		return nil, errors.New("probe requires committed shadow receipt evidence")
	}
	key, err := credentials.signingKey(cfg.Audience)
	if err != nil {
		return nil, err
	}
	origin, err := probeOrigin(cfg)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13}, TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
		TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: time.Duration(cfg.RequestTimeoutSeconds) * time.Second,
		MaxIdleConns: 16, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 16, IdleConnTimeout: 30 * time.Second, DisableCompression: true, MaxResponseHeaderBytes: 32 << 10}
	return &ProbeRunner{cfg: cfg, credentials: credentials, key: key, origin: origin, transport: transport, receipts: receipts,
		client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (r *ProbeRunner) Close() { r.transport.CloseIdleConnections() }

func (r *ProbeRunner) Execute(ctx context.Context, lease ProbeLease) Observation {
	start := time.Now()
	observation := Observation{EventID: lease.AttemptID, TargetRef: lease.Target.Ref, AccountID: lease.Target.AccountID, CredentialRevision: lease.Target.CredentialRevision, At: start, Outcome: Unknown}
	executionEnd := minTime(start.Add(time.Duration(r.cfg.RequestTimeoutSeconds)*time.Second), lease.ExpiresAt.Add(-time.Duration(r.cfg.ReceiptWaitSeconds+2)*time.Second))
	if !executionEnd.After(start.Add(5 * time.Second)) {
		return observation
	}
	executionCtx, cancel := context.WithDeadline(ctx, executionEnd)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"model": lease.Target.Model, "input": "Reply only OK", "max_output_tokens": 16, "stream": false})
	request, err := http.NewRequestWithContext(executionCtx, http.MethodPost, r.origin.String(), io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return observation
	}
	request.ContentLength = int64(len(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Authorization", "Bearer "+r.credentials.CoreAPIKey)
	request.Header.Set("User-Agent", "sub2api-dispatch-probe/1")
	digest := sha256.Sum256(body)
	envelope := custombridge.ForwardEnvelope{Version: 1, KID: r.key.ID, Audience: r.cfg.Audience, RequestID: uuid.NewString(),
		ReservationID: lease.ReservationID, AttemptID: lease.AttemptID, InternalKeyID: r.key.InternalKeyID, GroupID: r.key.GroupID, AccountID: lease.Target.AccountID,
		Endpoint: "/v1/responses", CanonicalModel: lease.Target.Model, PayloadSHA256: hex.EncodeToString(digest[:]), IssuedAt: start.Unix(), ExpiresAt: start.Add(5 * time.Second).Unix(), Deadline: executionEnd.Unix(),
		PolicyRevision: lease.Target.PolicyRevision, PoolRef: lease.Target.PoolRef, PayerRef: "dispatch-probe", ActorRef: "dispatch-probe"}
	if custombridge.SignForwardRequest(request, body, envelope, r.key) != nil {
		return observation
	}
	response, err := r.client.Do(request)
	if err != nil {
		return observation
	}
	defer func() { _ = response.Body.Close() }()
	// 本地鉴权、Core 拒绝和无归因的 HTTP 错误均不冒充 Provider 健康证据。
	if response.StatusCode != http.StatusOK {
		return observation
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 || !probeContent(raw, lease.Target.Model) {
		return observation
	}
	observation.LatencyMillis = time.Since(start).Milliseconds()
	cancel()
	receiptEnd := minTime(time.Now().Add(time.Duration(r.cfg.ReceiptWaitSeconds)*time.Second), lease.ExpiresAt.Add(-time.Second))
	receiptCtx, stop := context.WithDeadline(ctx, receiptEnd)
	defer stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		receipt, err := r.receipts.ReadReceipt(receiptCtx, lease.ReservationID, lease.AttemptID)
		if err == nil {
			if receipt == nil || receipt.AccountID != lease.Target.AccountID || receipt.InternalKeyID != r.key.InternalKeyID || receipt.ReservationID != lease.ReservationID || receipt.AttemptID != lease.AttemptID || receipt.SchemaVersion != 1 || receipt.Billing.Model != lease.Target.Model {
				return observation
			}
			observation.Outcome, observation.ProviderEvidence, observation.At = Success, true, time.Now()
			return observation
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return observation
		}
		select {
		case <-receiptCtx.Done():
			return observation
		case <-ticker.C:
		}
	}
}

func probeContent(raw []byte, model string) bool {
	var response struct {
		Model  string `json:"model"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Model != model || response.Status != "completed" {
		return false
	}
	for _, output := range response.Output {
		for _, content := range output.Content {
			if output.Type == "message" && content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				return true
			}
		}
	}
	return false
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
