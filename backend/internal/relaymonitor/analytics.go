package relaymonitor

import (
	"context"
	"encoding/json"

	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"math/big"

	"strconv"
	"time"
)

type Purchase struct {
	ID        int64     `json:"id"`
	StationID int64     `json:"station_id"`
	AmountCNY string    `json:"amount_cny"`
	StartAt   time.Time `json:"start_at"`
	EndAt     time.Time `json:"end_at"`
}

func purchaseCost(p Purchase, s, e time.Time) string {
	if s.Before(p.StartAt) {
		s = p.StartAt
	}
	if e.After(p.EndAt) {
		e = p.EndAt
	}
	v, ok := new(big.Rat).SetString(p.AmountCNY)
	if !ok || !e.After(s) || !p.EndAt.After(p.StartAt) {
		return "0.00000000"
	}
	v.Mul(v, new(big.Rat).SetFrac64(int64(e.Sub(s)), int64(p.EndAt.Sub(p.StartAt))))
	return v.FloatString(8)
}
func usageWindow(period string, n time.Time, loc *time.Location) (time.Time, time.Time) {
	switch period {
	case "today":
		l := n.In(loc)
		return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc), n
	case "7d":
		return n.Add(-7 * 24 * time.Hour), n
	case "30d":
		return n.Add(-30 * 24 * time.Hour), n
	default:
		return n.Add(-24 * time.Hour), n
	}
}
func previousUsageWindow(period string, start, end time.Time, loc *time.Location) (time.Time, time.Time) {
	if period == "today" {
		return start.In(loc).AddDate(0, 0, -1), end.In(loc).AddDate(0, 0, -1)
	}
	return start.Add(-end.Sub(start)), start
}
func (m *Module) purchases(ctx context.Context, id int64) ([]Purchase, error) {
	r, err := m.db.QueryContext(ctx, "SELECT id,station_id,amount_cny::text,start_at,end_at FROM relay_monitor_purchases WHERE station_id=$1 ORDER BY start_at", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	out := []Purchase{}
	for r.Next() {
		var p Purchase
		if err = r.Scan(&p.ID, &p.StationID, &p.AmountCNY, &p.StartAt, &p.EndAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, r.Err()
}
func (m *Module) analytics(ctx context.Context, s, e time.Time, subject *int64) (map[string]any, error) {
	filter, _ := ctx.Value(analyticsFilterKey{}).(analyticsFilter)
	stations, err := m.stations(ctx)
	if err != nil {
		return nil, err
	}
	rows := []UsageRow{}
	errs := []map[string]any{}
	fixed := new(big.Rat)
	total := new(big.Rat)
	for _, st := range stations {
		if filter.StationID > 0 && st.ID != filter.StationID {
			continue
		}
		if !st.Enabled || !st.IncludeInCost || ((subject == nil && st.SubjectID != nil) || (subject != nil && (st.SubjectID == nil || *st.SubjectID != *subject))) {
			continue
		}
		ps, pe := m.purchases(ctx, st.ID)
		if pe != nil {
			return nil, pe
		}
		hasFixed := false
		for _, p := range ps {
			v, _ := new(big.Rat).SetString(purchaseCost(p, s, e))
			fixed.Add(fixed, v)
			if p.StartAt.Before(e) && p.EndAt.After(s) {
				hasFixed = true
			}
		}
		if hasFixed || st.Type == "fixed" {
			if !hasFixed {
				errs = append(errs, map[string]any{"station_id": st.ID, "error": "固定付费记录缺失"})
			}
			continue
		}
		tokens, te := m.tokens(ctx, st.ID)
		if te != nil {
			return nil, te
		}
		hasEnabledToken := false
		for _, token := range tokens {
			hasEnabledToken = hasEnabledToken || token.Enabled
		}
		if !hasEnabledToken {
			errs = append(errs, map[string]any{"station_id": st.ID, "error": "站点尚未配置启用的令牌"})
		}
		balanceTokenID := int64(0)
		if st.BalanceTokenID != nil {
			balanceTokenID = *st.BalanceTokenID
		} else {
			for _, candidate := range tokens {
				if candidate.Enabled {
					balanceTokenID = candidate.ID
					break
				}
			}
		}
		for _, tok := range tokens {
			if filter.TokenID > 0 && tok.ID != filter.TokenID {
				continue
			}
			if !tok.Enabled {
				continue
			}
			var usage []UsageRow
			ue := m.useCredential(ctx, st, tok, func(cred map[string]string) (map[string]string, error) {
				var er error
				usage, er = m.queryUsage(ctx, st, tok, cred, s, e)
				return cred, er
			})
			if ue != nil || len(usage) == 0 {
				if st.BalanceMode == "independent" || tok.ID == balanceTokenID {
					usage = m.balanceUsage(ctx, st, tok, s, e)
				}
				if len(usage) > 0 {
					ue = nil
				}
			}
			if ue != nil || len(usage) == 0 {
				errs = append(errs, map[string]any{"station_id": st.ID, "token_id": tok.ID, "error": "上游用量查询失败"})
				continue
			}
			for _, r := range usage {
				r.StationID = st.ID
				r.TokenID = tok.ID
				at := s
				if parsed, er := time.Parse(time.RFC3339, r.Time); er == nil {
					at = parsed
				}
				rate, estimated := m.rateAt(ctx, st, tok, at)
				r.Estimated = r.Estimated || estimated || r.Time == ""
				if r.Currency == "CNY" || st.Type == "jucode" {
					rate = "1"
				}
				cost, ok := new(big.Rat).SetString(r.Cost)
				fx, fok := new(big.Rat).SetString(rate)
				if ok && fok {
					cost.Mul(cost, fx)
					r.CostCNY = cost.FloatString(8)
					if !hasFixed {
						total.Add(total, cost)
					}
				} else {
					r.CostCNY = ""
					r.Estimated = true
					errs = append(errs, map[string]any{"station_id": st.ID, "token_id": tok.ID, "model": r.Model, "error": "用量金额或历史汇率未知，未计入成本合计"})
				}
				rows = append(rows, r)
			}
		}
	}
	total.Add(total, fixed)
	return map[string]any{"start": s, "end": e, "rows": rows, "fixed_cost_cny": fixed.FloatString(8), "total_cost_cny": total.FloatString(8), "partial": len(errs) > 0, "errors": errs}, nil
}
func (m *Module) registerAnalytics(g *gin.RouterGroup) {
	handler := func(c *gin.Context) {
		cfg, err := m.settings(c)
		if err != nil {
			response.Error(c, 500, "读取设置失败")
			return
		}
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			loc = time.UTC
		}
		period := c.DefaultQuery("window", c.DefaultQuery("period", "today"))
		s, e := usageWindow(period, time.Now(), loc)
		var subject *int64
		if raw := c.Query("subject_id"); raw != "" {
			id, er := strconv.ParseInt(raw, 10, 64)
			if er != nil {
				response.Error(c, 400, "无效主体")
				return
			}
			subject = &id
		}
		stationID, _ := strconv.ParseInt(c.Query("station_id"), 10, 64)
		tokenID, _ := strconv.ParseInt(c.Query("token_id"), 10, 64)
		days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
		if days != 7 && days != 14 && days != 30 {
			days = 30
		}
		ctx := context.WithValue(c, analyticsFilterKey{}, analyticsFilter{StationID: stationID, TokenID: tokenID, Days: days})
		data, err := m.analytics(ctx, s, e, subject)
		if err != nil {
			response.Error(c, 500, "用量查询失败")
			return
		}
		previousStart, previousEnd := previousUsageWindow(period, s, e, loc)
		prev, err := m.analytics(ctx, previousStart, previousEnd, subject)
		if err != nil {
			response.Error(c, 500, "同期查询失败")
			return
		}
		data["previous"] = prev
		if c.FullPath() == "/api/v1/admin/relay-monitor/analytics" {
			m.analyticsDetails(ctx, s, e, subject, data)
		}
		response.Success(c, data)
	}
	g.GET("/usage", handler)
	g.GET("/analytics", handler)
	g.GET("/stations/:id/purchases", func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		p, err := m.purchases(c, id)
		if err != nil {
			response.Error(c, 500, "查询失败")
			return
		}
		response.Success(c, p)
	})
	g.POST("/stations/:id/purchases", func(c *gin.Context) {
		var p Purchase
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if c.ShouldBindJSON(&p) != nil || !p.EndAt.After(p.StartAt) {
			response.Error(c, 400, "无效采购区间")
			return
		}
		v, ok := new(big.Rat).SetString(p.AmountCNY)
		if !ok || v.Sign() < 0 {
			response.Error(c, 400, "无效金额")
			return
		}
		err := m.db.QueryRowContext(c, "INSERT INTO relay_monitor_purchases(station_id,amount_cny,start_at,end_at) VALUES($1,$2,$3,$4) RETURNING id", id, p.AmountCNY, p.StartAt, p.EndAt).Scan(&p.ID)
		if err != nil {
			response.Error(c, 500, "保存失败")
			return
		}
		p.StationID = id
		response.Success(c, p)
	})
	g.DELETE("/stations/:id/purchases/:purchaseID", func(c *gin.Context) {
		r, err := m.db.ExecContext(c, "DELETE FROM relay_monitor_purchases WHERE id=$1 AND station_id=$2", c.Param("purchaseID"), c.Param("id"))
		if err != nil {
			response.Error(c, 500, "删除失败")
			return
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			response.Error(c, 404, "采购记录不存在")
			return
		}
		response.Success(c, nil)
	})
	g.PUT("/stations/:id/purchases/:purchaseID", func(c *gin.Context) {
		var p Purchase
		if c.ShouldBindJSON(&p) != nil || !p.EndAt.After(p.StartAt) {
			response.Error(c, 400, "无效采购区间")
			return
		}
		v, ok := new(big.Rat).SetString(p.AmountCNY)
		if !ok || v.Sign() < 0 {
			response.Error(c, 400, "无效金额")
			return
		}
		result, err := m.db.ExecContext(c, "UPDATE relay_monitor_purchases SET amount_cny=$1,start_at=$2,end_at=$3 WHERE id=$4 AND station_id=$5", p.AmountCNY, p.StartAt, p.EndAt, c.Param("purchaseID"), c.Param("id"))
		if err != nil {
			response.Error(c, 500, "保存失败")
			return
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			response.Error(c, 404, "采购记录不存在")
			return
		}
		response.Success(c, map[string]bool{"updated": true})
	})
	m.registerOwn(g)
}

func (m *Module) rateAt(ctx context.Context, s Station, t Token, at time.Time) (string, bool) {
	lookup := func(token any, current *string) (*string, bool) {
		var rate *string
		er := m.db.QueryRowContext(ctx, `SELECT new_rate::text FROM relay_monitor_rate_changes WHERE station_id=$1 AND token_id IS NOT DISTINCT FROM $2::bigint AND effective_at<=$3 ORDER BY effective_at DESC,id DESC LIMIT 1`, s.ID, token, at).Scan(&rate)
		if er == nil {
			return rate, false
		}
		er = m.db.QueryRowContext(ctx, `SELECT old_rate::text FROM relay_monitor_rate_changes WHERE station_id=$1 AND token_id IS NOT DISTINCT FROM $2::bigint AND effective_at>$3 ORDER BY effective_at,id LIMIT 1`, s.ID, token, at).Scan(&rate)
		if er == nil {
			return rate, false
		}
		return current, true
	}
	rate, estimated := lookup(t.ID, t.CNYPerUSDOverride)
	if rate != nil {
		return *rate, estimated
	}
	rate, est := lookup(nil, &s.CNYPerUSD)
	if rate == nil {
		return s.CNYPerUSD, true
	}
	return *rate, estimated || est
}
func (m *Module) balanceUsage(ctx context.Context, s Station, t Token, start, end time.Time) []UsageRow {
	samples, err := m.history(ctx, s.ID, start, end)
	if err != nil {
		return nil
	}
	out := []UsageRow{}
	var previous *Snapshot
	for i := range samples {
		snap := &samples[i]
		if snap.TokenID != t.ID || !snap.OK || snap.Remaining == nil {
			continue
		}
		if previous != nil {
			before, bok := new(big.Rat).SetString(*previous.Remaining)
			after, aok := new(big.Rat).SetString(*snap.Remaining)
			if bok && aok {
				drop := new(big.Rat).Sub(before, after)
				if drop.Sign() > 0 {
					if snap.Currency != "CNY" {
						rate, ok := new(big.Rat).SetString(snap.CNYPerUSD)
						if !ok {
							continue
						}
						drop.Mul(drop, rate)
					}
					out = append(out, UsageRow{Model: "balance_estimate", Time: snap.CheckedAt.Format(time.RFC3339), Cost: drop.FloatString(8), CostCNY: drop.FloatString(8), Currency: "CNY", Estimated: true})
				}
			}
		}
		previous = snap
	}
	return out
}

type analyticsFilter struct {
	StationID, TokenID int64
	Days               int
}
type analyticsFilterKey struct{}

func (m *Module) analyticsDetails(ctx context.Context, s, e time.Time, subjectID *int64, data map[string]any) {
	subject := OwnSubject{Type: "local", IncludeLocalCost: true}
	if subjectID != nil {
		var raw []byte
		if m.db.QueryRowContext(ctx, "SELECT document FROM relay_monitor_subjects WHERE id=$1", *subjectID).Scan(&raw) != nil || json.Unmarshal(raw, &subject) != nil {
			data["partial"] = true
			return
		}
	}
	var keys struct {
		APIKeyIDs []int64 `json:"api_key_ids"`
	}
	_ = m.readDoc(ctx, "own-admin-keys", &keys)
	if subjectID == nil {
		subject.ResoldAPIKeyIDs = keys.APIKeyIDs
	}
	own, err := m.ownAnalytics(ctx, s, e, "model", subject, false)
	if err != nil {
		data["partial"] = true
		data["revenue_error"] = "收入查询失败"
	} else {
		data["revenue"] = own
	}
	fx := subject.CNYPerUSD
	if fx == "" {
		fx = "1"
	}
	wallet := new(big.Rat)
	if own != nil {
		if raw, ok := own["wallet_revenue_usd"].(string); ok {
			wallet.SetString(raw)
		} else if rows, ok := own["rows"].([]map[string]any); ok {
			for _, row := range rows {
				if v, ok := new(big.Rat).SetString(fmt.Sprint(row["wallet_revenue_usd"])); ok {
					wallet.Add(wallet, v)
				}
			}
		}
	}
	rate, valid := new(big.Rat).SetString(fx)
	cost, costValid := new(big.Rat).SetString(fmt.Sprint(data["total_cost_cny"]))
	if valid && costValid {
		revenue := new(big.Rat).Mul(wallet, rate)
		data["wallet_revenue_cny"] = revenue.FloatString(8)
		data["profit_cny"] = new(big.Rat).Sub(revenue, cost).FloatString(8)
		if revenue.Sign() > 0 {
			data["profit_margin"] = new(big.Rat).Quo(new(big.Rat).Sub(revenue, cost), revenue).FloatString(8)
		}
	}
	data["profit_complete"] = own != nil && own["partial"] == false && providerNum(own["subscription_usage_usd"]).IsZero() && data["partial"] == false
	data["profit_note"] = "钱包收入减上游成本；订阅实际收入未完整归因"
	filter, _ := ctx.Value(analyticsFilterKey{}).(analyticsFilter)
	rows, err := m.db.QueryContext(ctx, `SELECT date_trunc('hour',checked_at),sum(GREATEST(0,drop)*fx)::float8 FROM (SELECT snap.checked_at,CASE WHEN snap.result->>'currency'='CNY' THEN 1 ELSE snap.cny_per_usd END AS fx,lag(NULLIF(snap.result->>'remaining','')::numeric) OVER(PARTITION BY snap.token_id ORDER BY snap.checked_at)-NULLIF(snap.result->>'remaining','')::numeric AS drop FROM relay_monitor_snapshots snap JOIN relay_monitor_stations st ON st.id=snap.station_id WHERE st.enabled AND st.include_in_cost AND st.subject_id IS NOT DISTINCT FROM $1::bigint AND ($4::bigint=0 OR snap.station_id=$4) AND ($5::bigint=0 OR snap.token_id=$5) AND (st.balance_mode='independent' OR snap.token_id=COALESCE(st.balance_token_id,(SELECT min(id) FROM relay_monitor_tokens WHERE station_id=st.id AND enabled))) AND NOT EXISTS(SELECT 1 FROM relay_monitor_purchases p WHERE p.station_id=st.id AND p.start_at<$3 AND p.end_at>$2) AND snap.ok AND snap.checked_at >= $2 AND snap.checked_at < $3) d GROUP BY 1 ORDER BY 1`, subjectID, e.AddDate(0, 0, -analyticsDays(ctx)), e, filter.StationID, filter.TokenID)
	if err != nil {
		data["trend_partial"] = true
		return
	}
	defer func() { _ = rows.Close() }()
	samples := []ForecastSample{}
	heatmap := []map[string]any{}
	daily := map[string]float64{}
	cfg, cfgErr := m.settings(ctx)
	loc, locErr := time.LoadLocation(cfg.Timezone)
	if cfgErr != nil || locErr != nil {
		loc = time.UTC
		data["trend_partial"] = true
	}
	for rows.Next() {
		var at time.Time
		var cost float64
		if rows.Scan(&at, &cost) != nil {
			data["trend_partial"] = true
			return
		}
		samples = append(samples, ForecastSample{T: at.UnixMilli(), Cost: cost})
		heatmap = append(heatmap, map[string]any{"time": at, "cost_cny": cost, "estimated": true})
		daily[at.In(loc).Format("2006-01-02")] += cost
	}
	if rows.Err() != nil {
		data["trend_partial"] = true
	}
	_ = rows.Close()
	purchases, perr := m.db.QueryContext(ctx, `SELECT p.id,p.station_id,p.amount_cny::text,p.start_at,p.end_at FROM relay_monitor_purchases p JOIN relay_monitor_stations st ON st.id=p.station_id WHERE st.enabled AND st.include_in_cost AND st.subject_id IS NOT DISTINCT FROM $1::bigint AND ($4::bigint=0 OR st.id=$4) AND p.start_at<$3 AND p.end_at>$2`, subjectID, e.AddDate(0, 0, -analyticsDays(ctx)), e, filter.StationID)
	if perr != nil {
		data["trend_partial"] = true
	} else {
		for purchases.Next() {
			var p Purchase
			if purchases.Scan(&p.ID, &p.StationID, &p.AmountCNY, &p.StartAt, &p.EndAt) != nil {
				data["trend_partial"] = true
				break
			}
			for at := e.AddDate(0, 0, -analyticsDays(ctx)).Truncate(time.Hour); at.Before(e); at = at.Add(time.Hour) {
				from, to := at, at.Add(time.Hour)
				if start := e.AddDate(0, 0, -analyticsDays(ctx)); from.Before(start) {
					from = start
				}
				if to.After(e) {
					to = e
				}
				amount, _ := strconv.ParseFloat(purchaseCost(p, from, to), 64)
				if amount == 0 {
					continue
				}
				samples = append(samples, ForecastSample{T: at.UnixMilli(), Cost: amount})
				heatmap = append(heatmap, map[string]any{"time": at, "cost_cny": amount, "estimated": false, "source": "fixed_purchase"})
				daily[at.In(loc).Format("2006-01-02")] += amount
			}
		}
		if purchases.Err() != nil {
			data["trend_partial"] = true
		}
		_ = purchases.Close()
	}
	data["heatmap"] = heatmap
	data["trend"] = daily
	data["trend_estimated"] = true
	data["forecast_hourly"] = ForecastHourly(samples, 24)
	daySamples := []ForecastSample{}
	for day, cost := range daily {
		at, _ := time.Parse("2006-01-02", day)
		daySamples = append(daySamples, ForecastSample{T: at.UnixMilli(), Cost: cost})
	}
	data["forecast_daily"] = ForecastDaily(daySamples, 7)
}

func analyticsDays(ctx context.Context) int {
	f, _ := ctx.Value(analyticsFilterKey{}).(analyticsFilter)
	if f.Days == 7 || f.Days == 14 {
		return f.Days
	}
	return 30
}
