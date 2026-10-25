package relaymonitor

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func (m *Module) Register(admin *gin.RouterGroup) {
	g := admin.Group("/relay-monitor")
	g.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
		c.Next()
	})
	g.GET("/settings", m.getSettings)
	g.PUT("/settings", m.putSettings)
	g.GET("/stations", m.listStations)
	g.POST("/stations", m.createStation)
	g.PUT("/stations/:id", m.updateStation)
	g.DELETE("/stations/:id", m.deleteStation)
	g.GET("/stations/:id/tokens", m.listTokens)
	g.POST("/stations/:id/tokens", m.createToken)
	g.PUT("/stations/:id/tokens/:tokenId", m.updateToken)
	g.DELETE("/stations/:id/tokens/:tokenId", m.deleteToken)
	g.GET("/stations/:id/history", m.stationHistory)
	g.POST("/stations/:id/refresh", m.refreshOne)
	g.POST("/refresh", m.refresh)
	g.GET("/overview", m.overview)
	m.registerAnalytics(g)
	m.registerNotifications(g)
}

func fail(c *gin.Context, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "配置不存在")
		return
	}
	if errors.Is(err, errStale) || errors.Is(err, errBusy) {
		response.Error(c, 409, err.Error())
		return
	}
	response.InternalError(c, "监控操作失败，请检查数据库或模块配置")
}

func idParam(c *gin.Context, key string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效 ID")
		return 0, false
	}
	return id, true
}

