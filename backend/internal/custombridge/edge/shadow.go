package edge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

// ShadowStats 只有固定聚合字段，不按请求、身份或路由创建标签。
type ShadowStats struct {
	Enabled             bool            `json:"enabled"`
	SnapshotRevision    int64           `json:"snapshot_revision"`
	SnapshotExpiresAt   time.Time       `json:"snapshot_expires_at"`
	LastRefreshOK       bool            `json:"last_refresh_ok"`
	RefreshSuccesses    uint64          `json:"refresh_successes"`
	RefreshFailures     uint64          `json:"refresh_failures"`
	Observations        uint64          `json:"observations"`
	Matches             uint64          `json:"matches"`
	Mismatches          uint64          `json:"mismatches"`
	MismatchReasons     MismatchReasons `json:"mismatch_reasons"`
	Unavailable         uint64          `json:"unavailable"`
	SnapshotUnavailable uint64          `json:"snapshot_unavailable"`
	SnapshotExpired     uint64          `json:"snapshot_expired"`
	NoEligibleRoute     uint64          `json:"no_eligible_route"`
	SnapshotLineage     string          `json:"snapshot_lineage,omitempty"`
	LineageChanges      uint64          `json:"lineage_changes,omitempty"`
	InvalidationStats
}

// InvalidationStats 只在启用硬失效订阅后出现非零值；全部 omitempty，未启用时输出字节不变。
type InvalidationStats struct {
	InvalidationSequence  int64  `json:"invalidation_sequence,omitempty"`
	InvalidationRefreshOK bool   `json:"invalidation_refresh_ok,omitempty"`
	InvalidationRefreshes uint64 `json:"invalidation_refreshes,omitempty"`
	InvalidationFailures  uint64 `json:"invalidation_failures,omitempty"`
	InvalidationsApplied  uint64 `json:"invalidations_applied,omitempty"`
	InvalidationEntries   int    `json:"invalidation_entries,omitempty"`
	InvalidatedCandidates int    `json:"invalidated_candidates,omitempty"`
}

type MismatchReasons struct {
	ExpectedAccountAbsent uint64 `json:"expected_account_absent"`
	RouteMismatch         uint64 `json:"route_mismatch"`
	Disabled              uint64 `json:"disabled"`
	Health                uint64 `json:"health"`
	ZeroWeight            uint64 `json:"zero_weight"`
	Capacity              uint64 `json:"capacity"`
	SWRRChoice            uint64 `json:"swrr_choice"`
}

type shadowStatsRecord struct {
	Type  string      `json:"type"`
	Phase string      `json:"phase"`
	Stats ShadowStats `json:"stats"`
}

const shadowStatsWriteTimeout = 250 * time.Millisecond

// StatsWriter 必须实际执行写期限，并允许 SetWriteDeadline 与 Write 并发。
type StatsWriter interface {
	io.Writer
	SetWriteDeadline(time.Time) error
}

// 生命周期循环串行写入；取消回调只设置期限，不创建写 goroutine。
type shadowStatsReporter struct {
	output StatsWriter
	failed bool
}

func (r *shadowStatsReporter) write(ctx context.Context, phase string, stats ShadowStats) {
	r.writeRecord(ctx, shadowStatsRecord{Type: "bridge_edge_shadow_stats", Phase: phase, Stats: stats})
}

func (r *shadowStatsReporter) writeRecord(ctx context.Context, record any) {
	if r.output == nil || r.failed || ctx.Err() != nil {
		return
	}
	deadline := time.Now().Add(shadowStatsWriteTimeout)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	if err := r.output.SetWriteDeadline(deadline); err != nil {
		r.failed = true
		return
	}
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(canceled)
		_ = r.output.SetWriteDeadline(time.Now())
	})
	defer func() {
		if !stop() {
			<-canceled
		}
		_ = r.output.SetWriteDeadline(time.Time{})
	}()
	if ctx.Err() != nil {
		return
	}
	body, err := json.Marshal(record)
	if err != nil {
		r.failed = true
		return
	}
	body = append(body, '\n')
	if n, err := r.output.Write(body); err != nil || n != len(body) {
		r.failed = true
	}
}

