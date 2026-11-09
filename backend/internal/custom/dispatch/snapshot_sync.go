package dispatch

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/netutil"
)

// 快照同步只走同机 UDS：不开放 TCP 端口、不需要 TLS 材料，访问先受套接字文件权限约束，
// 请求与响应再以共享密钥 HMAC 认证。只传输与文件模式相同的快照内容，不含 Provider 凭据。
const (
	SnapshotSyncPath        = "/v1/dispatch/snapshot"
	SnapshotEnvelopeVersion = 1
	MaxSnapshotSyncWait     = 25 * time.Second
	MaxSnapshotSocketPath   = 100
	maxSnapshotSyncKeys     = 8
	maxSnapshotEnvelope     = MaxSnapshotBytes + 4096
	snapshotSyncClockSkew   = 30 * time.Second
	snapshotSyncHeaderLimit = 8 << 10
	headerSnapshotKey       = "X-Dispatch-Key-Id"
	headerSnapshotTime      = "X-Dispatch-Timestamp"
	headerSnapshotNonce     = "X-Dispatch-Nonce"
	headerSnapshotSignature = "X-Dispatch-Signature"
)

type SnapshotServerConfig struct {
	SocketPath           string `json:"socket_path"`
	KeysFile             string `json:"keys_file"`
	MaxConcurrent        int    `json:"max_concurrent,omitempty"`
	MaxRequestsPerSecond int    `json:"max_requests_per_second,omitempty"`
	// HardInvalidation 未配置时不启用独立失效通道，配置摘要与快照服务行为不变。
	HardInvalidation *HardInvalidationConfig `json:"hard_invalidation,omitempty"`
}

type SnapshotSyncKey struct {
	ID           string `json:"id"`
	SecretBase64 string `json:"secret_base64"`
}

// SnapshotEnvelope 的 Snapshot 与文件模式同一格式；发布 epoch 为 (PublishLineage, Snapshot.Revision)。
type SnapshotEnvelope struct {
	Version        int      `json:"version"`
	PublishLineage string   `json:"publish_lineage"`
	Snapshot       Snapshot `json:"snapshot"`
}

func ValidSnapshotSocketPath(path string) bool {
	return path != "" && len(path) <= MaxSnapshotSocketPath && !strings.ContainsRune(path, '\x00') &&
		strings.HasPrefix(path, "/") && filepath.Clean(path) == path
}

func normalizeSnapshotServerConfig(cfg SnapshotServerConfig) (SnapshotServerConfig, error) {
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 8
	}
	if cfg.MaxRequestsPerSecond == 0 {
		cfg.MaxRequestsPerSecond = 20
	}
	if !ValidSnapshotSocketPath(cfg.SocketPath) || strings.TrimSpace(cfg.KeysFile) == "" ||
		cfg.MaxConcurrent < 1 || cfg.MaxConcurrent > 64 || cfg.MaxRequestsPerSecond < 1 || cfg.MaxRequestsPerSecond > 1000 {
		return SnapshotServerConfig{}, errors.New("invalid dispatch snapshot server configuration")
	}
	if cfg.HardInvalidation != nil {
		hard, err := normalizeHardInvalidationConfig(*cfg.HardInvalidation)
		if err != nil {
			return SnapshotServerConfig{}, err
		}
		cfg.HardInvalidation = &hard
	}
	return cfg, nil
}

// DecodeSnapshotSyncSecret 要求标准 base64 且至少 32 字节，与桥签名密钥下限一致。
func DecodeSnapshotSyncSecret(id, value string) ([]byte, error) {
	secret, err := base64.StdEncoding.Strict().DecodeString(value)
	if !validRef(id) || err != nil || len(secret) < 32 {
		return nil, errors.New("invalid dispatch snapshot sync key")
	}
	return secret, nil
}

func LoadSnapshotSyncKeys(path string) (map[string][]byte, error) {
	var file struct {
		Version int               `json:"version"`
		Keys    []SnapshotSyncKey `json:"keys"`
	}
	if err := readJSONFile(path, 64<<10, &file); err != nil {
		return nil, err
	}
	if file.Version != 1 || len(file.Keys) < 1 || len(file.Keys) > maxSnapshotSyncKeys {
		return nil, errors.New("invalid dispatch snapshot sync key file")
	}
	keys := make(map[string][]byte, len(file.Keys))
	for _, key := range file.Keys {
		secret, err := DecodeSnapshotSyncSecret(key.ID, key.SecretBase64)
		if err != nil {
			return nil, err
		}
		if keys[key.ID] != nil {
			return nil, errors.New("duplicate dispatch snapshot sync key")
		}
		keys[key.ID] = secret
	}
	return keys, nil
}

