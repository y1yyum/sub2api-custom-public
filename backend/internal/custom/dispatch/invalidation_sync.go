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
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// 硬失效通道与快照长轮询共用同机 UDS、密钥和 HMAC 信任模型，但使用独立路径、签名域、速率窗口
// 与并发槽；两条路径仍共享监听器连接上限。
const (
	InvalidationSyncPath        = "/v1/dispatch/invalidations"
	InvalidationEnvelopeVersion = 1
	// InvalidationRetention 覆盖快照最长 TTL（30s）及 10s 时钟与传递余量：检测时已存在的快照
	// 在此之后必然过期，过期快照不能再被选号，因此更早的失效项无需再送达。
	InvalidationRetention   = 40 * time.Second
	maxInvalidationEnvelope = 4 << 20
)

// HardInvalidationConfig 嵌在 snapshot_server 中，未配置时不监听失效路径、不读 hold 表。
type HardInvalidationConfig struct {
	PollIntervalMilliseconds int `json:"poll_interval_milliseconds,omitempty"`
}

func normalizeHardInvalidationConfig(cfg HardInvalidationConfig) (HardInvalidationConfig, error) {
	if cfg.PollIntervalMilliseconds == 0 {
		cfg.PollIntervalMilliseconds = 500
	}
	if cfg.PollIntervalMilliseconds < 100 || cfg.PollIntervalMilliseconds > 5000 {
		return HardInvalidationConfig{}, errors.New("invalid dispatch hard invalidation configuration")
	}
	return cfg, nil
}

// InvalidationEnvelope 的 Sequence 是 feed 内单调序号；Feed 随 dispatchd 进程变化，变化时整体重发。
type InvalidationEnvelope struct {
	Version        int            `json:"version"`
	CoreInstance   string         `json:"core_instance"`
	Feed           string         `json:"feed"`
	Sequence       int64          `json:"sequence"`
	PublishLineage string         `json:"publish_lineage"`
	Reset          bool           `json:"reset"`
	Entries        []Invalidation `json:"entries"`
}

func ParseInvalidationEnvelope(body []byte) (InvalidationEnvelope, error) {
	var envelope InvalidationEnvelope
	if len(body) > maxInvalidationEnvelope || decodeJSON(body, &envelope) != nil {
		return InvalidationEnvelope{}, errors.New("invalid dispatch invalidation envelope JSON")
	}
	if envelope.Version != InvalidationEnvelopeVersion || !validRef(envelope.CoreInstance) || !validRef(envelope.Feed) ||
		envelope.Sequence < 1 || !validRef(envelope.PublishLineage) || envelope.Entries == nil || len(envelope.Entries) > MaxInvalidations {
		return InvalidationEnvelope{}, errors.New("invalid dispatch invalidation envelope identity")
	}
	seen := make(map[string]bool, len(envelope.Entries))
	for _, entry := range envelope.Entries {
		if seen[entry.HoldID] || !entry.valid(envelope.CoreInstance) || entry.Sequence > envelope.Sequence {
			return InvalidationEnvelope{}, errors.New("invalid or duplicate dispatch invalidation entry")
		}
		seen[entry.HoldID] = true
	}
	return envelope, nil
}

// HoldInvalidationState 是一次一致读取：当前 lineage、该 Core 已提交的最新快照 revision 与活动 hold。
type HoldInvalidationState struct {
	PublishLineage   string
	SnapshotRevision int64
	Holds            []HealthHoldReport
}

type invalidationRecord struct {
	entry    Invalidation
	detected time.Time
}

// InvalidationFeed 维护检测到的 hold 激活；只保留 InvalidationRetention 内的项，不持久化。
type InvalidationFeed struct {
	core    string
	keys    map[string][]byte
	slots   chan struct{}
	rate    int
	now     func() time.Time
	mu      sync.Mutex
	feed    string
	seq     int64
	lineage string
	known   map[string]int64
	records map[string]invalidationRecord
	changed chan struct{}
	window  time.Time
	count   int
}

func NewInvalidationFeed(core string, cfg SnapshotServerConfig, keys map[string][]byte) (*InvalidationFeed, error) {
	if !validRef(core) || len(keys) < 1 || len(keys) > maxSnapshotSyncKeys || cfg.HardInvalidation == nil {
		return nil, errors.New("invalid dispatch invalidation feed identity")
	}
	return &InvalidationFeed{core: core, keys: keys, slots: make(chan struct{}, cfg.MaxConcurrent), rate: cfg.MaxRequestsPerSecond,
		now: time.Now, feed: uuid.NewString(), known: map[string]int64{}, records: map[string]invalidationRecord{},
		changed: make(chan struct{})}, nil
}

