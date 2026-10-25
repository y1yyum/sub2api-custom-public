package relaymonitor

import (
	"context"
	"errors"
	"github.com/shopspring/decimal"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (m *Module) queryUsage(ctx context.Context, s Station, t Token, credential map[string]string, start, end time.Time) ([]UsageRow, error) {
	if !end.After(start) {
		return nil, errors.New("用量窗口结束时间必须晚于开始时间")
	}
	if end.Sub(start) > 366*24*time.Hour {
		return nil, errors.New("用量窗口不可超过 366 天")
	}
	c := credential
	if c == nil {
		c = map[string]string{}
	}
	if t.AccountID != nil {
		c = providerCopy(c)
		s.Type = strings.TrimSuffix(s.Type, "-password")
		c["_linked_account"] = "true"
	}
	changed := false
	out := []UsageRow{}
	var rows []providerBody
	var currency = "USD"
	var estimated bool
	var records bool
	switch s.Type {
	case "fixed", "newapi-key", "monoize-key":
		return nil, errors.New("该凭据类型不提供用量明细")
	case "newapi":
		last := end.Add(-time.Nanosecond).Unix()
		p := url.Values{"start_timestamp": {strconv.FormatInt(start.Unix(), 10)}, "end_timestamp": {strconv.FormatInt(last, 10)}}
		b, e := m.providerGet(ctx, s, c, "/api/data/self?"+p.Encode(), &changed)
		if e != nil {
			return nil, e
		}
		if _, ok := b["data"].([]any); !ok {
			return nil, errors.New("站点未开启数据看板（DataExportEnabled）")
		}
		rows = providerRows(b["data"])
	case "jucode":
		currency = "CNY"
		p := url.Values{"start": {start.Format(time.RFC3339Nano)}, "end": {end.Format(time.RFC3339Nano)}, "bucket": {"hour"}}
		b, e := m.providerGet(ctx, s, c, "/v1/me/stats/usage-by-model?"+p.Encode(), &changed)
		if e != nil {
			return nil, e
		}
		rows = providerRows(b["rows"])
	case "sub2api", "sub2api-password":
		midnight := func(at time.Time) bool {
			local := at.In(start.Location())
			return local.Equal(time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, start.Location()))
		}
		if !midnight(start) || !midnight(end) {
			var err error
			rows, err = m.sub2APIUsageRecords(ctx, s, c, start, end, &changed)
			if err != nil {
				return nil, err
			}
			records = true
			break
		}
		p := url.Values{"start_date": {start.Format("2006-01-02")}, "end_date": {end.Add(-time.Nanosecond).Format("2006-01-02")}, "timezone": {start.Location().String()}, "granularity": {"hour"}, "include_model_stats": {"true"}, "include_trend": {"true"}}
		b, e := m.providerGet(ctx, s, c, "/api/v1/usage/dashboard/snapshot-v2?"+p.Encode(), &changed)
		if e != nil {
			b, e = m.providerGet(ctx, s, c, "/api/v1/usage/dashboard/models?"+p.Encode(), &changed)
		}
		if e != nil {
			return nil, e
		}
		rows = providerRows(providerData(b)["models"])
	case "monoize":
		p := url.Values{"scope": {"self"}, "limit": {"1"}, "time_from": {start.UTC().Format(time.RFC3339Nano)}, "time_to": {end.UTC().Format(time.RFC3339Nano)}}
		b, e := m.providerGet(ctx, s, c, "/api/dashboard/request-logs?"+p.Encode(), &changed)
		if e != nil {
			return nil, e
		}
		// 日志合计支持绝对窗口；analytics 只能查询截止当前的相对窗口。
		row := UsageRow{Model: "all_models", StationID: s.ID, TokenID: t.ID, Requests: providerCount(b["total"]), Currency: currency, Estimated: true}
		if cost, err := providerDecimal(b["total_charge_nano_usd"]); err == nil {
			cost = cost.Div(decimal.NewFromInt(1000000000))
			row.Cost = cost.String()
			row.CostCNY = providerUsageCNY(cost, t, s, currency)
		}
		return []UsageRow{row}, nil
	default:
		return nil, errors.New("不支持的站点类型")
	}
	for _, b := range rows {
		row := UsageRow{StationID: s.ID, TokenID: t.ID, Estimated: estimated, Currency: currency}
		var cost decimal.Decimal
		var costErr error
		switch s.Type {
		case "newapi":
			row.Model = providerString(b["model_name"])
			row.Time = time.Unix(providerCount(b["created_at"]), 0).Format(time.RFC3339)
			row.Requests = providerCount(b["count"])
			row.InputTokens = usageTokenCount(b["token_used"])
			cost, costErr = providerDecimal(b["quota"])
			cost = cost.Div(decimal.NewFromInt(500000))
		case "jucode":
			row.Model = providerString(b["model"])
			row.Time = providerString(b["time"])
			row.Requests = providerCount(b["requests"])
			row.InputTokens = usageTokenCount(b["tokens_in"])
			row.OutputTokens = usageTokenCount(b["tokens_out"])
			row.CacheReadTokens = usageTokenCount(b["cached_tokens_in"])
			row.CacheCreationTokens = usageTokenCount(b["cache_creation_tokens_in"])
			cost, costErr = providerDecimal(b["cost_final_sum"])
		default:
			row.Model = providerString(b["model"])
			row.Requests = providerCount(b["requests"])
			row.InputTokens = usageTokenCount(b["input_tokens"])
			row.OutputTokens = usageTokenCount(b["output_tokens"])
			row.CacheReadTokens = usageTokenCount(b["cache_read_tokens"])
			row.CacheCreationTokens = usageTokenCount(b["cache_creation_tokens"])
			if records {
				row.Time = providerString(b["created_at"])
				row.Requests = 1
			}
			cost, costErr = providerDecimal(b["actual_cost"])
		}
		if row.Model == "" {
			row.Model = "unknown"
		}
		if costErr == nil {
			row.Cost = cost.String()
			row.CostCNY = providerUsageCNY(cost, t, s, currency)
		} else {
			row.Estimated = true
		}
		out = append(out, row)
	}
	return out, nil
}

