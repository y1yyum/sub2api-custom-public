package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// catalogStripEpsilon absorbs float error from spreading a minute over blocks.
const catalogStripEpsilon = 1e-9

// catalogStripTally holds the counts behind one block or one row. Calls
// exclude ignored error categories; values are fractional in calls mode,
// where a minute may be spread over several blocks.
type catalogStripTally struct {
	calls, success             float64
	cacheRead, cacheDenom      float64
	durationSum, durationCount float64
	ttftSum, ttftCount         float64
}

func (t *catalogStripTally) add(o catalogStripTally, share float64) {
	t.calls += o.calls * share
	t.success += o.success * share
	t.cacheRead += o.cacheRead * share
	t.cacheDenom += o.cacheDenom * share
	t.durationSum += o.durationSum * share
	t.durationCount += o.durationCount * share
	t.ttftSum += o.ttftSum * share
	t.ttftCount += o.ttftCount * share
}

func catalogAvg(sum, count float64) *float64 {
	if count < catalogStripEpsilon {
		return nil
	}
	v := sum / count
	return &v
}

func (t catalogStripTally) avgResponseMs() *float64 {
	return catalogAvg(t.durationSum, t.durationCount)
}

// avgTTFTMs is the mean time to first token of successful calls.
func (t catalogStripTally) avgTTFTMs() *float64 { return catalogAvg(t.ttftSum, t.ttftCount) }

// cacheRate is nil when the calls carried no prompt tokens.
func (t catalogStripTally) cacheRate() *float64 {
	if t.cacheDenom < catalogStripEpsilon {
		return nil
	}
	v := math.Min(1, t.cacheRead/t.cacheDenom)
	return &v
}

// catalogTallyFromMetric reads an admin (unredacted) V2 metric. ErrorRate is
// already net of ignored categories, so it recovers the counted errors.
func catalogTallyFromMetric(m ChannelMonitorV2Metric) catalogStripTally {
	counted := math.Round(m.ErrorRate * float64(m.RequestCount))
	t := catalogStripTally{
		calls:      float64(m.SuccessRequests) + counted,
		success:    float64(m.SuccessRequests),
		cacheRead:  float64(m.CacheRateNumerator),
		cacheDenom: float64(m.CacheRateDenominator),
	}
	if m.TTFT.AvgMs != nil && m.TTFT.SampleCount > 0 {
		t.ttftCount = float64(m.TTFT.SampleCount)
		t.ttftSum = *m.TTFT.AvgMs * t.ttftCount
	}
	return t
}

func catalogTallyFromMinute(m ChannelMonitorV2CallMinute) catalogStripTally {
	return catalogStripTally{
		calls:         float64(m.Calls),
		success:       float64(m.Success),
		cacheRead:     float64(m.CacheRead),
		cacheDenom:    float64(m.CacheDenominator),
		durationSum:   float64(m.DurationSum),
		durationCount: float64(m.DurationCount),
		ttftSum:       float64(m.TTFTSum),
		ttftCount:     float64(m.TTFTCount),
	}
}

type catalogStripJudge struct{ cfg CatalogStripConfig }

// health colours a tally by success rate; fewer than min_calls stays unknown.
func (j catalogStripJudge) health(t catalogStripTally) (string, *float64) {
	if t.calls < catalogStripEpsilon || t.calls+catalogStripEpsilon < float64(j.cfg.MinCalls) {
		return CatalogHealthUnknown, nil
	}
	rate := math.Min(1, t.success/t.calls)
	switch {
	case rate+catalogStripEpsilon >= j.cfg.Availability.Green:
		return CatalogHealthHealthy, &rate
	case rate+catalogStripEpsilon >= j.cfg.Availability.Yellow:
		return CatalogHealthWarning, &rate
	}
	return CatalogHealthCritical, &rate
}

func (j catalogStripJudge) cacheLevel(t catalogStripTally) (string, *float64) {
	r := t.cacheRate()
	if r == nil {
		return CatalogCacheLevelNone, nil
	}
	rate := *r
	switch {
	case rate+catalogStripEpsilon >= j.cfg.Cache.High:
		return CatalogCacheLevelHigh, &rate
	case rate+catalogStripEpsilon >= j.cfg.Cache.Mid:
		return CatalogCacheLevelMid, &rate
	case rate+catalogStripEpsilon >= j.cfg.Cache.Low && rate > 0:
		return CatalogCacheLevelLow, &rate
	}
	return CatalogCacheLevelNone, &rate
}

