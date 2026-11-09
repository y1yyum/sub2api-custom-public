package edge

import (
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

// StrictStats 使用固定聚合计数，不包含账号、路由或请求标签。
type StrictStats struct {
	Enabled             bool      `json:"enabled"`
	SnapshotRevision    int64     `json:"snapshot_revision"`
	SnapshotExpiresAt   time.Time `json:"snapshot_expires_at"`
	LastRefreshOK       bool      `json:"last_refresh_ok"`
	RefreshSuccesses    uint64    `json:"refresh_successes"`
	RefreshFailures     uint64    `json:"refresh_failures"`
	Acquisitions        uint64    `json:"acquisitions"`
	Selected            uint64    `json:"selected"`
	Unavailable         uint64    `json:"unavailable"`
	SnapshotUnavailable uint64    `json:"snapshot_unavailable"`
	SnapshotExpired     uint64    `json:"snapshot_expired"`
	NoEligibleRoute     uint64    `json:"no_eligible_route"`
	Inflight            uint64    `json:"inflight"`
	Released            uint64    `json:"released"`
	SnapshotLineage     string    `json:"snapshot_lineage,omitempty"`
	LineageChanges      uint64    `json:"lineage_changes,omitempty"`
	InvalidationStats
}

type strictStatsRecord struct {
	Type  string      `json:"type"`
	Phase string      `json:"phase"`
	Stats StrictStats `json:"stats"`
}

type strictDispatch struct {
	cache *shadowDispatch
	mu    sync.Mutex
	stats StrictStats
}

func newStrictDispatch(cfg ShadowDispatchConfig) *strictDispatch {
	return &strictDispatch{cache: newShadowDispatch(cfg), stats: StrictStats{Enabled: true}}
}

func (s *strictDispatch) acquire(grant Grant, model string, permits *dispatch.PermitSet, now time.Time) (dispatch.Decision, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	decision, release := s.cache.selector.Acquire(dispatch.Route{
		PoolRef: grant.PoolRef, PolicyRevision: grant.PolicyRevision, Model: model, Region: grant.Region,
	}, permits, now)
	s.stats.Acquisitions++
	if release == nil {
		s.stats.Unavailable++
		switch decision.Reason {
		case "snapshot_unavailable":
			s.stats.SnapshotUnavailable++
		case "snapshot_expired":
			s.stats.SnapshotExpired++
		case "no_eligible_route":
			s.stats.NoEligibleRoute++
		}
		return decision, nil
	}
	s.stats.Selected++
	s.stats.Inflight++
	var once sync.Once
	return decision, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			release()
			s.stats.Inflight--
			s.stats.Released++
		})
	}
}

func (s *strictDispatch) close() {
	if s != nil {
		s.cache.close()
	}
}

func (h *Handler) StrictStats() StrictStats {
	if h.strict == nil {
		return StrictStats{}
	}
	s := h.strict
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.stats
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	refresh := s.cache.statsLocked()
	stats.SnapshotRevision, stats.SnapshotExpiresAt = refresh.SnapshotRevision, refresh.SnapshotExpiresAt
	stats.LastRefreshOK, stats.RefreshSuccesses, stats.RefreshFailures = refresh.LastRefreshOK, refresh.RefreshSuccesses, refresh.RefreshFailures
	stats.SnapshotLineage, stats.LineageChanges = refresh.SnapshotLineage, refresh.LineageChanges
	stats.InvalidationStats = refresh.InvalidationStats
	return stats
}
