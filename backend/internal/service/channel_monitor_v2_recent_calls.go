package service

import (
	"context"
	"errors"
	"time"
)

// ChannelMonitorV2RecentCallsLookback is how far back the call window may
// reach; it equals the retention of the 1-minute facts.
const ChannelMonitorV2RecentCallsLookback = 7 * 24 * time.Hour

var errChannelMonitorV2RecentCallsUnsupported = errors.New("channel monitor v2 repository cannot read recent calls")

// ChannelMonitorV2CallMinute is one minute of one platform/group/model with
// ignored error categories already removed from Calls.
type ChannelMonitorV2CallMinute struct {
	Start            time.Time
	Platform         string
	GroupID          int64
	Model            string
	Success          int64
	Calls            int64
	CacheRead        int64
	CacheDenominator int64
	DurationSum      int64
	DurationCount    int64
	TTFTSum          int64
	TTFTCount        int64
}

// ChannelMonitorV2RecentCallsRepository is implemented next to the V2
// repository so the upstream interface stays unchanged.
type ChannelMonitorV2RecentCallsRepository interface {
	// GetRecentCalls returns, per platform/group/model, the newest minutes
	// (newest first) until window calls are covered.
	GetRecentCalls(ctx context.Context, filter ChannelMonitorV2Filter, cfg ChannelMonitorV2Config, window int) ([]ChannelMonitorV2CallMinute, error)
}

type ChannelMonitorV2RecentCalls struct {
	// Coverage is nil until the aggregator has produced any data.
	Coverage *ChannelMonitorV2Coverage
	Minutes  []ChannelMonitorV2CallMinute
}

// RecentCalls reads the latest window calls per row within the lookback,
// using the same platform, group and model scope as Matrix.
func (s *ChannelMonitorV2Service) RecentCalls(ctx context.Context, filter ChannelMonitorV2Filter, window int) (*ChannelMonitorV2RecentCalls, error) {
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	reader, ok := s.repo.(ChannelMonitorV2RecentCallsRepository)
	if !ok {
		return nil, errChannelMonitorV2RecentCallsUnsupported
	}
	now := s.now().UTC().Truncate(time.Minute).Add(time.Minute)
	filter.Start, filter.End, filter.Bucket = now.Add(-ChannelMonitorV2RecentCallsLookback), now, 0
	minutes, err := reader.GetRecentCalls(ctx, filter, *cfg, window)
	if err != nil {
		return nil, err
	}
	out := &ChannelMonitorV2RecentCalls{Minutes: minutes}
	wm, err := s.repo.GetAggregationWatermark(ctx)
	if err != nil {
		return nil, err
	}
	if wm != nil && wm.HasData && !wm.DataThrough.IsZero() {
		out.Coverage = &ChannelMonitorV2Coverage{
			RequestedStart:   filter.Start,
			RequestedEnd:     filter.End,
			CoverageStart:    filter.Start,
			DataThrough:      wm.DataThrough,
			ComputedAt:       wm.LastSuccessfulAt,
			CoverageComplete: true,
		}
	}
	return out, nil
}
