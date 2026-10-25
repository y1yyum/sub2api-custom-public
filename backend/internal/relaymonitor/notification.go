package relaymonitor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type NotificationChannel struct {
	Config          map[string]string `json:"config"`
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Enabled         bool              `json:"enabled"`
	HasSecret       bool              `json:"has_secret"`
	Secret          map[string]string `json:"secret,omitempty"`
	EncryptedSecret string            `json:"-"`
}

func signHMAC(key, message string) string {
	h := hmac.New(sha256.New, []byte(key))
	_, _ = h.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
func (m *Module) channels(ctx context.Context) ([]NotificationChannel, error) {
	r, e := m.db.QueryContext(ctx, "SELECT id,name,type,enabled,config FROM relay_monitor_notification_channels ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer func() { _ = r.Close() }()
	out := []NotificationChannel{}
	for r.Next() {
		var c NotificationChannel
		var public []byte
		if e = r.Scan(&c.ID, &c.Name, &c.Type, &c.Enabled, &public); e != nil {
			return nil, e
		}
		_ = json.Unmarshal(public, &c.Config)
		c.HasSecret = true
		out = append(out, c)
	}
	return out, r.Err()
}
func (m *Module) channel(ctx context.Context, id int64) (NotificationChannel, error) {
	var c NotificationChannel
	e := m.db.QueryRowContext(ctx, "SELECT id,name,type,enabled,secret_encrypted FROM relay_monitor_notification_channels WHERE id=$1", id).Scan(&c.ID, &c.Name, &c.Type, &c.Enabled, &c.EncryptedSecret)
	if e != nil {
		return c, e
	}
	plain, e := m.encryptor.Decrypt(c.EncryptedSecret)
	if e != nil {
		return c, errors.New("通知配置解密失败")
	}
	e = json.Unmarshal([]byte(plain), &c.Secret)
	return c, e
}
func (m *Module) deliver(ctx context.Context, c NotificationChannel, title, body string) error {
	cfg := c.Secret
	v := func(keys ...string) string {
		for _, key := range keys {
			if cfg[key] != "" {
				return cfg[key]
			}
		}
		return ""
	}
	endpoint := ""
	var payload map[string]any
	headers := map[string]string{}
	text := title + "\n" + body
	switch c.Type {
	case "telegram":
		base := v("apiBase", "api_base")
		if base == "" {
			base = "https://api.telegram.org"
		}
		endpoint = strings.TrimRight(base, "/") + "/bot" + v("botToken", "bot_token") + "/sendMessage"
		payload = map[string]any{"chat_id": v("chatId", "chat_id"), "text": text}
	case "dingtalk":
		endpoint = v("webhook")
		if secret := v("secret"); secret != "" {
			u, e := url.Parse(endpoint)
			if e != nil {
				return errors.New("无效通知 URL")
			}
			ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
			q := u.Query()
			q.Set("timestamp", ts)
			q.Set("sign", signHMAC(secret, ts+"\n"+secret))
			u.RawQuery = q.Encode()
			endpoint = u.String()
		}
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
	case "wecom":
		endpoint = v("webhook")
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
	case "feishu":
		endpoint = v("webhook")
		payload = map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
		if secret := v("secret"); secret != "" {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			payload["timestamp"] = ts
			payload["sign"] = signHMAC(ts+"\n"+secret, "")
		}
	case "bark":
		endpoint = strings.TrimRight(v("server"), "/")
		if endpoint == "" {
			endpoint = "https://api.day.app"
		}
		endpoint += "/push"
		payload = map[string]any{"device_key": v("deviceKey", "device_key"), "title": title, "body": body, "group": "relay-monitor"}
	case "ntfy":
		endpoint = v("server")
		if endpoint == "" {
			endpoint = "https://ntfy.sh"
		}
		payload = map[string]any{"topic": v("topic"), "title": title, "message": body}
		if v("token") != "" {
			headers["Authorization"] = "Bearer " + v("token")
		}
	case "serverchan":
		key := v("sendKey", "send_key")
		endpoint = "https://sctapi.ftqq.com/" + url.PathEscape(key) + ".send"
		if strings.HasPrefix(key, "sctp") {
			rest := strings.TrimPrefix(key, "sctp")
			if i := strings.Index(rest, "t"); i > 0 {
				n, e := strconv.ParseUint(rest[:i], 10, 64)
				if e != nil {
					return errors.New("无效 ServerChan key")
				}
				endpoint = fmt.Sprintf("https://%d.push.ft07.com/send/%s.send", n, url.PathEscape(key))
			}
		}
		payload = map[string]any{"title": title, "desp": body}
	case "resend":
		endpoint = "https://api.resend.com/emails"
		headers["Authorization"] = "Bearer " + v("apiKey", "api_key")
		payload = map[string]any{"from": v("from"), "to": recipients(v("to")), "subject": title, "text": body}
	case "smtp":
		return m.deliverSMTP(ctx, cfg, title, body)
	case "webhook":
		endpoint = v("url")
		payload = map[string]any{"source": "relay-monitor", "title": title, "body": body, "timestamp": time.Now().UTC().Format(time.RFC3339)}
		if v("headersJson", "headers_json") != "" {
			if json.Unmarshal([]byte(v("headersJson", "headers_json")), &headers) != nil {
				return errors.New("无效通知 headers")
			}
		}
	default:
		return errors.New("不支持的通知渠道")
	}
	b, _ := json.Marshal(payload)
	req, e := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(b)))
	if e != nil {
		return errors.New("无效通知 URL")
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := m.client.Do(req)
	if e != nil {
		return errors.New("通知网络请求失败")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("通知 HTTP %d", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if e != nil {
		return errors.New("读取通知响应失败")
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if result["error"] != nil {
		return errors.New("通知渠道返回业务错误")
	}
	if success, ok := result["success"].(bool); ok && !success {
		return errors.New("通知渠道拒绝请求")
	}
	switch c.Type {
	case "telegram":
		if result["ok"] != true {
			return errors.New("telegram 拒绝通知")
		}
	case "dingtalk", "wecom":
		if result["errcode"] != float64(0) {
			return errors.New("机器人拒绝通知")
		}
	case "feishu":
		code, ok := result["code"]
		if !ok {
			code = result["StatusCode"]
		}
		if code != float64(0) {
			return errors.New("飞书拒绝通知")
		}
	case "bark":
		if result["code"] != float64(200) {
			return errors.New("bark 拒绝通知")
		}
	case "serverchan":
		if result["code"] != float64(0) {
			return errors.New("serverchan 拒绝通知")
		}
	case "resend":
		if id, ok := result["id"].(string); !ok || id == "" {
			return errors.New("resend 未确认通知")
		}
	}
	return nil
}
func recipients(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
}
func (m *Module) deliverSMTP(ctx context.Context, c map[string]string, title, body string) error {
	if err := validateSMTP(c, title); err != nil {
		return err
	}
	host := c["host"]
	port := c["port"]
	if port == "" {
		port = "587"
	}
	conn, e := m.dialDestination(ctx, "tcp", net.JoinHostPort(host, port))
	if e != nil {
		return errors.New("SMTP 连接失败")
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(30 * time.Second)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	if port == "465" || c["secure"] == "true" {
		tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if e = tc.HandshakeContext(ctx); e != nil {
			return errors.New("SMTP TLS 失败")
		}
		conn = tc
	}
	client, e := smtp.NewClient(conn, host)
	if e != nil {
		return errors.New("SMTP 握手失败")
	}
	defer func() { _ = client.Close() }()
	if port != "465" && c["secure"] != "true" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP 必须支持 TLS")
		}
		if client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}) != nil {
			return errors.New("SMTP TLS 失败")
		}
	}
	if c["username"] != "" {
		if client.Auth(smtp.PlainAuth("", c["username"], c["password"], host)) != nil {
			return errors.New("SMTP 认证失败")
		}
	}
	from := c["from"]
	to := recipients(c["to"])
	for _, value := range append(append([]string{from, title}, to...), body) {
		if strings.Contains(value, "\r") {
			return errors.New("SMTP 无效换行")
		}
	}
	if strings.Contains(title, "\n") || strings.Contains(from, "\n") || len(to) == 0 {
		return errors.New("SMTP 无效邮件头")
	}
	if client.Mail(from) != nil {
		return errors.New("SMTP 发件人被拒绝")
	}
	for _, address := range to {
		if strings.Contains(address, "\n") || client.Rcpt(strings.TrimSpace(address)) != nil {
			return errors.New("SMTP 收件人被拒绝")
		}
	}
	w, e := client.Data()
	if e != nil {
		return errors.New("SMTP DATA 失败")
	}
	_, e = fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: =?UTF-8?B?%s?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", from, strings.Join(to, ","), base64.StdEncoding.EncodeToString([]byte(title)), strings.ReplaceAll(body, "\n", "\r\n"))
	if e != nil {
		return errors.New("SMTP 写入失败")
	}
	if w.Close() != nil {
		return errors.New("SMTP 未确认接收")
	}
	return client.Quit()
}
func (m *Module) enqueueEvent(ctx context.Context, tx *sql.Tx, key string, station *int64, kind, title, body string) error {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO relay_monitor_events(event_key,station_id,type,title,body)VALUES(NULLIF($1,''),$2,$3,$4,$5) ON CONFLICT(event_key) DO NOTHING RETURNING id`, key, station, kind, title, body).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var rules map[string][]int64
	var rulesRaw []byte
	err = tx.QueryRowContext(ctx, "SELECT value FROM relay_monitor_meta WHERE key=$1", "notification-rules").Scan(&rulesRaw)
	if err == nil {
		err = json.Unmarshal(rulesRaw, &rules)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	ids, configured := rules[kind]
	if !configured {
		_, err = tx.ExecContext(ctx, `INSERT INTO relay_monitor_outbox(event_id,channel_id) SELECT $1,id FROM relay_monitor_notification_channels WHERE enabled`, id)
		return err
	}
	for _, cid := range ids {
		if _, err = tx.ExecContext(ctx, `INSERT INTO relay_monitor_outbox(event_id,channel_id) SELECT $1,id FROM relay_monitor_notification_channels WHERE id=$2 AND enabled ON CONFLICT DO NOTHING`, id, cid); err != nil {
			return err
		}
	}
	return nil
}

type alertState struct {
	FailCount int                  `json:"fail_count"`
	Failed    bool                 `json:"failed"`
	Last      map[string]time.Time `json:"last"`
	Active    map[string]bool      `json:"active"`
}

func (m *Module) evaluateAlerts(ctx context.Context, tx *sql.Tx, s Station, snap Snapshot, cfg Settings) error {
	if _, e := tx.ExecContext(ctx, `INSERT INTO relay_monitor_alert_state(station_id,token_id,state)VALUES($1,$2,'{}') ON CONFLICT DO NOTHING`, s.ID, snap.TokenID); e != nil {
		return e
	}
	var raw []byte
	if e := tx.QueryRowContext(ctx, `SELECT state FROM relay_monitor_alert_state WHERE station_id=$1 AND token_id=$2 FOR UPDATE`, s.ID, snap.TokenID).Scan(&raw); e != nil {
		return e
	}
	state := alertState{}
	if e := json.Unmarshal(raw, &state); e != nil {
		return e
	}
	if state.Last == nil {
		state.Last = map[string]time.Time{}
	}
	if state.Active == nil {
		state.Active = map[string]bool{}
	}
	events := []string{}
	wasFailed := state.Failed
	if !snap.OK {
		state.FailCount++
		threshold := cfg.ErrorThreshold
		if threshold < 1 {
			threshold = 3
		}
		state.Failed = state.FailCount >= threshold
		if state.Failed {
			events = append(events, "error")
		}
	} else {
		state.FailCount = 0
		state.Failed = false
		if wasFailed {
			events = append(events, "recover")
		}
		balanceAlert := snap.RemainingCNY != nil && s.BalanceMode != "quota" && snap.BalanceKind != "quota"
		if balanceAlert && s.BalanceMode == "shared" {
			selected := int64(0)
			if s.BalanceTokenID != nil {
				selected = *s.BalanceTokenID
			} else if err := tx.QueryRowContext(ctx, `SELECT COALESCE(min(id),0) FROM relay_monitor_tokens WHERE station_id=$1 AND enabled`, s.ID).Scan(&selected); err != nil {
				return err
			}
			balanceAlert = snap.TokenID == selected
		}
		if balanceAlert {
			balance, be := strconv.ParseFloat(*snap.RemainingCNY, 64)
			low, _ := strconv.ParseFloat(s.LowBalance, 64)
			if low == 0 {
				low, _ = strconv.ParseFloat(cfg.LowBalance, 64)
			}
			if be == nil {
				if balance <= 0 {
					events = append(events, "exhaust")
				} else if balance <= low {
					events = append(events, "low")
				}
				if cfg.ETAHours > 0 {
					history, er := tx.QueryContext(ctx, `SELECT `+snapshotColumns+` FROM (SELECT DISTINCT ON(date_trunc('minute',checked_at)) * FROM relay_monitor_snapshots WHERE station_id=$1 AND token_id=$2 AND ok AND checked_at >= $3 ORDER BY date_trunc('minute',checked_at),checked_at DESC) h ORDER BY checked_at`, s.ID, snap.TokenID, snap.CheckedAt.Add(-48*time.Hour))
					if er != nil {
						return er
					}
					samples := []Snapshot{}
					for history.Next() {
						point, scanErr := scanSnapshot(history)
						if scanErr != nil {
							_ = history.Close()
							return scanErr
						}
						samples = append(samples, point)
					}
					er = history.Err()
					_ = history.Close()
					if er != nil {
						return er
					}
					if prediction := PredictExhaustion(predictionHistory(samples, snap.CNYPerUSD)); prediction != nil {
						if days, ok := prediction["etaDays"].(float64); ok && days*24 <= float64(cfg.ETAHours) {
							events = append(events, "eta")
						}
					}
				}
			}
		}
	}
	active := map[string]bool{}
	for _, kind := range events {
		active[kind] = true
		if _, sent := state.Last[kind]; s.NoRenewal && sent && (kind == "low" || kind == "exhaust" || kind == "eta") {
			continue
		}
		cooldown := time.Duration(cfg.CooldownMinutes) * time.Minute
		if cooldown < time.Minute {
			cooldown = time.Hour
		}
		if last, ok := state.Last[kind]; ok && snap.CheckedAt.Sub(last) < cooldown {
			continue
		}
		if err := m.enqueueEvent(ctx, tx, "", &s.ID, kind, "Relay Monitor: "+s.Name+" / "+kind, fmt.Sprintf("站点 %s，令牌 %d，事件 %s，检查时间 %s", s.Name, snap.TokenID, kind, snap.CheckedAt.Format(time.RFC3339))); err != nil {
			return err
		}
		state.Last[kind] = snap.CheckedAt
	}
	state.Active = active
	b, _ := json.Marshal(state)
	_, err := tx.ExecContext(ctx, "UPDATE relay_monitor_alert_state SET state=$1 WHERE station_id=$2 AND token_id=$3", string(b), s.ID, snap.TokenID)
	return err
}
func (m *Module) processNotifications(ctx context.Context) error {
	if _, err := m.db.ExecContext(ctx, `UPDATE relay_monitor_outbox SET status='failed',last_error='最后一次投递结果未知' WHERE status='retry' AND attempts>=12 AND next_attempt_at<=now()`); err != nil {
		return err
	}
	for i := 0; i < 10; i++ {
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var id, cid int64
		var attempts int
		var title, body string
		err = tx.QueryRowContext(ctx, `SELECT o.id,o.channel_id,e.title,e.body,o.attempts FROM relay_monitor_outbox o JOIN relay_monitor_events e ON e.id=o.event_id JOIN relay_monitor_notification_channels c ON c.id=o.channel_id WHERE o.status IN('pending','retry') AND o.next_attempt_at<=now() AND o.attempts<12 AND c.enabled ORDER BY o.id FOR UPDATE OF o SKIP LOCKED LIMIT 1`).Scan(&id, &cid, &title, &body, &attempts)
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return nil
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE relay_monitor_outbox SET attempts=attempts+1,status='retry',next_attempt_at=now()+interval '5 minutes' WHERE id=$1`, id)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		attempts++
		channel, sendErr := m.channel(ctx, cid)
		if sendErr == nil {
			sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			sendErr = m.deliver(sendCtx, channel, title, body)
			cancel()
		}
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if sendErr == nil {
			_, err = m.db.ExecContext(persistCtx, `UPDATE relay_monitor_outbox SET status='delivered',delivered_at=now(),last_error='' WHERE id=$1 AND attempts=$2`, id, attempts)
		} else {
			_, err = m.db.ExecContext(persistCtx, `UPDATE relay_monitor_outbox SET status=CASE WHEN attempts>=12 THEN 'failed' ELSE 'retry' END,last_error='渠道发送失败' WHERE id=$1 AND attempts=$2`, id, attempts)
		}
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) registerNotifications(g *gin.RouterGroup) {
	g.GET("/notifications/channels", func(c *gin.Context) {
		v, e := m.channels(c)
		if e != nil {
			response.Error(c, 500, "读取通知渠道失败")
			return
		}
		response.Success(c, v)
	})
	save := func(c *gin.Context) {
		var v NotificationChannel
		if c.ShouldBindJSON(&v) != nil || strings.TrimSpace(v.Name) == "" {
			response.Error(c, 400, "无效通知渠道")
			return
		}
		valid := map[string]bool{"telegram": true, "dingtalk": true, "wecom": true, "feishu": true, "bark": true, "ntfy": true, "serverchan": true, "resend": true, "smtp": true, "webhook": true}
		if !valid[v.Type] {
			response.Error(c, 400, "不支持的通知渠道")
			return
		}
		cfg, e := m.settings(c)
		if e != nil || !cfg.EncryptionKeyConfigured {
			response.Error(c, 400, "请先配置加密密钥")
			return
		}
		var old NotificationChannel
		if c.Param("id") != "" {
			var ok bool
			v.ID, ok = idParam(c, "id")
			if !ok {
				return
			}
			var er error
			old, er = m.channel(c, v.ID)
			if er != nil {
				fail(c, er)
				return
			}
		} else {
			v.ID = 0
		}
		if v.Type != old.Type {
			old.Secret = nil
		}
		v.Secret = mergeSecretFields(mergeSecretFields(old.Secret, v.Secret), v.Config)
		public := map[string]string{}
		for _, k := range []string{"chatId", "chat_id", "topic", "host", "port", "from", "to", "username", "secure"} {
			if value := v.Secret[k]; value != "" {
				public[k] = value
			}
		}
		v.Config = public
		configJSON, _ := json.Marshal(public)
		b, _ := json.Marshal(v.Secret)
		encrypted, e := m.encryptor.Encrypt(string(b))
		if e != nil {
			response.Error(c, 500, "加密失败")
			return
		}
		if v.ID == 0 {
			e = m.db.QueryRowContext(c, "INSERT INTO relay_monitor_notification_channels(name,type,enabled,secret_encrypted,config)VALUES($1,$2,$3,$4,$5) RETURNING id", v.Name, v.Type, v.Enabled, encrypted, string(configJSON)).Scan(&v.ID)
		} else {
			var result sql.Result
			result, e = m.db.ExecContext(c, "UPDATE relay_monitor_notification_channels SET name=$1,type=$2,enabled=$3,secret_encrypted=$4,config=$6 WHERE id=$5 AND secret_encrypted=$7 AND type=$8", v.Name, v.Type, v.Enabled, encrypted, v.ID, string(configJSON), old.EncryptedSecret, old.Type)
			if e == nil {
				n, err := result.RowsAffected()
				if err != nil {
					e = err
				} else if n != 1 {
					fail(c, errStale)
					return
				}
			}
		}
		if e != nil {
			response.Error(c, 500, "保存通知渠道失败")
			return
		}
		v.HasSecret = len(v.Secret) > 0
		v.Secret = nil
		response.Success(c, v)
	}
	g.POST("/notifications/channels", save)
	g.PUT("/notifications/channels/:id", save)
	g.DELETE("/notifications/channels/:id", func(c *gin.Context) {
		if _, e := m.db.ExecContext(c, "DELETE FROM relay_monitor_notification_channels WHERE id=$1", c.Param("id")); e != nil {
			response.Error(c, 500, "删除失败")
			return
		}
		response.Success(c, nil)
	})
	g.POST("/notifications/channels/:id/test", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		tx, err := m.db.BeginTx(c, nil)
		if err != nil {
			fail(c, err)
			return
		}
		defer func() { _ = tx.Rollback() }()
		var eventID, deliveryID int64
		err = tx.QueryRowContext(c, `INSERT INTO relay_monitor_events(type,title,body) VALUES('test','Relay Monitor 测试','通知渠道测试') RETURNING id`).Scan(&eventID)
		if err == nil {
			err = tx.QueryRowContext(c, `INSERT INTO relay_monitor_outbox(event_id,channel_id) SELECT $1,id FROM relay_monitor_notification_channels WHERE id=$2 AND enabled RETURNING id`, eventID, id).Scan(&deliveryID)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			fail(c, err)
			return
		}
		response.Success(c, map[string]any{"queued": true, "delivery_id": deliveryID})
	})
	g.GET("/notifications/rules", func(c *gin.Context) {
		v := map[string][]int64{}
		_ = m.readDoc(c, "notification-rules", &v)
		response.Success(c, v)
	})
	g.PUT("/notifications/rules", func(c *gin.Context) {
		var v map[string][]int64
		if c.ShouldBindJSON(&v) != nil {
			response.Error(c, 400, "无效通知规则")
			return
		}
		for key := range v {
			if key != "low" && key != "exhaust" && key != "error" && key != "recover" && key != "eta" && key != "report" {
				response.Error(c, 400, "无效事件")
				return
			}
		}
		if m.writeDoc(c, "notification-rules", v) != nil {
			response.Error(c, 500, "保存失败")
			return
		}
		response.Success(c, v)
	})
	g.GET("/notifications/deliveries", func(c *gin.Context) {
		r, e := m.db.QueryContext(c, `SELECT o.id,o.channel_id,e.type,e.title,o.status,o.attempts,o.next_attempt_at,o.last_error,o.delivered_at FROM relay_monitor_outbox o JOIN relay_monitor_events e ON e.id=o.event_id ORDER BY o.id DESC LIMIT 200`)
		if e != nil {
			response.Error(c, 500, "查询失败")
			return
		}
		defer func() { _ = r.Close() }()
		out := []map[string]any{}
		for r.Next() {
			var id, cid int64
			var kind, title, status, last string
			var attempts int
			var next time.Time
			var delivered *time.Time
			if r.Scan(&id, &cid, &kind, &title, &status, &attempts, &next, &last, &delivered) != nil {
				response.Error(c, 500, "读取失败")
				return
			}
			out = append(out, map[string]any{"id": id, "channel_id": cid, "type": kind, "title": title, "status": status, "attempts": attempts, "next_attempt_at": next, "last_error": last, "delivered_at": delivered})
		}
		response.Success(c, out)
	})
	m.registerReports(g)
}

func validateSMTP(c map[string]string, title string) error {
	if c["host"] == "" || len(recipients(c["to"])) == 0 {
		return errors.New("SMTP 缺少服务器或收件人")
	}
	for _, v := range append([]string{c["from"], title}, recipients(c["to"])...) {
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("SMTP 无效邮件头")
		}
	}
	return nil
}
