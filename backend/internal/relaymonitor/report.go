package relaymonitor

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"time"
)

func (m *Module) reportPreview(ctx context.Context, date string) (map[string]any, error) {
	cfg, e := m.settings(ctx)
	if e != nil {
		return nil, e
	}
	loc, e := time.LoadLocation(cfg.Timezone)
	if e != nil {
		loc = time.UTC
	}
	var start time.Time
	if date == "" {
		n := time.Now().In(loc)
		start = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	} else {
		start, e = time.ParseInLocation("2006-01-02", date, loc)
		if e != nil {
			return nil, e
		}
	}
	end := start.AddDate(0, 0, 1)
	data, e := m.analytics(ctx, start, end, nil)
	if e != nil {
		return nil, e
	}
	var adminKeys struct {
		APIKeyIDs []int64 `json:"api_key_ids"`
	}
	_ = m.readDoc(ctx, "own-admin-keys", &adminKeys)
	own, e := m.ownAnalytics(ctx, start, end, "model", OwnSubject{Type: "local", ResoldAPIKeyIDs: adminKeys.APIKeyIDs}, false)
	if e != nil {
		return nil, e
	}
	data["own"] = own
	wallet := providerNum(own["wallet_revenue_usd"])
	cost := providerNum(data["total_cost_cny"])
	data["wallet_revenue_cny"] = wallet.String()
	data["profit_cny"] = wallet.Sub(cost).String()
	data["profit_complete"] = providerNum(own["subscription_usage_usd"]).IsZero() && data["partial"] == false
	data["date"] = start.Format("2006-01-02")
	data["timezone"] = loc.String()
	data["title"] = "Relay Monitor 日报 " + start.Format("2006-01-02")
	data["body"] = fmt.Sprintf("日期：%s (%s)\n上游成本：%v CNY\n固定成本：%v CNY\n数据不完整：%v\n订阅收入：未完整归因", start.Format("2006-01-02"), loc.String(), data["total_cost_cny"], data["fixed_cost_cny"], data["partial"])
	data["body"] = fmt.Sprint(data["body"]) + fmt.Sprintf("\n钱包收入：%v CNY (本地主体默认汇率 1)\n钱包利润：%v CNY\n订阅用量：%v USD，实际订阅收入未完整归因", data["wallet_revenue_cny"], data["profit_cny"], own["subscription_usage_usd"])
	stations, err := m.stations(ctx)
	if err != nil {
		return nil, err
	}
	for _, station := range stations {
		if !station.Enabled {
			continue
		}
		tokens, err := m.tokens(ctx, station.ID)
		if err != nil {
			return nil, err
		}
		latest, err := m.latest(ctx, station.ID)
		if err != nil {
			return nil, err
		}
		value, _, failed, estimated := currentBalance(station, tokens, latest)
		balance := "Unknown"
		if value != nil {
			balance = *value + " CNY"
		}
		data["body"] = fmt.Sprint(data["body"]) + fmt.Sprintf("\n%s：%s，查询失败数：%d，估算：%t", station.Name, balance, failed, estimated)
	}
	return data, nil
}
func (m *Module) queueReport(ctx context.Context, data map[string]any, key string) error {
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	if e = m.enqueueEvent(ctx, tx, key, nil, "report", fmt.Sprint(data["title"]), fmt.Sprint(data["body"])); e != nil {
		return e
	}
	return tx.Commit()
}
func (m *Module) scheduleReports(ctx context.Context) error {
	cfg, e := m.settings(ctx)
	if e != nil || !cfg.DailyReportEnabled {
		return e
	}
	loc, e := time.LoadLocation(cfg.Timezone)
	if e != nil {
		return e
	}
	clock := cfg.DailyReportTime
	if clock == "" {
		clock = "09:00"
	}
	now := time.Now().In(loc)
	due, e := time.ParseInLocation("2006-01-02 15:04", now.Format("2006-01-02")+" "+clock, loc)
	if e != nil {
		return e
	}
	if now.Before(due) {
		return nil
	}
	key := "daily:" + now.Format("2006-01-02") + ":" + loc.String()
	var exists bool
	if e = m.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM relay_monitor_events WHERE event_key=$1)", key).Scan(&exists); e != nil || exists {
		return e
	}
	data, e := m.reportPreview(ctx, "")
	if e != nil {
		return e
	}
	return m.queueReport(ctx, data, key)
}
func (m *Module) registerReports(g *gin.RouterGroup) {
	g.GET("/reports/preview", func(c *gin.Context) {
		v, e := m.reportPreview(c, c.Query("date"))
		if e != nil {
			response.Error(c, 400, "日报生成失败，请检查日期和数据源")
			return
		}
		response.Success(c, v)
	})
	g.POST("/reports/send", func(c *gin.Context) {
		var req struct {
			Date string `json:"date"`
		}
		if c.Request.ContentLength > 0 && c.ShouldBindJSON(&req) != nil {
			response.Error(c, 400, "无效日期")
			return
		}
		v, e := m.reportPreview(c, req.Date)
		if e == nil {
			e = m.queueReport(c, v, "")
		}
		if e != nil {
			response.Error(c, 500, "日报入队失败")
			return
		}
		response.Success(c, map[string]bool{"queued": true})
	})
}
