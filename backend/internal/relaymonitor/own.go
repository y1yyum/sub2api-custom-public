package relaymonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"strconv"
	"time"
)

type OwnSubject struct {
	CNYPerUSD        string  `json:"cny_per_usd"`
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Type             string  `json:"type"`
	StationID        *int64  `json:"station_id"`
	AdminUserIDs     []int64 `json:"admin_user_ids"`
	ResoldAPIKeyIDs  []int64 `json:"resold_api_key_ids"`
	IncludeLocalCost bool    `json:"include_local_cost"`
}

func (m *Module) ownAnalytics(ctx context.Context, s, e time.Time, dimension string, subject OwnSubject, audit bool) (map[string]any, error) {
	if subject.Type == "newapi" {
		return m.externalOwnAnalytics(ctx, s, e, dimension, subject, audit)
	}
	dim := map[string]string{"model": "l.model", "user": "l.user_id::text", "group": "COALESCE(l.group_id::text,'ungrouped')", "channel": "COALESCE(l.channel_id::text,'unknown')", "user_group": "l.user_id::text||':'||COALESCE(l.group_id::text,'ungrouped')"}[dimension]
	if dim == "" {
		dim = "l.model"
	}
	exclusion := "u.role <> 'admin'"
	args := []any{s, e}
	for _, adminID := range subject.AdminUserIDs {
		args = append(args, adminID)
		exclusion += fmt.Sprintf(" AND l.user_id <> $%d", len(args))
	}
	if len(subject.ResoldAPIKeyIDs) > 0 {
		exclusion += " OR l.api_key_id IN ("
		for i, id := range subject.ResoldAPIKeyIDs {
			if i > 0 {
				exclusion += ","
			}
			args = append(args, id)
			exclusion += fmt.Sprintf("$%d", len(args))
		}
		exclusion += ")"
	}
	query := `SELECT ` + dim + `,count(*),COALESCE(sum(l.actual_cost) FILTER(WHERE l.subscription_id IS NULL AND (` + exclusion + `)),0)::text,COALESCE(sum(l.actual_cost) FILTER(WHERE l.subscription_id IS NOT NULL),0)::text,COALESCE(sum(l.input_tokens),0),COALESCE(sum(l.output_tokens),0),COALESCE(sum(l.cache_read_tokens),0),COALESCE(sum(l.cache_creation_tokens),0) FROM usage_logs l JOIN users u ON u.id=l.user_id WHERE l.created_at >= $1 AND l.created_at < $2 GROUP BY 1 ORDER BY 1`
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []map[string]any{}
	for rows.Next() {
		var name, wallet, subscription string
		var requests, input, output, cacheRead, cacheCreation int64
		if err = rows.Scan(&name, &requests, &wallet, &subscription, &input, &output, &cacheRead, &cacheCreation); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"name": name, "requests": requests, "wallet_revenue_usd": wallet, "subscription_usage_usd": subscription, "input_tokens": input, "output_tokens": output, "cache_read_tokens": cacheRead, "cache_creation_tokens": cacheCreation})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	result := map[string]any{"rows": out, "start": s, "end": e, "dimension": dimension, "partial": false, "subscription_revenue_complete": false, "subscription_note": "订阅实际收入未归因，订阅用量单独列示"}
	walletTotal, subscriptionTotal := providerNum("0"), providerNum("0")
	for _, row := range out {
		walletTotal = walletTotal.Add(providerNum(row["wallet_revenue_usd"]))
		subscriptionTotal = subscriptionTotal.Add(providerNum(row["subscription_usage_usd"]))
	}
	result["wallet_revenue_usd"] = walletTotal.String()
	result["subscription_usage_usd"] = subscriptionTotal.String()
	if audit {
		logs, er := m.db.QueryContext(ctx, `SELECT id,created_at,user_id,api_key_id,model,actual_cost::text,subscription_id FROM usage_logs WHERE created_at >= $1 AND created_at < $2 ORDER BY created_at,id LIMIT 20001`, s, e)
		if er != nil {
			return nil, er
		}
		defer func() { _ = logs.Close() }()
		records := []map[string]any{}
		truncated := false
		for logs.Next() {
			var id, uid, key int64
			var at time.Time
			var model, cost string
			var sub *int64
			if er = logs.Scan(&id, &at, &uid, &key, &model, &cost, &sub); er != nil {
				return nil, er
			}
			if len(records) == 20000 {
				truncated = true
				break
			}
			records = append(records, map[string]any{"id": id, "created_at": at, "user_id": uid, "api_key_id": key, "model": model, "actual_cost": cost, "subscription_id": sub})
		}
		result["records"] = records
		result["truncated"] = truncated
		result["partial"] = truncated
		result["limit"] = 20000
		result["coverage"] = map[string]any{"start": s, "end": e, "returned": len(records), "complete": !truncated}
	}
	return result, nil
}
func (m *Module) registerOwn(g *gin.RouterGroup) {
	m.registerOwnBalances(g)
	g.GET("/own/admin-keys", func(c *gin.Context) {
		v := map[string]any{"api_key_ids": []int64{}}
		_ = m.readDoc(c, "own-admin-keys", &v)
		response.Success(c, v)
	})
	g.PUT("/own/admin-keys", func(c *gin.Context) {
		var v struct {
			APIKeyIDs []int64 `json:"api_key_ids"`
		}
		if c.ShouldBindJSON(&v) != nil {
			response.Error(c, 400, "无效参数")
			return
		}
		for _, id := range v.APIKeyIDs {
			if id <= 0 {
				response.Error(c, 400, "无效 API Key ID")
				return
			}
		}
		if m.writeDoc(c, "own-admin-keys", v) != nil {
			response.Error(c, 500, "保存失败")
			return
		}
		response.Success(c, v)
	})
	g.GET("/own/subjects", func(c *gin.Context) {
		rows, err := m.db.QueryContext(c, "SELECT id,document FROM relay_monitor_subjects ORDER BY id")
		if err != nil {
			response.Error(c, 500, "查询失败")
			return
		}
		defer func() { _ = rows.Close() }()
		out := []OwnSubject{}
		for rows.Next() {
			var s OwnSubject
			var b []byte
			if rows.Scan(&s.ID, &b) != nil {
				response.Error(c, 500, "读取失败")
				return
			}
			id := s.ID
			if json.Unmarshal(b, &s) != nil {
				response.Error(c, 500, "读取失败")
				return
			}
			s.ID = id
			out = append(out, s)
		}
		response.Success(c, out)
	})
	save := func(c *gin.Context) {
		var s OwnSubject
		if c.ShouldBindJSON(&s) != nil || s.Name == "" || (s.Type != "local" && s.Type != "newapi") {
			response.Error(c, 400, "无效主体")
			return
		}
		if s.Type == "newapi" && s.StationID == nil {
			response.Error(c, 400, "请选择上游站点")
			return
		}
		if s.CNYPerUSD == "" {
			s.CNYPerUSD = "1"
		}
		if _, er := validateRate(s.CNYPerUSD); er != nil {
			response.Error(c, 400, "无效汇率")
			return
		}
		b, _ := json.Marshal(s)
		var err error
		if c.Param("id") == "" {
			err = m.db.QueryRowContext(c, "INSERT INTO relay_monitor_subjects(document)VALUES($1) RETURNING id", string(b)).Scan(&s.ID)
		} else {
			s.ID, _ = strconv.ParseInt(c.Param("id"), 10, 64)
			_, err = m.db.ExecContext(c, "UPDATE relay_monitor_subjects SET document=$1 WHERE id=$2", string(b), s.ID)
		}
		if err != nil {
			response.Error(c, 500, "保存失败")
			return
		}
		response.Success(c, s)
	}
	g.POST("/own/subjects", save)
	g.PUT("/own/subjects/:id", save)
	g.DELETE("/own/subjects/:id", func(c *gin.Context) {
		if _, err := m.db.ExecContext(c, "DELETE FROM relay_monitor_subjects WHERE id=$1", c.Param("id")); err != nil {
			response.Error(c, 500, "删除失败")
			return
		}
		response.Success(c, nil)
	})
	for _, path := range []string{"/own/analytics", "/own/audit"} {
		audit := path == "/own/audit"
		g.GET(path, func(c *gin.Context) {
			cfg, err := m.settings(c)
			if err != nil {
				response.Error(c, 500, "读取设置失败")
				return
			}
			loc, er := time.LoadLocation(cfg.Timezone)
			if er != nil {
				loc = time.UTC
			}
			period := c.DefaultQuery("window", "today")
			s, e := usageWindow(period, time.Now(), loc)
			subject := OwnSubject{Type: "local", IncludeLocalCost: true}
			var keys struct {
				APIKeyIDs []int64 `json:"api_key_ids"`
			}
			_ = m.readDoc(c, "own-admin-keys", &keys)
			subject.ResoldAPIKeyIDs = keys.APIKeyIDs
			if id := c.Query("subject_id"); id != "" {
				var b []byte
				if m.db.QueryRowContext(c, "SELECT document FROM relay_monitor_subjects WHERE id=$1", id).Scan(&b) != nil || json.Unmarshal(b, &subject) != nil {
					response.Error(c, 404, "主体不存在")
					return
				}
			}
			result, err := m.ownAnalytics(c, s, e, c.DefaultQuery("dimension", "model"), subject, audit)
			if err != nil {
				response.Error(c, 500, "本地用量查询失败")
				return
			}
			previousStart, previousEnd := previousUsageWindow(period, s, e, loc)
			previous, err := m.ownAnalytics(c, previousStart, previousEnd, c.DefaultQuery("dimension", "model"), subject, false)
			if err != nil {
				response.Error(c, 500, "同期查询失败")
				return
			}
			result["previous"] = previous
			var costSubject *int64
			if c.Query("subject_id") != "" {
				id, _ := strconv.ParseInt(c.Query("subject_id"), 10, 64)
				costSubject = &id
			}
			cost, err := m.analytics(c, s, e, costSubject)
			if err != nil {
				result["partial"] = true
			} else {
				result["upstream_cost"] = cost
			}
			response.Success(c, result)
		})
	}
}
func (m *Module) externalOwnAnalytics(ctx context.Context, s, e time.Time, dimension string, subject OwnSubject, audit bool) (map[string]any, error) {
	if subject.StationID == nil {
		return nil, fmt.Errorf("missing station")
	}
	station, err := m.station(ctx, *subject.StationID)
	if err != nil {
		return nil, err
	}
	if station.Type != "newapi" {
		return nil, fmt.Errorf("subject requires NewAPI")
	}
	tokens, err := m.tokens(ctx, station.ID)
	if err != nil {
		return nil, err
	}
	var token *Token
	for i := range tokens {
		if tokens[i].Enabled {
			token = &tokens[i]
			break
		}
	}
	if token == nil {
		return nil, fmt.Errorf("missing administrator credential")
	}
	records := []providerBody{}
	truncated := false
	total := int64(0)
	err = m.useCredential(ctx, station, *token, func(credential map[string]string) (map[string]string, error) {
		changed := false
		if id, er := strconv.ParseInt(providerCred(credential, "user_id", "userId"), 10, 64); er == nil && id > 0 {
			subject.AdminUserIDs = append(subject.AdminUserIDs, id)
		}
		for page := 1; page <= 201; page++ {
			path := fmt.Sprintf("/api/log/?p=%d&page_size=100&type=2&start_timestamp=%d&end_timestamp=%d", page, s.Unix(), e.Unix())
			result, er := m.providerGet(ctx, station, credential, path, &changed)
			if er != nil {
				return credential, er
			}
			data := providerData(result)
			items := providerRows(data["items"])
			if len(items) == 0 {
				items = providerRows(data["logs"])
			}
			if len(items) == 0 {
				items = providerRows(result["data"])
			}
			if n := providerCount(data["total"]); n > total {
				total = n
			}
			for _, item := range items {
				if len(records) == 20000 {
					truncated = true
					break
				}
				records = append(records, item)
			}
			if truncated || len(items) < 100 {
				break
			}
			if total > 0 && int64(len(records)) >= total {
				break
			}
		}
		if total > int64(len(records)) {
			truncated = true
		}
		return credential, nil
	})
	if err != nil {
		return nil, err
	}
	groups := map[string]map[string]any{}
	wallet := providerNum("0")
	subscription := providerNum("0")
	safeRecords := []map[string]any{}
	admins := map[int64]bool{}
	for _, id := range subject.AdminUserIDs {
		admins[id] = true
	}
	resold := map[int64]bool{}
	for _, id := range subject.ResoldAPIKeyIDs {
		resold[id] = true
	}
	for _, row := range records {
		uid := providerCount(row["user_id"])
		keyID := providerCount(row["token_id"])
		at := time.Unix(providerCount(row["created_at"]), 0)
		if at.Before(s) || !at.Before(e) {
			continue
		}
		revenue := providerNum(row["quota"]).Div(providerNum("500000"))
		isSubscription := providerCount(row["subscription_id"]) > 0
		if isSubscription {
			subscription = subscription.Add(revenue)
		} else if !admins[uid] || resold[keyID] {
			wallet = wallet.Add(revenue)
		} else {
			revenue = providerNum("0")
		}
		name := providerString(row["model_name"])
		switch dimension {
		case "user":
			name = strconv.FormatInt(uid, 10)
		case "channel":
			name = providerString(row["channel"])
		case "group":
			name = providerString(row["group"])
		case "user_group":
			name = strconv.FormatInt(uid, 10) + ":" + providerString(row["group"])
		}
		group := groups[name]
		if group == nil {
			group = map[string]any{"name": name, "requests": int64(0), "wallet_revenue_usd": "0", "subscription_usage_usd": "0"}
			groups[name] = group
		}
		requests, _ := group["requests"].(int64)
		group["requests"] = requests + 1
		field := "wallet_revenue_usd"
		if isSubscription {
			field = "subscription_usage_usd"
		}
		group[field] = providerNum(group[field]).Add(revenue).String()
		if audit {
			safeRecords = append(safeRecords, map[string]any{"id": providerCount(row["id"]), "user_id": uid, "api_key_id": keyID, "model": providerString(row["model_name"]), "created_at": at, "actual_cost": revenue.String(), "subscription": isSubscription})
		}
	}
	out := []map[string]any{}
	for _, v := range groups {
		out = append(out, v)
	}
	data := map[string]any{"start": s, "end": e, "rows": out, "wallet_revenue_usd": wallet.String(), "subscription_usage_usd": subscription.String(), "subscription_revenue_complete": false, "partial": truncated, "truncated": truncated, "limit": 20000, "coverage": map[string]any{"returned": len(records), "total": total, "complete": !truncated}}
	if audit {
		data["records"] = safeRecords
	}
	return data, nil
}

func (m *Module) registerOwnBalances(g *gin.RouterGroup) {
	g.GET("/own/user-balances", func(c *gin.Context) {
		rows, err := m.db.QueryContext(c, `SELECT id,username,balance::text,count(*) OVER() FROM users WHERE deleted_at IS NULL AND role<>'admin' ORDER BY id LIMIT 20000`)
		if err != nil {
			response.Error(c, 500, "用户余额查询失败")
			return
		}
		defer func() { _ = rows.Close() }()
		out := []map[string]any{}
		var total int64
		for rows.Next() {
			var id int64
			var username, balance string
			if rows.Scan(&id, &username, &balance, &total) != nil {
				response.Error(c, 500, "用户余额读取失败")
				return
			}
			out = append(out, map[string]any{"user_id": id, "username": username, "balance_usd": balance})
		}
		if rows.Err() != nil {
			response.Error(c, 500, "用户余额读取失败")
			return
		}
		response.Success(c, map[string]any{"rows": out, "total": total, "truncated": total > int64(len(out))})
	})
}