// bucket judges one block. Grey blocks are always drawn at the "none" height.
func (j catalogStripJudge) bucket(start time.Time, t catalogStripTally) CatalogBucket {
	b := CatalogBucket{Start: start, CacheLevel: CatalogCacheLevelNone}
	b.Health, b.SuccessRate = j.health(t)
	switch {
	case t.calls < catalogStripEpsilon:
		b.Reason = CatalogBucketReasonNoData
	case b.Health == CatalogHealthUnknown:
		b.Reason = CatalogBucketReasonLowSample
	default:
		b.CacheLevel, b.CacheRate = j.cacheLevel(t)
		b.TTFTMs = t.avgTTFTMs()
	}
	return b
}

func (j catalogStripJudge) trendBucket(p ChannelMonitorV2TrendPoint) CatalogBucket {
	return j.bucket(p.BucketStart, catalogTallyFromMetric(p.Metrics))
}

func (j catalogStripJudge) emptyBucket(start time.Time) CatalogBucket {
	return CatalogBucket{Start: start, Health: CatalogHealthUnknown, Reason: CatalogBucketReasonNoData, CacheLevel: CatalogCacheLevelNone}
}

// catalogCallBlocks spreads the newest window calls over cfg.Blocks equal
// blocks, oldest block first. minutes must be newest first. A minute cannot
// be split by time, so its counts are shared out in proportion to how many of
// its calls fall into each block. Blocks beyond the available calls stay empty.
func catalogCallBlocks(minutes []ChannelMonitorV2CallMinute, cfg CatalogStripConfig) ([]catalogStripTally, []time.Time) {
	blocks := make([]catalogStripTally, cfg.Blocks)
	starts := make([]time.Time, cfg.Blocks)
	window := float64(cfg.CallWindow)
	width := window / float64(cfg.Blocks)
	pos := 0.0
	for _, m := range minutes {
		if pos >= window-catalogStripEpsilon {
			break
		}
		if m.Calls <= 0 {
			continue
		}
		t := catalogTallyFromMinute(m)
		lo, hi := pos, math.Min(pos+t.calls, window)
		for k := int(lo / width); k < cfg.Blocks && float64(k)*width < hi-catalogStripEpsilon; k++ {
			overlap := math.Min(hi, float64(k+1)*width) - math.Max(lo, float64(k)*width)
			if overlap <= catalogStripEpsilon {
				continue
			}
			blocks[k].add(t, overlap/t.calls)
			starts[k] = m.Start
		}
		pos = hi
	}
	for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
		blocks[i], blocks[j] = blocks[j], blocks[i]
		starts[i], starts[j] = starts[j], starts[i]
	}
	return blocks, starts
}

// catalogRowStatus is the judged availability of one catalog row.
type catalogRowStatus struct {
	health        string
	successRate   *float64
	avgResponseMs *float64
	cacheRate     *float64
	ttftMs        *float64
	buckets       []CatalogBucket
	incidents     []CatalogIncident
}

// catalogCallStatus judges one row in calls mode. Block start times are only
// used to date events: they are cleared before the blocks leave the service,
// so the response cannot be turned into a call rate.
func catalogCallStatus(minutes []ChannelMonitorV2CallMinute, cfg CatalogStripConfig) *catalogRowStatus {
	judge := catalogStripJudge{cfg: cfg}
	blocks, starts := catalogCallBlocks(minutes, cfg)
	out := &catalogRowStatus{buckets: make([]CatalogBucket, len(blocks))}
	var total catalogStripTally
	for i := range blocks {
		total.add(blocks[i], 1)
		out.buckets[i] = judge.bucket(starts[i], blocks[i])
	}
	out.incidents = catalogIncidents(out.buckets, 0)
	for i := range out.buckets {
		out.buckets[i].Start = time.Time{}
	}
	out.health, out.successRate = judge.health(total)
	out.avgResponseMs = total.avgResponseMs()
	out.cacheRate, out.ttftMs = total.cacheRate(), total.avgTTFTMs()
	return out
}

func catalogEmptyCallBuckets(cfg CatalogStripConfig) []CatalogBucket {
	judge := catalogStripJudge{cfg: cfg}
	out := make([]CatalogBucket, cfg.Blocks)
	for i := range out {
		out[i] = judge.emptyBucket(time.Time{})
	}
	return out
}

// catalogTimeStatus judges one matrix row in time mode.
func catalogTimeStatus(item *ChannelMonitorV2MatrixRow, cov ChannelMonitorV2Coverage, cfg CatalogStripConfig) *catalogRowStatus {
	judge := catalogStripJudge{cfg: cfg}
	out := &catalogRowStatus{
		buckets:       catalogBuckets(item.Buckets, cov, judge),
		avgResponseMs: item.Metrics.Duration.AvgMs,
	}
	out.incidents = catalogIncidents(out.buckets, cov.BucketSeconds)
	total := catalogTallyFromMetric(item.Metrics)
	out.health, out.successRate = judge.health(total)
	out.cacheRate, out.ttftMs = total.cacheRate(), total.avgTTFTMs()
	return out
}

