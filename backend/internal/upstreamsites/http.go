package upstreamsites

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// 管理路由的认证、审计和二次验证由宿主中间件负责；此处仅返回脱敏业务数据。

// ProviderSummary contains only fields required for reviewing site membership.
type ProviderSummary struct {
	ID          int64  `json:"id"`          // Existing host provider ID.
	Name        string `json:"name"`        // Existing administrator label.
	BaseURL     string `json:"base_url"`    // Existing forwarding root.
	Status      string `json:"status"`      // Read-only manual lifecycle.
	Schedulable bool   `json:"schedulable"` // Read-only scheduling switch.
}

// SaveRequest carries a complete config revision and optional credential replacements.
type SaveRequest struct {
	Config           Config   `json:"config"`            // Complete editable configuration.
	Version          int64    `json:"config_version"`    // Expected public revision.
	Secrets          *Secrets `json:"secrets,omitempty"` // Blank fields retain credentials.
	ClearKey         bool     `json:"clear_key"`         // Explicit wallet key deletion.
	ClearProxy       bool     `json:"clear_proxy"`       // Explicit monitor proxy deletion.
	ClearWalletToken bool     `json:"clear_wallet_token"`
	Archived         bool     `json:"archived"` // Disable site monitoring lifecycle.
}

// Handler 由宿主挂载到管理员路由下。
func (p *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sites", p.listHTTP)
	mux.HandleFunc("POST /sites", p.saveHTTP)
	mux.HandleFunc("PUT /sites/{id}", p.saveHTTP)
	mux.HandleFunc("PUT /sites/{id}/manual", p.manualHTTP)
	mux.HandleFunc("POST /sites/{id}/query", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		source := r.URL.Query().Get("source")
		if source == "" {
			source = "balance"
		}
		o, e := p.query(r.Context(), id, source)
		reply(w, o, e)
	})
	mux.HandleFunc("GET /sites/{id}/history", p.historyHTTP)
	mux.HandleFunc("GET /sites/{id}/daily", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var s StoredSite
		_, e := p.read(r.Context(), "sites", strconv.FormatInt(id, 10), &s)
		if e != nil {
			reply(w, nil, e)
			return
		}
		d, e := p.daily(r.Context(), s.Site)
		reply(w, d, e)
	})
	mux.HandleFunc("GET /sites/{id}/alerts", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		rows, e := p.records(r.Context(), "events", fmtSource(id, ""))
		out := []json.RawMessage{}
		for _, v := range rows {
			out = append(out, json.RawMessage(v.ValueJson))
		}
		reply(w, out, e)
	})
	mux.HandleFunc("GET /providers", func(w http.ResponseWriter, r *http.Request) { data, e := p.providers(r.Context()); reply(w, data, e) })
	mux.HandleFunc("GET /bindings", func(w http.ResponseWriter, r *http.Request) {
		var bindings map[string]int64
		version, e := p.read(r.Context(), "meta", "bindings", &bindings)
		if errors.Is(e, ErrNotFound) {
			bindings = map[string]int64{}
			e = nil
		}
		reply(w, map[string]any{"bindings": bindings, "version": version}, e)
	})
	mux.HandleFunc("PUT /bindings", p.bindHTTP)
	mux.HandleFunc("GET /control", p.controlHTTP)
	mux.HandleFunc("DELETE /sites/{id}", p.deleteHTTP)
	return p.httpTransactions(mux)
}