type shadowDispatch struct {
	cfg       ShadowDispatchConfig
	selector  *dispatch.Selector
	client    *dispatch.SnapshotClient
	hard      *dispatch.InvalidationClient
	feed      string
	mu        sync.Mutex
	stats     ShadowStats
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

func newShadowDispatch(cfg ShadowDispatchConfig) *shadowDispatch {
	ctx, cancel := context.WithCancel(context.Background())
	var selector *dispatch.Selector
	if cfg.SelectionSeed != nil {
		selector = dispatch.NewSelectorWithSeed(cfg.CoreInstance, *cfg.SelectionSeed)
	} else {
		selector = dispatch.NewSelector(cfg.CoreInstance)
	}
	shadow := &shadowDispatch{
		cfg: cfg, selector: selector,
		stats: ShadowStats{Enabled: true}, cancel: cancel, done: make(chan struct{}),
	}
	if cfg.SnapshotSubscription != nil {
		subscription := cfg.SnapshotSubscription
		// normalizeConfig 已验证密钥编码与长度。
		secret, _ := base64.StdEncoding.Strict().DecodeString(subscription.SecretBase64)
		shadow.client = dispatch.NewSnapshotClient(subscription.SocketPath, subscription.KeyID, secret, cfg.CoreInstance)
		shadow.subscribe(ctx, 0)
		retry := time.Duration(cfg.PollIntervalMilliseconds) * time.Millisecond
		var workers sync.WaitGroup
		workers.Go(func() {
			defer shadow.client.Close()
			wait := time.Duration(subscription.WaitMilliseconds) * time.Millisecond
			subscriptionLoop(ctx, retry, func() bool { return shadow.subscribe(ctx, wait) })
		})
		if hard := subscription.HardInvalidation; hard != nil {
			shadow.hard = dispatch.NewInvalidationClient(subscription.SocketPath, subscription.KeyID, secret, cfg.CoreInstance)
			shadow.pullInvalidations(ctx, 0)
			workers.Go(func() {
				defer shadow.hard.Close()
				wait := time.Duration(hard.WaitMilliseconds) * time.Millisecond
				subscriptionLoop(ctx, retry, func() bool { return shadow.pullInvalidations(ctx, wait) })
			})
		}
		go func() {
			workers.Wait()
			close(shadow.done)
		}()
		return shadow
	}
	shadow.refresh(time.Now())
	go func() {
		defer close(shadow.done)
		ticker := time.NewTicker(time.Duration(cfg.PollIntervalMilliseconds) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				shadow.refresh(time.Now())
			}
		}
	}()
	return shadow
}

const maxSubscriptionBackoff = 30 * time.Second

