package edge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
)

const edgeRequestIDHeader = "X-Edge-Request-ID"

type Handler struct {
	cfg       Config
	core      *url.URL
	client    *http.Client
	transport *http.Transport
	grants    map[string]Grant
	keys      map[string]custombridge.SigningKey
	shadow    *shadowDispatch
	strict    *strictDispatch
	permits   map[string]*dispatch.PermitSet
}

func NewHandler(cfg Config) (*Handler, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	origin, err := coreURL(cfg)
	if err != nil {
		return nil, err
	}
	origin.Path = custombridge.ForwardPath
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: time.Duration(cfg.DialTimeoutSeconds) * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSNextProto:           make(map[string]func(string, *tls.Conn) http.RoundTripper),
		TLSHandshakeTimeout:    time.Duration(cfg.DialTimeoutSeconds) * time.Second,
		ResponseHeaderTimeout:  time.Duration(cfg.ResponseHeaderTimeoutSeconds) * time.Second,
		MaxIdleConns:           32,
		MaxIdleConnsPerHost:    32,
		MaxConnsPerHost:        128,
		IdleConnTimeout:        90 * time.Second,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	handler := &Handler{
		cfg: cfg, core: origin, transport: transport,
		client:  &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		grants:  make(map[string]Grant, len(cfg.Grants)),
		keys:    make(map[string]custombridge.SigningKey, len(cfg.SigningKeys)),
		permits: make(map[string]*dispatch.PermitSet, len(cfg.Grants)),
	}
	for _, key := range cfg.SigningKeys {
		secret, _ := base64.StdEncoding.Strict().DecodeString(key.SecretBase64)
		handler.keys[key.ID] = custombridge.SigningKey{ID: key.ID, Secret: secret, InternalKeyID: key.InternalKeyID, GroupID: key.GroupID}
	}
	for _, grant := range cfg.Grants {
		handler.grants[grant.BearerSHA256] = grant
		if cfg.StrictDispatch != nil {
			// normalizeConfig 已验证并冻结授权；运行时不接受客户端目标。
			handler.permits[grant.ID], _ = dispatch.NewPermitSet(grant.StrictTargets)
		}
	}
	handler.cfg.SigningKeys, handler.cfg.Grants = nil, nil
	if cfg.ShadowDispatch != nil {
		handler.shadow = newShadowDispatch(*cfg.ShadowDispatch)
	}
	if cfg.StrictDispatch != nil {
		handler.strict = newStrictDispatch(*cfg.StrictDispatch)
	}
	return handler, nil
}