type snapshotQuery struct {
	core     string
	lineage  string
	revision int64
	wait     time.Duration
}

func (q snapshotQuery) values() url.Values {
	return url.Values{
		"core_instance":  {q.core},
		"after_lineage":  {q.lineage},
		"after_revision": {strconv.FormatInt(q.revision, 10)},
		"wait_ms":        {strconv.FormatInt(q.wait.Milliseconds(), 10)},
	}
}

func parseSnapshotQuery(values url.Values) (snapshotQuery, error) {
	invalid := errors.New("invalid dispatch snapshot query")
	if len(values) != 4 {
		return snapshotQuery{}, invalid
	}
	single := func(name string) (string, bool) {
		value, ok := values[name]
		return strings.Join(value, ""), ok && len(value) == 1
	}
	core, okCore := single("core_instance")
	lineage, okLineage := single("after_lineage")
	revisionText, okRevision := single("after_revision")
	waitText, okWait := single("wait_ms")
	revision, revisionErr := strconv.ParseInt(revisionText, 10, 64)
	wait, waitErr := strconv.ParseInt(waitText, 10, 64)
	if !okCore || !okLineage || !okRevision || !okWait || revisionErr != nil || waitErr != nil || !validRef(core) ||
		(lineage != "" && !validRef(lineage)) || (lineage == "") != (revision == 0) || revision < 0 ||
		revisionText != strconv.FormatInt(revision, 10) || waitText != strconv.FormatInt(wait, 10) ||
		wait < 0 || wait > MaxSnapshotSyncWait.Milliseconds() {
		return snapshotQuery{}, invalid
	}
	return snapshotQuery{core, lineage, revision, time.Duration(wait) * time.Millisecond}, nil
}

func snapshotRequestMAC(secret []byte, keyID, timestamp, nonce string, query snapshotQuery) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "sub2api-dispatch-snapshot-request-v1\n%s\n%s\n%s\n%s\n%s\n%d\n%d",
		keyID, timestamp, nonce, query.core, query.lineage, query.revision, query.wait.Milliseconds())
	return mac.Sum(nil)
}

func snapshotResponseMAC(secret []byte, nonce string, status int, body []byte) []byte {
	digest := sha256.Sum256(body)
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "sub2api-dispatch-snapshot-response-v1\n%s\n%d\n%x", nonce, status, digest)
	return mac.Sum(nil)
}