func subscriptionLoop(ctx context.Context, retry time.Duration, fetch func() bool) {
	failures := 0
	for ctx.Err() == nil {
		if fetch() {
			failures = 0
			continue
		}
		failures++
		timer := time.NewTimer(subscriptionBackoff(retry, failures))
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

// subscriptionBackoff 自刷新间隔起按连续失败次数翻倍、上限 30 秒，并在 [delay/2, delay] 内抖动，
// 服务端持续返回被拒快照或不可用时，多个 Edge 不会以固定间隔同步重试。
func subscriptionBackoff(base time.Duration, failures int) time.Duration {
	delay := maxSubscriptionBackoff
	if shift := failures - 1; shift < 16 && base<<shift < maxSubscriptionBackoff {
		delay = base << shift
	}
	return delay/2 + rand.N(delay/2+1)
}

func (s *shadowDispatch) refresh(now time.Time) {
	// 文件读取只在启动和后台刷新发生，不占用请求观察的锁。
	snapshot, err := dispatch.LoadSnapshot(s.cfg.SnapshotFile, now)
	s.apply("", snapshot, err, now)
}

// subscribe 发起一次订阅请求；失败或被拒时返回 false，由调用方按 subscriptionBackoff 退避。
// 断连不改变已接受快照的到期时间，过期后由 Selector 按原规则拒绝。
func (s *shadowDispatch) subscribe(ctx context.Context, wait time.Duration) bool {
	s.mu.Lock()
	lineage, revision := s.stats.SnapshotLineage, s.stats.SnapshotRevision
	s.mu.Unlock()
	fetch, err := s.client.Fetch(ctx, lineage, revision, wait)
	if ctx.Err() != nil {
		return false
	}
	now := time.Now()
	if err == nil && fetch.NotModified {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.stats.LastRefreshOK = true
		s.stats.RefreshSuccesses++
		return true
	}
	return s.apply(fetch.Envelope.PublishLineage, fetch.Envelope.Snapshot, err, now)
}

// pullInvalidations 拉取一次硬失效；只收紧候选。失败时保留已应用的覆盖层与位置，
// 失效改由快照传播，断连不放宽任何已剔除目标。
func (s *shadowDispatch) pullInvalidations(ctx context.Context, wait time.Duration) bool {
	s.mu.Lock()
	feed, sequence := s.feed, s.stats.InvalidationSequence
	s.mu.Unlock()
	fetch, err := s.hard.Fetch(ctx, feed, sequence, wait)
	if ctx.Err() != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	applied := 0
	if err == nil && !fetch.NotModified {
		envelope := fetch.Envelope
		applied, err = s.selector.ApplyInvalidations(envelope.PublishLineage, envelope.Entries)
		if err == nil {
			s.feed, s.stats.InvalidationSequence = envelope.Feed, envelope.Sequence
		}
	}
	s.stats.InvalidationRefreshOK = err == nil
	if err != nil {
		s.stats.InvalidationFailures++
		return false
	}
	s.stats.InvalidationRefreshes++
	s.stats.InvalidationsApplied += uint64(applied)
	return true
}

func (s *shadowDispatch) apply(lineage string, snapshot dispatch.Snapshot, err error, now time.Time) bool {
	if err == nil && s.cfg.RequireLiveLoad && snapshot.LoadObservedAt.IsZero() {
		err = errors.New("dispatch snapshot lacks live load evidence")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		if lineage == "" {
			err = s.selector.Update(snapshot, now)
		} else {
			err = s.selector.UpdateEpoch(lineage, snapshot, now)
		}
	}
	s.stats.LastRefreshOK = err == nil
	if err != nil {
		s.stats.RefreshFailures++
		return false
	}
	s.stats.RefreshSuccesses++
	if lineage != "" && s.stats.SnapshotLineage != "" && lineage != s.stats.SnapshotLineage {
		s.stats.LineageChanges++
	}
	s.stats.SnapshotRevision, s.stats.SnapshotExpiresAt, s.stats.SnapshotLineage = snapshot.Revision, snapshot.ExpiresAt, lineage
	return true
}

func (s *shadowDispatch) observe(grant Grant, model string, now time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	decision, mismatch := s.selector.SelectShadow(dispatch.Route{
		PoolRef: grant.PoolRef, PolicyRevision: grant.PolicyRevision, Model: model, Region: grant.Region,
	}, grant.AccountID, now)
	s.stats.Observations++
	if decision.AccountID <= 0 {
		s.stats.Unavailable++
		switch decision.Reason {
		case "snapshot_unavailable":
			s.stats.SnapshotUnavailable++
		case "snapshot_expired":
			s.stats.SnapshotExpired++
		case "no_eligible_route":
			s.stats.NoEligibleRoute++
		}
	} else if decision.AccountID == grant.AccountID {
		s.stats.Matches++
	} else {
		s.stats.Mismatches++
		switch mismatch {
		case dispatch.MismatchExpectedAccountAbsent:
			s.stats.MismatchReasons.ExpectedAccountAbsent++
		case dispatch.MismatchRoute:
			s.stats.MismatchReasons.RouteMismatch++
		case dispatch.MismatchDisabled:
			s.stats.MismatchReasons.Disabled++
		case dispatch.MismatchHealth:
			s.stats.MismatchReasons.Health++
		case dispatch.MismatchZeroWeight:
			s.stats.MismatchReasons.ZeroWeight++
		case dispatch.MismatchCapacity:
			s.stats.MismatchReasons.Capacity++
		case dispatch.MismatchSWRRChoice:
			s.stats.MismatchReasons.SWRRChoice++
		}
	}
}

func (s *shadowDispatch) close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(s.cancel)
	<-s.done
}

func (h *Handler) ShadowStats() ShadowStats {
	if h.shadow == nil {
		return ShadowStats{}
	}
	h.shadow.mu.Lock()
	defer h.shadow.mu.Unlock()
	return h.shadow.statsLocked()
}

func (s *shadowDispatch) statsLocked() ShadowStats {
	stats := s.stats
	if s.hard != nil {
		stats.InvalidationEntries, stats.InvalidatedCandidates = s.selector.InvalidationCounts()
	}
	return stats
}