func (m *Module) getSettings(c *gin.Context) {
	s, err := m.settings(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	response.Success(c, s)
}

func validateSettings(s Settings) error {
	if s.RefreshSeconds < 10 || s.RefreshSeconds > 86400 {
		return errors.New("刷新间隔应为10至86400秒")
	}
	if s.RetentionDays < 30 || s.RetentionDays > 3650 {
		return errors.New("保留天数应为30至3650")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return errors.New("统计时区必须是有效 IANA 时区")
	}
	if _, err := time.Parse("15:04", s.DailyReportTime); err != nil {
		return errors.New("日报时间应为 HH:mm")
	}
	if s.ErrorThreshold < 1 || s.ErrorThreshold > 100 || s.ETAHours < 1 || s.ETAHours > 8760 || s.CooldownMinutes < 1 || s.CooldownMinutes > 525600 {
		return errors.New("告警配置超出范围")
	}
	if d, err := decimal.NewFromString(s.LowBalance); err != nil || d.IsNegative() {
		return errors.New("余额阈值不能为负数")
	}
	return nil
}

func (m *Module) putSettings(c *gin.Context) {
	s, err := m.settings(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	if err = c.ShouldBindJSON(&s); err != nil {
		response.BadRequest(c, "设置格式错误")
		return
	}
	if err = validateSettings(s); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	s.EncryptionKeyConfigured = false
	if err = m.writeDoc(c.Request.Context(), "settings", s); err != nil {
		fail(c, err)
		return
	}
	m.getSettings(c)
}

func (m *Module) listStations(c *gin.Context) {
	ss, err := m.stations(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	for i := range ss {
		items, e := m.latest(c.Request.Context(), ss[i].ID)
		if e != nil {
			fail(c, e)
			return
		}
		for _, item := range items {
			if ss[i].BalanceTokenID != nil && item.TokenID != *ss[i].BalanceTokenID {
				continue
			}
			copy := item
			ss[i].Latest = &copy
			break
		}
	}
	response.Success(c, gin.H{"stations": ss})
}

func (m *Module) validateStation(s *Station) error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || len(s.Name) > 200 {
		return errors.New("站点名称为必填，最多200字节")
	}
	valid := map[string]bool{"newapi": true, "newapi-key": true, "sub2api": true, "sub2api-password": true, "jucode": true, "monoize": true, "monoize-key": true, "fixed": true}
	if !valid[s.Type] {
		return errors.New("不支持的站点类型")
	}
	if s.Type != "fixed" {
		if strings.Contains(s.URL, "?") {
			return errors.New("站点地址请填写不含查询参数的基础 URL；凭证应单独保存")
		}
		if err := m.validateURL(s.URL); err != nil {
			return errors.New("站点地址不符合运营者 URL 策略")
		}
	}
	rate, err := validateRate(s.CNYPerUSD)
	if err != nil {
		return err
	}
	s.CNYPerUSD = rate
	if s.BalanceMode != "shared" && s.BalanceMode != "independent" && s.BalanceMode != "quota" {
		return errors.New("余额类型无效")
	}
	if !ratePattern.MatchString(s.LowBalance) {
		return errors.New("余额阈值格式错误")
	}
	if len(s.ChannelAliases) > 100 {
		return errors.New("渠道别名最多100个")
	}
	for _, alias := range s.ChannelAliases {
		if len(alias) > 200 {
			return errors.New("渠道别名过长")
		}
	}
	return nil
}

func (m *Module) createStation(c *gin.Context) {
	s := Station{Enabled: true, CNYPerUSD: "1", BalanceMode: "shared", LowBalance: "5", IncludeInCost: true, ChannelAliases: []string{}}
	if err := c.ShouldBindJSON(&s); err != nil {
		response.BadRequest(c, "站点格式错误")
		return
	}
	s.ID = 0
	s.Version = 1
	s.BalanceTokenID = nil
	if err := m.validateStation(&s); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	aliases, _ := json.Marshal(s.ChannelAliases)
	tx, err := m.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		fail(c, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(c.Request.Context(), `INSERT INTO relay_monitor_stations(name,url,type,enabled,cny_per_usd,balance_mode,low_balance,no_renewal,include_in_cost,subject_id,channel_aliases) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb) RETURNING id`, s.Name, s.URL, s.Type, s.Enabled, s.CNYPerUSD, s.BalanceMode, s.LowBalance, s.NoRenewal, s.IncludeInCost, s.SubjectID, string(aliases)).Scan(&s.ID)
	if err == nil {
		_, err = tx.ExecContext(c.Request.Context(), `INSERT INTO relay_monitor_rate_changes(station_id,new_rate) VALUES($1,$2)`, s.ID, s.CNYPerUSD)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(c, err)
		return
	}
	response.Success(c, s)
}

func (m *Module) updateStation(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	old, err := m.station(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	s := old
	if err = c.ShouldBindJSON(&s); err != nil {
		response.BadRequest(c, "站点格式错误")
		return
	}
	s.ID = id
	if err = m.validateStation(&s); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if s.Version != old.Version {
		fail(c, errStale)
		return
	}
	tx, err := m.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		fail(c, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if s.BalanceTokenID != nil {
		var exists bool
		err = tx.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM relay_monitor_tokens WHERE id=$1 AND station_id=$2 AND enabled=TRUE)`, *s.BalanceTokenID, id).Scan(&exists)
		if err != nil {
			fail(c, err)
			return
		}
		if !exists {
			response.BadRequest(c, "余额查询令牌必须属于该站点且已启用")
			return
		}
	}
	aliases, _ := json.Marshal(s.ChannelAliases)
	result, err := tx.ExecContext(c.Request.Context(), `UPDATE relay_monitor_stations SET name=$1,url=$2,type=$3,enabled=$4,cny_per_usd=$5,balance_mode=$6,balance_token_id=$7,low_balance=$8,no_renewal=$9,include_in_cost=$10,subject_id=$11,channel_aliases=$12::jsonb,version=version+1,updated_at=NOW() WHERE id=$13 AND version=$14`, s.Name, s.URL, s.Type, s.Enabled, s.CNYPerUSD, s.BalanceMode, s.BalanceTokenID, s.LowBalance, s.NoRenewal, s.IncludeInCost, s.SubjectID, string(aliases), id, old.Version)
	if err != nil {
		fail(c, err)
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		fail(c, err)
		return
	}
	if n != 1 {
		fail(c, errStale)
		return
	}
	if old.CNYPerUSD != s.CNYPerUSD {
		_, err = tx.ExecContext(c.Request.Context(), `INSERT INTO relay_monitor_rate_changes(station_id,old_rate,new_rate) VALUES($1,$2,$3)`, id, old.CNYPerUSD, s.CNYPerUSD)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(c, err)
		return
	}
	s.Version++
	response.Success(c, s)
}

func (m *Module) deleteStation(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	result, err := m.db.ExecContext(c.Request.Context(), `DELETE FROM relay_monitor_stations WHERE id=$1`, id)
	if err != nil {
		fail(c, err)
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		fail(c, err)
		return
	}
	if n != 1 {
		fail(c, sql.ErrNoRows)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (m *Module) listTokens(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	tokens, err := m.tokens(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	response.Success(c, gin.H{"tokens": tokens})
}

type tokenInput struct {
	Name              string            `json:"name"`
	AccountID         *int64            `json:"account_id"`
	Enabled           bool              `json:"enabled"`
	CNYPerUSDOverride *string           `json:"cny_per_usd_override"`
	Credential        map[string]string `json:"credential"`
	Version           int64             `json:"version"`
}

func (m *Module) validateToken(c *gin.Context, in *tokenInput, old *Token) (string, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 {
		return "", errors.New("令牌名称为必填，最多200字节")
	}
	if in.CNYPerUSDOverride != nil {
		rate, err := validateRate(*in.CNYPerUSDOverride)
		if err != nil {
			return "", err
		}
		in.CNYPerUSDOverride = &rate
	}
	if in.AccountID != nil {
		if *in.AccountID <= 0 {
			return "", errors.New("关联账号 ID 无效")
		}
		if len(in.Credential) > 0 {
			return "", errors.New("关联账号不能同时保存独立凭证")
		}
		if _, err := m.accounts.GetByID(c.Request.Context(), *in.AccountID); err != nil {
			return "", errors.New("关联账号不存在")
		}
		return "", nil
	}
	if len(in.Credential) == 0 {
		if old != nil && old.AccountID == nil {
			return old.EncryptedCredential, nil
		}
		return "", nil
	}
	if !m.cfg.Totp.EncryptionKeyConfigured {
		return "", errors.New("保存凭证前必须设置固定 TOTP_ENCRYPTION_KEY")
	}
	if len(in.Credential) > 32 {
		return "", errors.New("凭证字段过多")
	}
	for k, v := range in.Credential {
		if len(k) > 64 || len(v) > 32768 {
			return "", errors.New("凭证字段过长")
		}
	}
	existing := map[string]string{}
	if old != nil && old.AccountID == nil && old.EncryptedCredential != "" {
		plain, err := m.encryptor.Decrypt(old.EncryptedCredential)
		if err != nil || json.Unmarshal([]byte(plain), &existing) != nil {
			return "", errors.New("原凭证解密失败，未修改保存的凭证")
		}
	}
	merged := mergeSecretFields(existing, in.Credential)
	if len(merged) > 32 {
		return "", errors.New("凭证字段过多")
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return "", errors.New("凭证格式错误")
	}
	encrypted, err := m.encryptor.Encrypt(string(raw))
	if err != nil {
		return "", errors.New("凭证加密失败")
	}
	return encrypted, nil
}

func (m *Module) createToken(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	s, err := m.station(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	in := tokenInput{Enabled: true}
	if err = c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "令牌格式错误")
		return
	}
	cipher, err := m.validateToken(c, &in, nil)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	t := Token{StationID: id, Name: in.Name, AccountID: in.AccountID, Enabled: in.Enabled, CNYPerUSDOverride: in.CNYPerUSDOverride, HasCredential: cipher != "" || in.AccountID != nil, Version: 1}
	tx, err := m.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		fail(c, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(c.Request.Context(), `INSERT INTO relay_monitor_tokens(station_id,name,account_id,enabled,cny_per_usd_override,credential_encrypted) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, id, in.Name, in.AccountID, in.Enabled, in.CNYPerUSDOverride, cipher).Scan(&t.ID)
	if err == nil {
		_, err = tx.ExecContext(c.Request.Context(), `INSERT INTO relay_monitor_rate_changes(station_id,token_id,new_rate) VALUES($1,$2,$3)`, id, t.ID, in.CNYPerUSDOverride)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(c, err)
		return
	}
	applyRate(s, &t)
	response.Success(c, t)
}

func (m *Module) updateToken(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	tokenID, ok := idParam(c, "tokenId")
	if !ok {
		return
	}
	ss, err := m.station(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	tokens, err := m.tokens(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	var old *Token
	for i := range tokens {
		if tokens[i].ID == tokenID {
			old = &tokens[i]
			break
		}
	}
	if old == nil {
		fail(c, sql.ErrNoRows)
		return
	}
	in := tokenInput{Name: old.Name, AccountID: old.AccountID, Enabled: old.Enabled, CNYPerUSDOverride: old.CNYPerUSDOverride, Version: old.Version}
	if err = c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "令牌格式错误")
		return
	}
	if in.Version != old.Version {
		fail(c, errStale)
		return
	}
	cipher, err := m.validateToken(c, &in, old)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	tx, err := m.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		fail(c, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	preserveCipher := len(in.Credential) == 0 && in.AccountID == nil && old.AccountID == nil
	result, err := tx.ExecContext(c.Request.Context(), `UPDATE relay_monitor_tokens SET name=$1,account_id=$2,enabled=$3,cny_per_usd_override=$4,credential_encrypted=CASE WHEN $9 THEN credential_encrypted ELSE $5 END,version=version+1,updated_at=NOW() WHERE id=$6 AND station_id=$7 AND version=$8 AND ($9 OR credential_encrypted=$10)`, in.Name, in.AccountID, in.Enabled, in.CNYPerUSDOverride, cipher, tokenID, id, in.Version, preserveCipher, old.EncryptedCredential)
	if err != nil {
		fail(c, err)
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		fail(c, err)
		return
	}
	if n != 1 {
		fail(c, errStale)
		return
	}
	if !equalOptional(old.CNYPerUSDOverride, in.CNYPerUSDOverride) {
		_, err = tx.ExecContext(c.Request.Context(), `INSERT INTO relay_monitor_rate_changes(station_id,token_id,old_rate,new_rate) VALUES($1,$2,$3,$4)`, id, tokenID, old.CNYPerUSDOverride, in.CNYPerUSDOverride)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(c, err)
		return
	}
	t := Token{ID: tokenID, StationID: id, Name: in.Name, AccountID: in.AccountID, Enabled: in.Enabled, CNYPerUSDOverride: in.CNYPerUSDOverride, Version: in.Version + 1, HasCredential: cipher != "" || in.AccountID != nil}
	applyRate(ss, &t)
	response.Success(c, t)
}

func equalOptional(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (m *Module) deleteToken(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	tokenID, ok := idParam(c, "tokenId")
	if !ok {
		return
	}
	tx, err := m.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		fail(c, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(c.Request.Context(), `UPDATE relay_monitor_stations SET balance_token_id=NULL,version=version+1 WHERE id=$1 AND balance_token_id=$2`, id, tokenID)
	if err != nil {
		fail(c, err)
		return
	}
	r, err := tx.ExecContext(c.Request.Context(), `DELETE FROM relay_monitor_tokens WHERE id=$1 AND station_id=$2`, tokenID, id)
	if err != nil {
		fail(c, err)
		return
	}
	n, err := r.RowsAffected()
	if err != nil {
		fail(c, err)
		return
	}
	if n != 1 {
		fail(c, sql.ErrNoRows)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (m *Module) stationHistory(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	end := time.Now().UTC()
	start := end.Add(-48 * time.Hour)
	for key, target := range map[string]*time.Time{"start": &start, "end": &end} {
		if value := c.Query(key); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				response.BadRequest(c, "时间应为 RFC3339")
				return
			}
			*target = parsed
		}
	}
	if !end.After(start) || end.Sub(start) > 366*24*time.Hour {
		response.BadRequest(c, "历史窗口无效或超过366天")
		return
	}
	items, err := m.history(c.Request.Context(), id, start, end)
	if err != nil {
		fail(c, err)
		return
	}
	response.Success(c, gin.H{"history": items, "start": start, "end": end, "downsampled": true})
}

func (m *Module) refreshOne(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	s, err := m.station(c.Request.Context(), id)
	if err == nil {
		err = m.refreshStation(c.Request.Context(), s)
	}
	if err != nil {
		fail(c, err)
		return
	}
	response.Success(c, gin.H{"refreshed": true})
}
func (m *Module) refresh(c *gin.Context) {
	if err := m.refreshAll(c.Request.Context()); err != nil {
		fail(c, err)
		return
	}
	response.Success(c, gin.H{"refreshed": true})
}