func usageTokenCount(value any) *int64 {
	count, err := strconv.ParseInt(providerString(value), 10, 64)
	if err != nil || count < 0 {
		return nil
	}
	return &count
}

func (m *Module) sub2APIUsageRecords(ctx context.Context, s Station, c map[string]string, start, end time.Time, changed *bool) ([]providerBody, error) {
	const limit = 20000
	p := url.Values{"start_date": {start.UTC().Format("2006-01-02")}, "end_date": {end.Add(-time.Nanosecond).UTC().Format("2006-01-02")}, "timezone": {"UTC"}, "page_size": {"1000"}, "sort_by": {"id"}, "sort_order": {"asc"}}
	out := []providerBody{}
	seen := map[int64]bool{}
	var total, size int64
	for page := int64(1); page <= 200; page++ {
		p.Set("page", strconv.FormatInt(page, 10))
		body, err := m.providerGet(ctx, s, c, "/api/v1/usage?"+p.Encode(), changed)
		if err != nil {
			return nil, err
		}
		data := providerData(body)
		count, countErr := strconv.ParseInt(providerString(data["total"]), 10, 64)
		pageSize, sizeErr := strconv.ParseInt(providerString(data["page_size"]), 10, 64)
		currentPage, pageErr := strconv.ParseInt(providerString(data["page"]), 10, 64)
		items, itemsOK := data["items"].([]any)
		if countErr != nil || sizeErr != nil || pageErr != nil || !itemsOK || count < 0 || count > limit || pageSize < 1 || pageSize > 1000 || currentPage != page {
			return nil, errors.New("上游用量分页无效或超过 20000 条，无法确认窗口完整性")
		}
		if page == 1 {
			total, size = count, pageSize
		}
		remaining := total - (page-1)*size
		if remaining > size {
			remaining = size
		}
		if count != total || pageSize != size || remaining < 0 || int64(len(items)) != remaining {
			return nil, errors.New("上游用量分页不完整或查询期间发生变化")
		}
		for _, item := range items {
			row := providerObject(item)
			id, idErr := strconv.ParseInt(providerString(row["id"]), 10, 64)
			at, timeErr := time.Parse(time.RFC3339Nano, providerString(row["created_at"]))
			if idErr != nil || id <= 0 || seen[id] || timeErr != nil {
				return nil, errors.New("上游用量记录缺少有效时间或存在重复")
			}
			seen[id] = true
			if !at.Before(start) && at.Before(end) {
				out = append(out, row)
			}
		}
		if page*size >= total {
			return out, nil
		}
	}
	return nil, errors.New("上游用量分页超过安全上限，无法确认窗口完整性")
}
func providerUsageCNY(cost decimal.Decimal, t Token, s Station, currency string) string {
	if currency == "CNY" {
		return cost.String()
	}
	rate := t.EffectiveCNYPerUSD
	if rate == "" {
		rate = s.CNYPerUSD
	}
	d, e := decimal.NewFromString(rate)
	if e != nil || !d.IsPositive() {
		return ""
	}
	return cost.Mul(d).String()
}