// Observe 把一次读取到的活动 hold 与已知版本比较：新出现或版本变化的 hold 记为新失效项。
// 尚无发布 lineage 时不就绪（此前没有订阅快照可供失效）。
func (f *InvalidationFeed) Observe(state HoldInvalidationState) error {
	if state.PublishLineage == "" {
		return nil
	}
	if !validRef(state.PublishLineage) || state.SnapshotRevision < 0 || len(state.Holds) > MaxInvalidations {
		return errors.New("invalid dispatch hold invalidation state")
	}
	now := f.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	changed := f.seq == 0 || state.PublishLineage != f.lineage
	if changed {
		clear(f.known)
		f.seq++
	}
	f.lineage = state.PublishLineage
	active := make(map[string]int64, len(state.Holds))
	for _, hold := range state.Holds {
		active[hold.ID] = hold.Revision
		if revision, known := f.known[hold.ID]; known && revision == hold.Revision {
			continue
		}
		f.seq++
		f.records[hold.ID] = invalidationRecord{Invalidation{HoldID: hold.ID, HoldRevision: hold.Revision, Class: hold.Class,
			Scope: hold.Scope, PublishLineage: state.PublishLineage, FloorRevision: state.SnapshotRevision, Sequence: f.seq}, now}
		changed = true
	}
	f.known = active
	for id, record := range f.records {
		if now.Sub(record.detected) >= InvalidationRetention {
			delete(f.records, id)
		}
	}
	if changed {
		close(f.changed)
		f.changed = make(chan struct{})
	}
	return nil
}

type invalidationQuery struct {
	core     string
	feed     string
	sequence int64
	wait     time.Duration
}

func (q invalidationQuery) values() url.Values {
	return url.Values{
		"core_instance":  {q.core},
		"after_feed":     {q.feed},
		"after_sequence": {strconv.FormatInt(q.sequence, 10)},
		"wait_ms":        {strconv.FormatInt(q.wait.Milliseconds(), 10)},
	}
}

func parseInvalidationQuery(values url.Values) (invalidationQuery, error) {
	invalid := errors.New("invalid dispatch invalidation query")
	if len(values) != 4 {
		return invalidationQuery{}, invalid
	}
	single := func(name string) (string, bool) {
		value, ok := values[name]
		return strings.Join(value, ""), ok && len(value) == 1
	}
	core, okCore := single("core_instance")
	feed, okFeed := single("after_feed")
	sequenceText, okSequence := single("after_sequence")
	waitText, okWait := single("wait_ms")
	sequence, sequenceErr := strconv.ParseInt(sequenceText, 10, 64)
	wait, waitErr := strconv.ParseInt(waitText, 10, 64)
	if !okCore || !okFeed || !okSequence || !okWait || sequenceErr != nil || waitErr != nil || !validRef(core) ||
		(feed != "" && !validRef(feed)) || (feed == "") != (sequence == 0) || sequence < 0 ||
		sequenceText != strconv.FormatInt(sequence, 10) || waitText != strconv.FormatInt(wait, 10) ||
		wait < 0 || wait > MaxSnapshotSyncWait.Milliseconds() {
		return invalidationQuery{}, invalid
	}
	return invalidationQuery{core, feed, sequence, time.Duration(wait) * time.Millisecond}, nil
}

func invalidationRequestMAC(secret []byte, keyID, timestamp, nonce string, query invalidationQuery) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "sub2api-dispatch-invalidation-request-v1\n%s\n%s\n%s\n%s\n%s\n%d\n%d",
		keyID, timestamp, nonce, query.core, query.feed, query.sequence, query.wait.Milliseconds())
	return mac.Sum(nil)
}

func invalidationResponseMAC(secret []byte, nonce string, status int, body []byte) []byte {
	digest := sha256.Sum256(body)
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "sub2api-dispatch-invalidation-response-v1\n%s\n%d\n%x", nonce, status, digest)
	return mac.Sum(nil)
}

func (f *InvalidationFeed) allow(now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if window := now.Truncate(time.Second); !window.Equal(f.window) {
		f.window, f.count = window, 0
	}
	f.count++
	return f.count <= f.rate
}