// catalogCacheRateForBilling hides the cache rate of rows not billed per token.
func catalogCacheRateForBilling(rate *float64, billingMode string) *float64 {
	if billingMode == string(BillingModeToken) || billingMode == "" {
		return rate
	}
	return nil
}

// catalogBucketsForBilling drops the cache height from rows that are not billed per
// token: their usage carries no prompt cache to measure.
func catalogBucketsForBilling(in []CatalogBucket, billingMode string) []CatalogBucket {
	out := append([]CatalogBucket(nil), in...)
	if billingMode == string(BillingModeToken) || billingMode == "" {
		return out
	}
	for i := range out {
		out[i].CacheLevel, out[i].CacheRate = "", nil
	}
	return out
}

// catalogCallCacheTTL matches the page's auto refresh, so concurrent viewers
// with the same scope share one scan of the 1-minute facts.
const (
	catalogCallCacheTTL     = 60 * time.Second
	catalogCallCacheMaxSize = 256
)

type catalogCallCacheEntry struct {
	at   time.Time
	data *ChannelMonitorV2RecentCalls
}

type catalogCallCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]catalogCallCacheEntry
}

func newCatalogCallCache() *catalogCallCache {
	return &catalogCallCache{now: time.Now, entries: map[string]catalogCallCacheEntry{}}
}

func catalogCallCacheKey(f ChannelMonitorV2Filter, window int) string {
	ids := func(v []int64) string {
		s := append([]int64(nil), v...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		return fmt.Sprint(s)
	}
	strs := func(v []string) string {
		s := append([]string(nil), v...)
		sort.Strings(s)
		return strings.Join(s, ",")
	}
	return fmt.Sprintf("%d|%t|%s|%s|%s|%s", window, f.RestrictGroups, ids(f.AllowedGroupIDs), ids(f.GroupIDs), strs(f.Platforms), strs(f.Models))
}

func (c *catalogCallCache) get(ctx context.Context, f ChannelMonitorV2Filter, window int, load func(context.Context, ChannelMonitorV2Filter, int) (*ChannelMonitorV2RecentCalls, error)) (*ChannelMonitorV2RecentCalls, error) {
	key := catalogCallCacheKey(f, window)
	now := c.now()
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && now.Sub(e.at) < catalogCallCacheTTL {
		c.mu.Unlock()
		return e.data, nil
	}
	c.mu.Unlock()

	data, err := load(ctx, f, window)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= catalogCallCacheMaxSize {
		for k, e := range c.entries {
			if now.Sub(e.at) >= catalogCallCacheTTL {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= catalogCallCacheMaxSize {
			c.entries = map[string]catalogCallCacheEntry{}
		}
	}
	c.entries[key] = catalogCallCacheEntry{at: now, data: data}
	return data, nil
}

// catalogCallMinutesByRow groups minutes by catalog key, newest first. Raw
// model names that normalize to one key are merged minute by minute.
func catalogCallMinutesByRow(minutes []ChannelMonitorV2CallMinute) map[CatalogRowKey][]ChannelMonitorV2CallMinute {
	byRow := map[CatalogRowKey]map[time.Time]ChannelMonitorV2CallMinute{}
	for _, m := range minutes {
		key := NewCatalogRowKey(m.GroupID, m.Platform, m.Model)
		if byRow[key] == nil {
			byRow[key] = map[time.Time]ChannelMonitorV2CallMinute{}
		}
		cur := byRow[key][m.Start]
		cur.Start = m.Start
		cur.Success += m.Success
		cur.Calls += m.Calls
		cur.CacheRead += m.CacheRead
		cur.CacheDenominator += m.CacheDenominator
		cur.DurationSum += m.DurationSum
		cur.DurationCount += m.DurationCount
		cur.TTFTSum += m.TTFTSum
		cur.TTFTCount += m.TTFTCount
		byRow[key][m.Start] = cur
	}
	out := make(map[CatalogRowKey][]ChannelMonitorV2CallMinute, len(byRow))
	for key, set := range byRow {
		list := make([]ChannelMonitorV2CallMinute, 0, len(set))
		for _, m := range set {
			list = append(list, m)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Start.After(list[j].Start) })
		out[key] = list
	}
	return out
}
