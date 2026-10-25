package relaymonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/shopspring/decimal"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type providerBody map[string]any

func providerObject(v any) providerBody {
	if b, ok := v.(map[string]any); ok {
		return b
	}
	return providerBody{}
}
func providerData(b providerBody) providerBody {
	if v, ok := b["data"].(map[string]any); ok {
		return v
	}
	return b
}
func providerString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
func providerDecimal(v any) (decimal.Decimal, error) {
	s := providerString(v)
	if s == "" {
		return decimal.Zero, errors.New("缺少有效金额数据")
	}
	d, e := decimal.NewFromString(s)
	if e != nil || d.Exponent() < -18 || d.Exponent() > 18 || len(s) > 100 {
		return decimal.Zero, errors.New("金额数据格式无效")
	}
	return d, nil
}
func providerAmount(v any) *string {
	d, e := providerDecimal(v)
	if e != nil {
		return nil
	}
	s := d.String()
	return &s
}
func providerNum(v any) decimal.Decimal     { d, _ := providerDecimal(v); return d }
func providerCount(v any) int64             { return providerNum(v).IntPart() }
func providerPtr(d decimal.Decimal) *string { s := d.String(); return &s }
func providerCred(c map[string]string, keys ...string) string {
	for _, k := range keys {
		if c[k] != "" {
			return c[k]
		}
	}
	return ""
}
func providerCopy(c map[string]string) map[string]string {
	r := map[string]string{}
	for k, v := range c {
		r[k] = v
	}
	return r
}
func providerName(b providerBody, keys ...string) string {
	for _, k := range keys {
		if s := providerString(b[k]); s != "" {
			return s
		}
	}
	return ""
}
func providerRows(v any) []providerBody {
	a, _ := v.([]any)
	r := make([]providerBody, 0, len(a))
	for _, x := range a {
		if b, ok := x.(map[string]any); ok {
			r = append(r, b)
		}
	}
	return r
}
func (m *Module) providerRequest(ctx context.Context, base, path, method string, headers map[string]string, body any) (providerBody, int, error) {
	raw := strings.TrimRight(base, "/") + path
	if e := m.validateURL(raw); e != nil {
		return nil, 0, e
	}
	var payload io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, 0, e
		}
		payload = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, raw, payload)
	if e != nil {
		return nil, 0, errors.New("请求地址无效")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := m.client
	if client == nil {
		client = newOutboundClient(m.cfg)
	}
	res, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, errors.New("站点请求失败或超时")
	}
	defer func() { _ = res.Body.Close() }()
	rawBody, e := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024+1))
	if e != nil {
		return nil, res.StatusCode, errors.New("读取站点响应失败")
	}
	if len(rawBody) > 4*1024*1024 {
		return nil, res.StatusCode, errors.New("站点响应超出限制")
	}
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.UseNumber()
	var b providerBody
	if e = decoder.Decode(&b); e != nil || b == nil {
		return nil, res.StatusCode, errors.New("站点响应不是有效 JSON")
	}
	if res.StatusCode >= 300 {
		return b, res.StatusCode, fmt.Errorf("站点返回 HTTP %d", res.StatusCode)
	}
	if success, ok := b["success"].(bool); ok && !success {
		return b, res.StatusCode, errors.New("站点拒绝请求，请检查凭据与权限")
	}
	if code, ok := b["code"].(json.Number); ok && code.String() != "0" {
		return b, res.StatusCode, errors.New("站点返回业务错误，请检查凭据与权限")
	}
	return b, res.StatusCode, nil
}
func providerHeaders(kind string, c map[string]string) map[string]string {
	token := providerCred(c, "access_token", "accessToken", "token", "api_key", "apiKey")
	h := map[string]string{}
	if kind == "newapi" {
		h["Authorization"] = token
		if id := providerCred(c, "user_id", "userId"); id != "" {
			h["New-Api-User"] = id
		}
	} else {
		if strings.HasSuffix(kind, "-key") {
			token = providerCred(c, "api_key", "apiKey", "access_token", "accessToken", "token")
		}
		h["Authorization"] = "Bearer " + strings.TrimSpace(strings.TrimPrefix(token, "Bearer "))
	}
	return h
}
func providerBase(s Station) string {
	b := strings.TrimRight(s.URL, "/")
	if strings.HasPrefix(s.Type, "monoize") {
		for _, suffix := range []string{"/v1", "/api"} {
			if strings.HasSuffix(strings.ToLower(b), suffix) {
				b = b[:len(b)-len(suffix)]
				break
			}
		}
	}
	return b
}
func providerExpiration(c map[string]string) time.Time {
	v := providerCred(c, "expires_at", "expiresAt")
	if t, e := time.Parse(time.RFC3339, v); e == nil {
		return t
	}
	if n, e := strconv.ParseInt(v, 10, 64); e == nil {
		if n > 100000000000 {
			return time.UnixMilli(n)
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}
func (m *Module) providerLogin(ctx context.Context, s Station, c map[string]string, refresh bool) error {
	base := providerBase(s)
	if refresh && s.Type == "sub2api-password" && providerCred(c, "refresh_token", "refreshToken") != "" {
		b, _, e := m.providerRequest(ctx, base, "/api/v1/auth/refresh", "POST", providerHeaders(s.Type, c), providerBody{"refresh_token": providerCred(c, "refresh_token", "refreshToken")})
		d := providerData(b)
		if e == nil && providerString(d["access_token"]) != "" && providerString(d["refresh_token"]) != "" && providerCount(d["expires_in"]) > 0 {
			providerSaveTokens(c, d)
			return nil
		}
	}
	if c["password"] == "" {
		return errors.New("缺少自动登录密码")
	}
	capToken, e := m.providerCaptcha(ctx, s)
	if e != nil {
		return e
	}
	path := "/api/v1/auth/login"
	payload := providerBody{"email": providerCred(c, "email", "username"), "password": c["password"]}
	if payload["email"] == "" {
		return errors.New("缺少登录邮箱或用户名")
	}
	if s.Type == "monoize" {
		path = "/api/dashboard/auth/login"
		payload = providerBody{"username": providerCred(c, "username", "email"), "password": c["password"], "captcha_token": capToken}
	} else if capToken != "" {
		payload["turnstile_token"] = capToken
	}
	b, _, e := m.providerRequest(ctx, base, path, "POST", nil, payload)
	if e != nil {
		return e
	}
	d := providerData(b)
	if d["requires_2fa"] == true {
		return errors.New("账号启用两步验证（2FA），请改用登录令牌模式")
	}
	if s.Type == "monoize" {
		if providerString(d["token"]) == "" {
			return errors.New("登录响应缺少会话令牌")
		}
		c["access_token"] = providerString(d["token"])
		delete(c, "refresh_token")
		delete(c, "expires_at")
	} else {
		if providerString(d["access_token"]) == "" {
			return errors.New("登录响应缺少 access_token")
		}
		providerSaveTokens(c, d)
	}
	c["last_login_at"] = time.Now().UTC().Format(time.RFC3339)
	return nil
}
func providerSaveTokens(c map[string]string, d providerBody) {
	c["access_token"] = providerString(d["access_token"])
	c["refresh_token"] = providerString(d["refresh_token"])
	seconds := providerCount(d["expires_in"])
	if seconds <= 0 {
		seconds = 23 * 3600
	}
	c["expires_at"] = time.Now().Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339)
}
func (m *Module) providerGet(ctx context.Context, s Station, c map[string]string, path string, rotated *bool) (providerBody, error) {
	auto := c["_linked_account"] != "true" && (s.Type == "sub2api-password" || s.Type == "monoize")
	if auto && (providerCred(c, "access_token", "accessToken", "token") == "" || (s.Type == "sub2api-password" && providerExpiration(c).Before(time.Now().Add(2*time.Minute)))) {
		if e := m.providerLogin(ctx, s, c, true); e != nil {
			return nil, e
		}
		*rotated = true
	}
	if providerCred(c, "access_token", "accessToken", "token", "api_key", "apiKey") == "" {
		return nil, errors.New("缺少访问令牌或 API Key")
	}
	b, status, e := m.providerRequest(ctx, providerBase(s), path, "GET", providerHeaders(s.Type, c), nil)
	if status == 401 && auto {
		if e = m.providerLogin(ctx, s, c, true); e != nil {
			return nil, e
		}
		*rotated = true
		b, _, e = m.providerRequest(ctx, providerBase(s), path, "GET", providerHeaders(s.Type, c), nil)
	}
	return b, e
}
func (m *Module) query(ctx context.Context, s Station, t Token, credential map[string]string) (QueryResult, error) {
	r := QueryResult{Currency: "USD", BalanceKind: "shared"}
	if s.Type == "fixed" {
		r.BalanceKind = "independent"
		return r, nil
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
	var path, field string
	switch s.Type {
	case "newapi":
		path = "/api/user/self"
		field = "quota"
	case "newapi-key":
		path = "/dashboard/billing/subscription"
		field = "hard_limit_usd"
		r.BalanceKind = "independent"
	case "sub2api", "sub2api-password":
		path = "/api/v1/auth/me"
		field = "balance"
	case "jucode":
		path = "/v1/me"
		field = "balance"
		r.Currency = "CNY"
	case "monoize":
		path = "/api/dashboard/auth/me"
		field = "balance_usd"
	case "monoize-key":
		path = "/user/balance"
		r.BalanceKind = "independent"
	default:
		return r, errors.New("不支持的站点类型")
	}
	b, e := m.providerGet(ctx, s, c, path, &changed)
	if e != nil {
		return r, e
	}
	d := providerData(b)
	if s.Type == "monoize-key" {
		var info providerBody
		for _, row := range providerRows(d["balance_infos"]) {
			if info == nil || row["currency"] == "USD" {
				info = row
			}
			if row["currency"] == "USD" {
				break
			}
		}
		d = info
		field = "total_balance"
		if currency := providerString(info["currency"]); currency != "" {
			if currency != "USD" && currency != "CNY" {
				return r, errors.New("站点返回不支持的币种")
			}
			r.Currency = currency
		}
	}
	amount, e := providerDecimal(d[field])
	if e != nil && (!strings.HasPrefix(s.Type, "sub2api") || !providerNum(d["quota"]).IsPositive()) {
		return r, e
	}
	r.Remaining = providerPtr(amount)
	r.Account = providerName(d, "username", "nickname", "email", "display_name", "name")
	switch s.Type {
	case "newapi":
		r.Remaining = providerPtr(amount.Div(decimal.NewFromInt(500000)))
		r.Used = providerPtr(providerNum(d["used_quota"]).Div(decimal.NewFromInt(500000)))
		r.Requests = providerCount(d["request_count"])
	case "newapi-key":
		usage, err := m.providerGet(ctx, s, c, "/dashboard/billing/usage", &changed)
		if err == nil && usage["total_usage"] != nil {
			used := providerNum(usage["total_usage"]).Div(decimal.NewFromInt(100))
			r.Used = providerPtr(used)
			r.Remaining = providerPtr(decimal.Max(decimal.Zero, amount.Sub(used)))
		} else {
			r.Estimated = true
		}
	case "sub2api", "sub2api-password":
		quota := providerNum(d["quota"])
		used := providerNum(d["quota_used"])
		if d["quota_used"] == nil {
			used = providerNum(d["used_quota"])
		}
		if quota.IsPositive() {
			r.BalanceKind = "quota"
			r.Remaining = providerPtr(decimal.Max(decimal.Zero, quota.Sub(used)))
			r.Used = providerPtr(used)
		} else if total := providerNum(d["total_recharged"]); total.IsPositive() {
			r.Used = providerPtr(decimal.Max(decimal.Zero, total.Sub(amount)))
		} else {
			r.Used = providerPtr(used)
		}
		if stats, err := m.providerGet(ctx, s, c, "/api/v1/usage/dashboard/stats", &changed); err == nil {
			v := providerData(stats)
			r.TodayUsed = providerAmount(v["today_actual_cost"])
			r.Requests = providerCount(v["today_requests"])
			r.Tokens = providerCount(v["today_tokens"])
		}
	case "jucode":
		if life, err := m.providerGet(ctx, s, c, "/v1/me/stats/lifetime", &changed); err == nil {
			r.Used = providerAmount(life["total_cost"])
		}
		mid, now, err := m.providerToday(ctx)
		if err != nil {
			return r, err
		}
		params := url.Values{"start": {mid.Format(time.RFC3339)}, "end": {now.Format(time.RFC3339)}, "bucket": {"hour"}}
		if stats, err := m.providerGet(ctx, s, c, "/v1/me/stats/usage?"+params.Encode(), &changed); err == nil {
			sum := decimal.Zero
			for _, row := range providerRows(stats["buckets"]) {
				sum = sum.Add(providerNum(row["cost_final_sum"]))
				r.Requests += providerCount(row["requests"])
				for _, k := range []string{"tokens_in", "tokens_out", "cached_tokens_in", "cache_creation_tokens_in", "audio_tokens_in", "audio_tokens_out"} {
					r.Tokens += providerCount(row[k])
				}
			}
			r.TodayUsed = providerPtr(sum)
		}
	case "monoize":
		if life, err := m.providerGet(ctx, s, c, "/api/dashboard/request-logs?scope=self&limit=1", &changed); err == nil {
			r.Used = providerPtr(providerNum(life["total_charge_nano_usd"]).Div(decimal.NewFromInt(1000000000)))
		}
		mid, now, err := m.providerToday(ctx)
		if err != nil {
			return r, err
		}
		if stats, err := m.providerGet(ctx, s, c, "/api/dashboard/request-logs?scope=self&limit=1&time_from="+url.QueryEscape(mid.Format(time.RFC3339))+"&time_to="+url.QueryEscape(now.Format(time.RFC3339)), &changed); err == nil {
			r.TodayUsed = providerPtr(providerNum(stats["total_charge_nano_usd"]).Div(decimal.NewFromInt(1000000000)))
			r.Requests = providerCount(stats["total"])
		}
	}
	if changed {
		r.Credential = c
	}
	return r, nil
}

func (m *Module) providerToday(ctx context.Context) (time.Time, time.Time, error) {
	tz := "UTC"
	if m.cfg != nil && m.cfg.Timezone != "" {
		tz = m.cfg.Timezone
	}
	if m.db != nil {
		settings, err := m.settings(ctx)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		tz = settings.Timezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc), now, nil
}
