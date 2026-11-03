package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// channelMonitorV2RecentCallsSQL walks each platform/group/model back from
// its newest minute and keeps minutes until the running call count reaches
// the window. Ignored error categories are not calls at all.
const channelMonitorV2RecentCallsSQL = `
WITH f AS (
	SELECT m.bucket_start, m.platform, m.group_id, m.model, m.success_requests, m.error_requests,
		m.cache_read_tokens, m.input_tokens + m.cache_creation_tokens + m.cache_read_tokens AS cache_denominator,
		m.duration_sum_ms, m.duration_count, m.ttft_sum_ms, m.ttft_count
	FROM channel_monitor_v2_metrics_1m m %s AND m.group_id > 0
), ig AS (
	SELECT e.bucket_start, e.platform, e.group_id, e.model, SUM(e.error_requests) AS ignored
	FROM channel_monitor_v2_error_metrics_1m e %s AND e.group_id > 0
		AND e.error_category = ANY($%d) AND e.taxonomy_version = $%d
	GROUP BY e.bucket_start, e.platform, e.group_id, e.model
), c AS (
	SELECT f.*, f.success_requests + GREATEST(f.error_requests - COALESCE(ig.ignored, 0), 0) AS calls
	FROM f LEFT JOIN ig ON ig.bucket_start = f.bucket_start AND ig.platform = f.platform
		AND ig.group_id = f.group_id AND ig.model = f.model
), w AS (
	SELECT c.*, SUM(c.calls) OVER (
		PARTITION BY c.platform, c.group_id, c.model ORDER BY c.bucket_start DESC ROWS UNBOUNDED PRECEDING
	) AS through
	FROM c WHERE c.calls > 0
)
SELECT bucket_start, platform, group_id, model, success_requests, calls,
	cache_read_tokens, cache_denominator, duration_sum_ms, duration_count, ttft_sum_ms, ttft_count
FROM w WHERE through - calls < $%d
ORDER BY platform, group_id, model, bucket_start DESC`

func (r *channelMonitorV2Repository) GetRecentCalls(ctx context.Context, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, window int) ([]service.ChannelMonitorV2CallMinute, error) {
	out := []service.ChannelMonitorV2CallMinute{}
	if window <= 0 || channelMonitorV2RestrictedGroupScopeEmpty(filter, cfg) {
		return out, nil
	}
	metricsWhere, args := channelMonitorV2Where(filter, cfg, "m")
	errorsWhere, _ := channelMonitorV2Where(filter, cfg, "e")
	ignored := cfg.IgnoredErrorCategories
	if ignored == nil {
		ignored = []string{}
	}
	args = append(args, pq.Array(ignored), service.ChannelMonitorV2TaxonomyVersion, window)
	n := len(args)
	query := fmt.Sprintf(channelMonitorV2RecentCallsSQL, metricsWhere, errorsWhere, n-2, n-1, n)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m service.ChannelMonitorV2CallMinute
		if err := rows.Scan(&m.Start, &m.Platform, &m.GroupID, &m.Model, &m.Success, &m.Calls,
			&m.CacheRead, &m.CacheDenominator, &m.DurationSum, &m.DurationCount, &m.TTFTSum, &m.TTFTCount); err != nil {
			return nil, err
		}
		// Catalog rows only exist for real model names; collapsed "other" traffic has no row.
		if channelMonitorV2DisplayModel(cfg, m.Platform, m.Model) != strings.TrimSpace(m.Model) ||
			!channelMonitorV2ModelSelected(filter, cfg, m.Platform, m.Model) {
			continue
		}
		m.Start = m.Start.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}