// reply emits stable diagnostics without reflecting secret-bearing upstream errors.
func reply(w http.ResponseWriter, data any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		status := 500
		message := "操作失败，请检查服务日志和配置"
		switch {
		case errors.Is(err, ErrInvalid):
			status = 400
			message = "配置无效，请检查阈值与探针设置"
		case errors.Is(err, ErrBound):
			status = 409
			message = "请先解除账号绑定"
		case errors.Is(err, ErrConflict):
			status = 409
			message = "数据已更新，请刷新重试"
		case errors.Is(err, ErrNotFound):
			status = 404
			message = "站点不存在"
		case errors.Is(err, ErrBusy):
			status = 409
			message = "查询进行中，请稍后重试"
		}
		if status >= 500 {
			slog.Error("上游站点操作失败", "error", err)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": status, "message": message})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
}

// listHTTP 返回站点配置和绑定数量，不返回凭据。
func (p *Manager) listHTTP(w http.ResponseWriter, r *http.Request) {
	values, err := p.sites(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	bindings := map[string]int64{}
	_, e := p.read(r.Context(), "meta", "bindings", &bindings)
	if e != nil && !errors.Is(e, ErrNotFound) {
		reply(w, nil, e)
		return
	}
	counts := map[int64]int{}
	for _, id := range bindings {
		counts[id]++
	}
	out := []Site{}
	for _, v := range values {
		v.Site.ProviderCount = counts[v.Site.ID]
		stats, e := p.daily(r.Context(), v.Site)
		if e != nil {
			reply(w, nil, e)
			return
		}
		v.Site.summarizeCosts(stats, time.Now())
		out = append(out, v.Site)
	}
	reply(w, map[string]any{"sites": out, "control_available": true, "version": "1"}, nil)
}

// saveHTTP applies optimistic edits while retaining omitted write-only secrets.
func (p *Manager) saveHTTP(w http.ResponseWriter, r *http.Request) {
	var req SaveRequest
	req.Config = DefaultConfig()
	req.Config.Timezone = "Asia/Shanghai"
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil {
		reply(w, nil, ErrInvalid)
		return
	}
	if req.Config.Validate() != nil {
		reply(w, nil, ErrInvalid)
		return
	}
	id, idErr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if r.Method == http.MethodPut && (idErr != nil || id < 1) {
		reply(w, nil, ErrInvalid)
		return
	}
	var stored StoredSite
	version := int64(-1)
	var secret Secrets
	if id > 0 {
		v, e := p.read(r.Context(), "sites", strconv.FormatInt(id, 10), &stored)
		if e != nil {
			reply(w, nil, e)
			return
		}
		version = v
		if req.Version != stored.Site.ConfigVersion {
			reply(w, nil, ErrConflict)
			return
		}
		raw, e := p.cipher.Decrypt(stored.Ciphertext)
		if e != nil {
			reply(w, nil, e)
			return
		}
		if json.Unmarshal([]byte(raw), &secret) != nil {
			reply(w, nil, ErrInvalid)
			return
		}
	} else {
		if e := p.kv.db.QueryRowContext(r.Context(), "SELECT nextval('upstream_site_ids')").Scan(&id); e != nil {
			reply(w, nil, e)
			return
		}
		stored.Site = Site{ID: id, WalletEpoch: 1, StatisticsEpoch: 1, NextDue: map[string]time.Time{}, State: State{Reasons: map[string]time.Time{}, Counters: map[string]Counter{}, Version: 1}}
	}
	walletChanged := version > 0 && (req.Config.BaseURL != stored.Site.Config.BaseURL || req.Config.Adapter != stored.Site.Config.Adapter)
	activeProbes := map[string]bool{}
	savedProbeKeys := secret.ProbeKeys
	secret.ProbeKeys = map[string]string{}
	for _, probe := range req.Config.Probes {
		activeProbes[probe.ID] = true
		for _, saved := range stored.Site.Config.Probes {
			if saved.ID == probe.ID && saved.URL == probe.URL && savedProbeKeys[probe.ID] != "" {
				secret.ProbeKeys[probe.ID] = savedProbeKeys[probe.ID]
			}
		}
	}
	if req.Secrets != nil {
		v := req.Secrets
		if v.Key != "" && v.Key != secret.Key {
			secret.Key = v.Key
			walletChanged = version > 0
		}
		if v.WalletToken != "" && v.WalletToken != secret.WalletToken {
			secret.WalletToken = v.WalletToken
			walletChanged = version > 0
		}
		if v.WalletUserID != "" && v.WalletUserID != secret.WalletUserID {
			secret.WalletUserID = v.WalletUserID
			walletChanged = version > 0
		}
		if v.ProxyURL != "" {
			u, e := url.Parse(v.ProxyURL)
			if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
				reply(w, nil, ErrInvalid)
				return
			}
			secret.ProxyURL = v.ProxyURL
		}
		if len(v.ProbeKeys) > 0 {
			for k, x := range v.ProbeKeys {
				if !activeProbes[k] {
					reply(w, nil, ErrInvalid)
					return
				}
				if x != "" {
					secret.ProbeKeys[k] = x
				}
			}
		}
	}
	if req.ClearKey {
		secret.Key = ""
		walletChanged = version > 0
	}
	if req.ClearWalletToken {
		secret.WalletToken = ""
		secret.WalletUserID = ""
		walletChanged = version > 0
	}
	if req.ClearProxy {
		secret.ProxyURL = ""
	}
	if req.Config.Adapter == "new_api" {
		if _, err := newAPIUserID(secret.WalletUserID); err != nil {
			reply(w, nil, ErrInvalid)
			return
		}
	}
	if walletChanged {
		stored.Site.WalletEpoch++
		stored.Site.Latest = nil
		stored.Site.LastObservation = nil
	}
	if version > 0 && req.Config.Timezone != stored.Site.Config.Timezone {
		stored.Site.StatisticsEpoch++
	}
	if req.Archived {
		var bindings map[string]int64
		_, e := p.read(r.Context(), "meta", "bindings", &bindings)
		if e != nil && !errors.Is(e, ErrNotFound) {
			reply(w, nil, e)
			return
		}
		for _, sid := range bindings {
			if sid == id {
				reply(w, nil, ErrInvalid)
				return
			}
		}
	}
	raw, e := json.Marshal(secret)
	if e != nil {
		reply(w, nil, e)
		return
	}
	stored.Ciphertext, e = p.cipher.Encrypt(string(raw))
	if e != nil {
		reply(w, nil, e)
		return
	}
	stored.Site.State = Reconfigure(req.Config, stored.Site.State)
	retentionChanged := version > 0 && stored.Site.Config.SummaryRetentionDays != req.Config.SummaryRetentionDays
	stored.Site.Config = req.Config
	stored.Site.ConfigVersion++
	stored.Site.HasKey = secret.Key != "" || secret.WalletToken != ""
	stored.Site.HasProxy = secret.ProxyURL != ""
	stored.Site.Archived = req.Archived
	_, e = p.kv.Put(r.Context(), "sites", strconv.FormatInt(id, 10), stored, IfVersion(version))
	if e == nil {
		e = p.reconcile(r.Context())
	}
	if e == nil && retentionChanged {
		e = p.retainDaily(r.Context(), stored.Site)
	}
	reply(w, stored.Site, e)
}

// historyHTTP provides bounded pages while retaining exact imported observations.
func (p *Manager) historyHTTP(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	all, e := p.history(r.Context(), id)
	if e != nil {
		reply(w, nil, e)
		return
	}
	items := []Observation{}
	source := r.URL.Query().Get("source")
	for i := len(all) - 1; i >= 0; i-- {
		if source == "" || all[i].Source == source {
			items = append(items, all[i])
		}
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + 100
	if end > len(items) {
		end = len(items)
	}
	reply(w, map[string]any{"items": items[offset:end], "total": len(items)}, nil)
}

// bindHTTP atomically replaces a reviewed mapping without touching host providers.
func (p *Manager) bindHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Bindings map[string]int64 `json:"bindings"`
		Version  int64            `json:"version"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || req.Bindings == nil {
		reply(w, nil, ErrInvalid)
		return
	}
	providers, e := p.providers(r.Context())
	if e != nil {
		reply(w, nil, e)
		return
	}
	allowed := map[string]bool{}
	for _, v := range providers {
		allowed[strconv.FormatInt(v.ID, 10)] = true
	}
	sites, e := p.sites(r.Context())
	if e != nil {
		reply(w, nil, e)
		return
	}
	valid := map[int64]bool{}
	for _, s := range sites {
		valid[s.Site.ID] = !s.Site.Archived
	}
	for id, site := range req.Bindings {
		if !allowed[id] || !valid[site] {
			reply(w, nil, ErrInvalid)
			return
		}
	}
	version := req.Version
	if version == 0 {
		version = -1
	}
	v, e := p.kv.Put(r.Context(), "meta", "bindings", req.Bindings, IfVersion(version))
	if e == nil {
		e = p.reconcile(r.Context())
	}
	reply(w, map[string]any{"version": v}, e)
}