func validNonce(value string) bool {
	if len(value) != 32 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type servedSnapshot struct {
	lineage   string
	revision  int64
	expiresAt time.Time
	body      []byte
}

// SnapshotServer 只提供当前进程最近一次提交的全量快照；不续期、不缓存历史版本。
type SnapshotServer struct {
	core    string
	keys    map[string][]byte
	slots   chan struct{}
	rate    int
	now     func() time.Time
	mu      sync.Mutex
	current *servedSnapshot
	changed chan struct{}
	window  time.Time
	count   int
}

func NewSnapshotServer(core string, cfg SnapshotServerConfig, keys map[string][]byte) (*SnapshotServer, error) {
	if !validRef(core) || len(keys) < 1 || len(keys) > maxSnapshotSyncKeys {
		return nil, errors.New("invalid dispatch snapshot server identity")
	}
	return &SnapshotServer{core: core, keys: keys, slots: make(chan struct{}, cfg.MaxConcurrent), rate: cfg.MaxRequestsPerSecond,
		now: time.Now, changed: make(chan struct{})}, nil
}

func (s *SnapshotServer) Publish(published PublishedSnapshot) error {
	if published.Snapshot.CoreInstance != s.core || !validRef(published.Lineage) {
		return errors.New("dispatch snapshot server publish identity mismatch")
	}
	body, err := json.Marshal(SnapshotEnvelope{Version: SnapshotEnvelopeVersion, PublishLineage: published.Lineage, Snapshot: published.Snapshot})
	if err != nil || len(body) > maxSnapshotEnvelope {
		return errors.New("dispatch snapshot envelope encode limit exceeded")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = &servedSnapshot{published.Lineage, published.Snapshot.Revision, published.Snapshot.ExpiresAt, body}
	close(s.changed)
	s.changed = make(chan struct{})
	return nil
}

func (s *SnapshotServer) allow(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if window := now.Truncate(time.Second); !window.Equal(s.window) {
		s.window, s.count = window, 0
	}
	s.count++
	return s.count <= s.rate
}

func (s *SnapshotServer) authenticate(r *http.Request, query snapshotQuery, now time.Time) ([]byte, string, bool) {
	keyID, timestamp, nonce := r.Header.Get(headerSnapshotKey), r.Header.Get(headerSnapshotTime), r.Header.Get(headerSnapshotNonce)
	secret := s.keys[keyID]
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	signature, signatureErr := base64.StdEncoding.Strict().DecodeString(r.Header.Get(headerSnapshotSignature))
	if secret == nil || err != nil || timestamp != strconv.FormatInt(seconds, 10) || !validNonce(nonce) || signatureErr != nil {
		return nil, "", false
	}
	if skew := now.Sub(time.Unix(seconds, 0)); skew > snapshotSyncClockSkew || skew < -snapshotSyncClockSkew {
		return nil, "", false
	}
	if !hmac.Equal(signature, snapshotRequestMAC(secret, keyID, timestamp, nonce, query)) {
		return nil, "", false
	}
	return secret, nonce, true
}

// rejectSnapshot 关闭连接，被拒请求不能以 keep-alive 连接长期占用监听器配额。
func rejectSnapshot(w http.ResponseWriter, status int) {
	w.Header().Set("Connection", "close")
	w.WriteHeader(status)
}

// ServeHTTP 先验签再计入速率与并发槽，未签名请求不能耗尽已认证订阅者的配额。
func (s *SnapshotServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != SnapshotSyncPath || r.URL.RawPath != "" {
		rejectSnapshot(w, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		rejectSnapshot(w, http.StatusMethodNotAllowed)
		return
	}
	query, err := parseSnapshotQuery(r.URL.Query())
	if err != nil || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		rejectSnapshot(w, http.StatusBadRequest)
		return
	}
	secret, nonce, ok := s.authenticate(r, query, s.now())
	if !ok {
		rejectSnapshot(w, http.StatusUnauthorized)
		return
	}
	if query.core != s.core {
		rejectSnapshot(w, http.StatusNotFound)
		return
	}
	if !s.allow(s.now()) {
		rejectSnapshot(w, http.StatusTooManyRequests)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		rejectSnapshot(w, http.StatusServiceUnavailable)
		return
	}
	timer := time.NewTimer(query.wait)
	defer timer.Stop()
	for {
		s.mu.Lock()
		current, changed := s.current, s.changed
		s.mu.Unlock()
		live := current != nil && s.now().Before(current.expiresAt)
		if live && (current.lineage != query.lineage || current.revision != query.revision) {
			writeSignedSnapshot(w, secret, nonce, http.StatusOK, current.body)
			return
		}
		select {
		case <-changed:
			continue
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		s.mu.Lock()
		current = s.current
		s.mu.Unlock()
		switch {
		case current == nil || !s.now().Before(current.expiresAt):
			rejectSnapshot(w, http.StatusServiceUnavailable)
		case current.lineage != query.lineage || current.revision != query.revision:
			writeSignedSnapshot(w, secret, nonce, http.StatusOK, current.body)
		default:
			writeSignedSnapshot(w, secret, nonce, http.StatusNoContent, nil)
		}
		return
	}
}

func writeSignedSnapshot(w http.ResponseWriter, secret []byte, nonce string, status int, body []byte) {
	w.Header().Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(snapshotResponseMAC(secret, nonce, status, body)))
	if body != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// NewSnapshotHTTPServer 限制头部、读写与空闲时长；写期限覆盖最长等待。
func NewSnapshotHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: MaxSnapshotSyncWait + 10*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: snapshotSyncHeaderLimit,
	}
}

var dialSnapshotSocket = func(path string) (net.Conn, error) {
	return net.DialTimeout("unix", path, 200*time.Millisecond)
}

// ListenSnapshotSocket 只替换确认无人监听（ECONNREFUSED）的陈旧套接字；普通文件、链接、
// 在用或无法判定（超时、EAGAIN 等）的套接字一律拒绝。同时接入的连接数不超过 maxConnections。
func ListenSnapshotSocket(path string, maxConnections int) (net.Listener, error) {
	if !ValidSnapshotSocketPath(path) || maxConnections < 1 {
		return nil, errors.New("invalid dispatch snapshot socket path")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode().Type() != os.ModeSocket {
			return nil, errors.New("dispatch snapshot socket path is not a socket")
		}
		conn, err := dialSnapshotSocket(path)
		if err == nil {
			_ = conn.Close()
			return nil, errors.New("dispatch snapshot socket already in use")
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, errors.New("dispatch snapshot socket may still be in use")
		}
		if err := os.Remove(path); err != nil {
			return nil, errors.New("dispatch snapshot stale socket removal failed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("dispatch snapshot socket path unavailable")
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, errors.New("dispatch snapshot socket listen failed")
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = listener.Close()
		return nil, errors.New("dispatch snapshot socket permission failed")
	}
	return netutil.LimitListener(listener, maxConnections), nil
}

// SnapshotClient 是 Edge 订阅端；不使用环境代理、不跟随重定向，每次请求独立签名。
type SnapshotClient struct {
	http   *http.Client
	keyID  string
	secret []byte
	core   string
}

type SnapshotFetch struct {
	NotModified bool
	Envelope    SnapshotEnvelope
}

func NewSnapshotClient(socketPath, keyID string, secret []byte, core string) *SnapshotClient {
	dialer := &net.Dialer{Timeout: time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns: 1, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: MaxSnapshotSyncWait + 5*time.Second,
		MaxResponseHeaderBytes: snapshotSyncHeaderLimit, DisableCompression: true,
	}
	return &SnapshotClient{
		http:  &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		keyID: keyID, secret: secret, core: core,
	}
}

func (c *SnapshotClient) Close() { c.http.CloseIdleConnections() }

// Fetch 在 (afterLineage, afterRevision) 与服务端当前 epoch 相同时最多等待 wait；
// 返回的快照已按接收时刻校验格式、到期及 Core 身份，单调性由 Selector 判定。
func (c *SnapshotClient) Fetch(ctx context.Context, afterLineage string, afterRevision int64, wait time.Duration) (SnapshotFetch, error) {
	query := snapshotQuery{c.core, afterLineage, afterRevision, wait.Truncate(time.Millisecond)}
	if wait < 0 || wait > MaxSnapshotSyncWait || (afterLineage == "") != (afterRevision == 0) {
		return SnapshotFetch{}, errors.New("invalid dispatch snapshot subscription position")
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return SnapshotFetch{}, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	requestCtx, cancel := context.WithTimeout(ctx, wait+5*time.Second)
	defer cancel()
	target := url.URL{Scheme: "http", Host: "dispatchd", Path: SnapshotSyncPath, RawQuery: query.values().Encode()}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return SnapshotFetch{}, err
	}
	request.Header.Set(headerSnapshotKey, c.keyID)
	request.Header.Set(headerSnapshotTime, timestamp)
	request.Header.Set(headerSnapshotNonce, nonce)
	request.Header.Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(snapshotRequestMAC(c.secret, c.keyID, timestamp, nonce, query)))
	response, err := c.http.Do(request)
	if err != nil {
		return SnapshotFetch{}, errors.New("dispatch snapshot subscription unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return SnapshotFetch{}, fmt.Errorf("dispatch snapshot subscription status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSnapshotEnvelope+1))
	if err != nil || len(body) > maxSnapshotEnvelope {
		return SnapshotFetch{}, errors.New("dispatch snapshot subscription read limit exceeded")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(response.Header.Get(headerSnapshotSignature))
	if err != nil || !hmac.Equal(signature, snapshotResponseMAC(c.secret, nonce, response.StatusCode, body)) {
		return SnapshotFetch{}, errors.New("dispatch snapshot subscription signature invalid")
	}
	if response.StatusCode == http.StatusNoContent {
		if len(body) != 0 {
			return SnapshotFetch{}, errors.New("dispatch snapshot subscription unexpected body")
		}
		return SnapshotFetch{NotModified: true}, nil
	}
	envelope, err := ParseSnapshotEnvelope(body, time.Now())
	if err != nil {
		return SnapshotFetch{}, err
	}
	if envelope.Snapshot.CoreInstance != c.core {
		return SnapshotFetch{}, errors.New("dispatch snapshot core identity mismatch")
	}
	return SnapshotFetch{Envelope: envelope}, nil
}

func ParseSnapshotEnvelope(body []byte, now time.Time) (SnapshotEnvelope, error) {
	var envelope SnapshotEnvelope
	if len(body) > maxSnapshotEnvelope || decodeJSON(body, &envelope) != nil {
		return SnapshotEnvelope{}, errors.New("invalid dispatch snapshot envelope JSON")
	}
	if envelope.Version != SnapshotEnvelopeVersion || !validRef(envelope.PublishLineage) {
		return SnapshotEnvelope{}, errors.New("invalid dispatch snapshot envelope identity")
	}
	return envelope, envelope.Snapshot.Validate(now)
}