func (f *InvalidationFeed) authenticate(r *http.Request, query invalidationQuery, now time.Time) ([]byte, string, bool) {
	keyID, timestamp, nonce := r.Header.Get(headerSnapshotKey), r.Header.Get(headerSnapshotTime), r.Header.Get(headerSnapshotNonce)
	secret := f.keys[keyID]
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	signature, signatureErr := base64.StdEncoding.Strict().DecodeString(r.Header.Get(headerSnapshotSignature))
	if secret == nil || err != nil || timestamp != strconv.FormatInt(seconds, 10) || !validNonce(nonce) || signatureErr != nil {
		return nil, "", false
	}
	if skew := now.Sub(time.Unix(seconds, 0)); skew > snapshotSyncClockSkew || skew < -snapshotSyncClockSkew {
		return nil, "", false
	}
	if !hmac.Equal(signature, invalidationRequestMAC(secret, keyID, timestamp, nonce, query)) {
		return nil, "", false
	}
	return secret, nonce, true
}

// envelopeLocked 在 feed 不同或位置超前时整体重发保留项，否则只发序号更大的项。
func (f *InvalidationFeed) envelopeLocked(query invalidationQuery) ([]byte, error) {
	reset := query.feed != f.feed || query.sequence > f.seq
	envelope := InvalidationEnvelope{Version: InvalidationEnvelopeVersion, CoreInstance: f.core, Feed: f.feed, Sequence: f.seq,
		PublishLineage: f.lineage, Reset: reset, Entries: []Invalidation{}}
	for _, record := range f.records {
		if reset || record.entry.Sequence > query.sequence {
			envelope.Entries = append(envelope.Entries, record.entry)
		}
	}
	sort.Slice(envelope.Entries, func(i, j int) bool { return envelope.Entries[i].Sequence < envelope.Entries[j].Sequence })
	body, err := json.Marshal(envelope)
	if err != nil || len(body) > maxInvalidationEnvelope {
		return nil, errors.New("dispatch invalidation envelope encode limit exceeded")
	}
	return body, nil
}

// ServeHTTP 的处理顺序与快照订阅一致：形状 → 验签 → core → 速率 → 并发槽。
func (f *InvalidationFeed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != InvalidationSyncPath || r.URL.RawPath != "" {
		rejectSnapshot(w, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		rejectSnapshot(w, http.StatusMethodNotAllowed)
		return
	}
	query, err := parseInvalidationQuery(r.URL.Query())
	if err != nil || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		rejectSnapshot(w, http.StatusBadRequest)
		return
	}
	secret, nonce, ok := f.authenticate(r, query, f.now())
	if !ok {
		rejectSnapshot(w, http.StatusUnauthorized)
		return
	}
	if query.core != f.core {
		rejectSnapshot(w, http.StatusNotFound)
		return
	}
	if !f.allow(f.now()) {
		rejectSnapshot(w, http.StatusTooManyRequests)
		return
	}
	select {
	case f.slots <- struct{}{}:
		defer func() { <-f.slots }()
	default:
		rejectSnapshot(w, http.StatusServiceUnavailable)
		return
	}
	timer := time.NewTimer(query.wait)
	defer timer.Stop()
	for {
		f.mu.Lock()
		ready, current, changed := f.seq > 0, query.feed == f.feed && query.sequence == f.seq, f.changed
		var body []byte
		if ready && !current {
			body, err = f.envelopeLocked(query)
		}
		f.mu.Unlock()
		switch {
		case err != nil:
			rejectSnapshot(w, http.StatusServiceUnavailable)
			return
		case body != nil:
			writeSignedInvalidation(w, secret, nonce, http.StatusOK, body)
			return
		}
		select {
		case <-changed:
			continue
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		if !ready {
			rejectSnapshot(w, http.StatusServiceUnavailable)
			return
		}
		f.mu.Lock()
		current = query.feed == f.feed && query.sequence == f.seq
		if !current {
			body, err = f.envelopeLocked(query)
		}
		f.mu.Unlock()
		switch {
		case err != nil:
			rejectSnapshot(w, http.StatusServiceUnavailable)
		case body != nil:
			writeSignedInvalidation(w, secret, nonce, http.StatusOK, body)
		default:
			writeSignedInvalidation(w, secret, nonce, http.StatusNoContent, nil)
		}
		return
	}
}

func writeSignedInvalidation(w http.ResponseWriter, secret []byte, nonce string, status int, body []byte) {
	w.Header().Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(invalidationResponseMAC(secret, nonce, status, body)))
	if body != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// NewSyncHandler 只在启用硬失效时把失效路径挂到同一监听器；其余路径仍由快照服务原样处理。
func NewSyncHandler(snapshots *SnapshotServer, invalidations *InvalidationFeed) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == InvalidationSyncPath {
			invalidations.ServeHTTP(w, r)
			return
		}
		snapshots.ServeHTTP(w, r)
	})
}