func (h *Handler) Close() {
	h.shadow.close()
	h.strict.close()
	h.transport.CloseIdleConnections()
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.EscapedPath() != "/v1/responses" || request.URL.RawQuery != "" || request.URL.ForceQuery {
		writeError(writer, http.StatusNotFound, "not_found")
		return
	}
	grant, ok := h.authorize(request.Header)
	if !ok {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		writeError(writer, http.StatusUnauthorized, "invalid_api_key")
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "unsupported_content_type")
		return
	}
	if request.Header.Get("Content-Encoding") != "" || request.Header.Get("Upgrade") != "" {
		writeError(writer, http.StatusBadRequest, "unsupported_transport")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, h.cfg.MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large")
		} else {
			writeError(writer, http.StatusBadRequest, "invalid_request_body")
		}
		return
	}
	validate := custombridge.ValidateResponsesBody
	if h.strict != nil {
		validate = validateStrictResponsesBody
	}
	model, err := validate(body)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "unsupported_responses_request")
		return
	}
	requestID, reservationID, attemptID, err := requestIdentity(grant.ID, request.Header.Values("Idempotency-Key"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	h.shadow.observe(grant, model, time.Now())
	accountID := grant.AccountID
	if h.strict != nil {
		decision, release := h.strict.acquire(grant, model, h.permits[grant.ID], time.Now())
		if release == nil {
			writeError(writer, http.StatusServiceUnavailable, "strict_dispatch_unavailable")
			return
		}
		defer release()
		accountID = decision.AccountID
	}
	writer.Header().Set(edgeRequestIDHeader, requestID)
	now := time.Now().Unix()
	deadline := now + int64(h.cfg.RequestTimeoutSeconds)
	ctx, cancel := context.WithDeadline(request.Context(), time.Unix(deadline, 0))
	defer cancel()
	// 不提供 GetBody，也不转发幂等头；复用连接时 Transport 不能重放这份 POST 正文。
	outgoing, err := http.NewRequestWithContext(ctx, http.MethodPost, h.core.String(), io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "edge_request_failed")
		return
	}
	outgoing.ContentLength = int64(len(body))
	outgoing.Header.Set("Content-Type", "application/json")
	outgoing.Header.Set("Accept", "application/json, text/event-stream")
	outgoing.Header.Set("Accept-Encoding", "identity")
	outgoing.Header.Set("User-Agent", "sub2api-bridge-edge/1")
	outgoing.Header.Set("Authorization", "Bearer "+grant.CoreAPIKey)
	payloadHash := sha256.Sum256(body)
	key := h.keys[grant.SigningKeyID]
	envelope := custombridge.ForwardEnvelope{
		Version: custombridge.ForwardVersion, KID: key.ID, Audience: h.cfg.Audience,
		RequestID: requestID, ReservationID: reservationID, AttemptID: attemptID,
		InternalKeyID: grant.CoreKeyID, GroupID: grant.GroupID, AccountID: accountID,
		Endpoint: "/v1/responses", CanonicalModel: model, PayloadSHA256: hex.EncodeToString(payloadHash[:]),
		IssuedAt: now, ExpiresAt: now + int64(h.cfg.SignatureTTLSeconds), Deadline: deadline,
		PolicyRevision: grant.PolicyRevision, PoolRef: grant.PoolRef, PayerRef: grant.PayerRef, ActorRef: grant.ActorRef,
	}
	if err := custombridge.SignForwardRequest(outgoing, body, envelope, key); err != nil {
		writeError(writer, http.StatusInternalServerError, "edge_signing_failed")
		return
	}
	response, err := h.client.Do(outgoing)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(writer, http.StatusGatewayTimeout, "request_timeout")
		} else if ctx.Err() == nil {
			writeError(writer, http.StatusBadGateway, "core_unavailable")
		}
		return
	}
	defer func() { _ = response.Body.Close() }()
	if err := copyResponse(ctx, writer, response); err != nil {
		// 已提交的响应不能追加成功终止或另一份 JSON；中止连接保留断流事实。
		panic(http.ErrAbortHandler)
	}
}

func (h *Handler) authorize(header http.Header) (Grant, bool) {
	values := header.Values("Authorization")
	if len(values) != 1 {
		return Grant{}, false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !validToken(token) {
		return Grant{}, false
	}
	digest := sha256.Sum256([]byte(token))
	grant, ok := h.grants[hex.EncodeToString(digest[:])]
	return grant, ok
}

func requestIdentity(grantID string, idempotencyKeys []string) (string, string, string, error) {
	if len(idempotencyKeys) > 1 || len(idempotencyKeys) == 1 && (!validToken(idempotencyKeys[0]) || len(idempotencyKeys[0]) > 256) {
		return "", "", "", errors.New("幂等键无效")
	}
	requestID, err := uuid.NewRandom()
	if err != nil {
		return "", "", "", errors.New("请求身份生成失败")
	}
	if len(idempotencyKeys) == 1 {
		return requestID.String(), identityHash("reservation", grantID, idempotencyKeys[0]), identityHash("attempt", grantID, idempotencyKeys[0]), nil
	}
	reservationID, err := uuid.NewRandom()
	if err != nil {
		return "", "", "", errors.New("请求身份生成失败")
	}
	attemptID, err := uuid.NewRandom()
	if err != nil {
		return "", "", "", errors.New("请求身份生成失败")
	}
	return requestID.String(), reservationID.String(), attemptID.String(), nil
}

func identityHash(kind, grantID, key string) string {
	hash := sha256.New()
	for _, part := range []string{"sub2api.bridge-edge.v1", kind, grantID, key} {
		_ = binary.Write(hash, binary.BigEndian, uint64(len(part)))
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"type": "edge_error", "code": code, "message": "Edge request failed"}})
}