// WatchHoldInvalidations 按间隔一致读取活动 hold 并送入 feed；只读事务、不取调度全局锁，
// 每次读取以 1 秒为限。读取失败交给 report，失效通道退化为普通快照传播。
func WatchHoldInvalidations(ctx context.Context, store *Store, core string, interval time.Duration, feed *InvalidationFeed, report func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		state, err := store.HoldInvalidationState(readCtx, core)
		cancel()
		if err == nil {
			err = feed.Observe(state)
		}
		if ctx.Err() != nil {
			return
		}
		report(err)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// InvalidationClient 是 Edge 的失效订阅端；连接与快照订阅分开，不使用代理、不跟随重定向。
type InvalidationClient struct {
	http   *http.Client
	keyID  string
	secret []byte
	core   string
}

type InvalidationFetch struct {
	NotModified bool
	Envelope    InvalidationEnvelope
}

func NewInvalidationClient(socketPath, keyID string, secret []byte, core string) *InvalidationClient {
	snapshot := NewSnapshotClient(socketPath, keyID, secret, core)
	return &InvalidationClient{http: snapshot.http, keyID: keyID, secret: secret, core: core}
}

func (c *InvalidationClient) Close() { c.http.CloseIdleConnections() }

// Fetch 在位置与服务端相同时最多等待 wait。非重发响应必须属于同一 feed 且序号不回退。
func (c *InvalidationClient) Fetch(ctx context.Context, afterFeed string, afterSequence int64, wait time.Duration) (InvalidationFetch, error) {
	query := invalidationQuery{c.core, afterFeed, afterSequence, wait.Truncate(time.Millisecond)}
	if wait < 0 || wait > MaxSnapshotSyncWait || (afterFeed == "") != (afterSequence == 0) {
		return InvalidationFetch{}, errors.New("invalid dispatch invalidation subscription position")
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return InvalidationFetch{}, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	requestCtx, cancel := context.WithTimeout(ctx, wait+5*time.Second)
	defer cancel()
	target := url.URL{Scheme: "http", Host: "dispatchd", Path: InvalidationSyncPath, RawQuery: query.values().Encode()}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return InvalidationFetch{}, err
	}
	request.Header.Set(headerSnapshotKey, c.keyID)
	request.Header.Set(headerSnapshotTime, timestamp)
	request.Header.Set(headerSnapshotNonce, nonce)
	request.Header.Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(invalidationRequestMAC(c.secret, c.keyID, timestamp, nonce, query)))
	response, err := c.http.Do(request)
	if err != nil {
		return InvalidationFetch{}, errors.New("dispatch invalidation subscription unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return InvalidationFetch{}, fmt.Errorf("dispatch invalidation subscription status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxInvalidationEnvelope+1))
	if err != nil || len(body) > maxInvalidationEnvelope {
		return InvalidationFetch{}, errors.New("dispatch invalidation subscription read limit exceeded")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(response.Header.Get(headerSnapshotSignature))
	if err != nil || !hmac.Equal(signature, invalidationResponseMAC(c.secret, nonce, response.StatusCode, body)) {
		return InvalidationFetch{}, errors.New("dispatch invalidation subscription signature invalid")
	}
	if response.StatusCode == http.StatusNoContent {
		if len(body) != 0 {
			return InvalidationFetch{}, errors.New("dispatch invalidation subscription unexpected body")
		}
		return InvalidationFetch{NotModified: true}, nil
	}
	envelope, err := ParseInvalidationEnvelope(body)
	if err != nil {
		return InvalidationFetch{}, err
	}
	if envelope.CoreInstance != c.core {
		return InvalidationFetch{}, errors.New("dispatch invalidation core identity mismatch")
	}
	if !envelope.Reset && (envelope.Feed != afterFeed || envelope.Sequence < afterSequence) {
		return InvalidationFetch{}, errors.New("dispatch invalidation sequence out of order")
	}
	for _, entry := range envelope.Entries {
		if !envelope.Reset && entry.Sequence <= afterSequence {
			return InvalidationFetch{}, errors.New("dispatch invalidation sequence out of order")
		}
	}
	return InvalidationFetch{Envelope: envelope}, nil
}
