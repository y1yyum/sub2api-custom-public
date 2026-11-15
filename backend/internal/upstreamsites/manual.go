package upstreamsites

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func (p *Manager) manualHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
		Version int64 `json:"state_version"`
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err != nil || id < 1 || decoder.Decode(&req) != nil || req.Enabled == nil {
		reply(w, nil, ErrInvalid)
		return
	}
	key := strconv.FormatInt(id, 10)
	var stored StoredSite
	version, err := p.read(r.Context(), "sites", key, &stored)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if stored.Site.State.Version != req.Version {
		reply(w, nil, ErrConflict)
		return
	}
	if stored.Site.Archived {
		reply(w, nil, ErrInvalid)
		return
	}
	if stored.Site.State.Manual == !*req.Enabled {
		reply(w, stored.Site.State, nil)
		return
	}
	stored.Site.State.Manual = !*req.Enabled
	stored.Site.State.Version++
	if _, err = p.kv.Put(r.Context(), "sites", key, stored, IfVersion(version)); err != nil {
		reply(w, nil, err)
		return
	}
	if err = p.reconcile(r.Context()); err != nil {
		reply(w, nil, err)
		return
	}
	kind := "manual_disable"
	if *req.Enabled {
		kind = "manual_enable"
	}
	now := time.Now().UTC()
	event := map[string]any{"site_id": id, "site_name": stored.Site.Config.Name, "kind": kind, "at": now}
	_, err = p.kv.Put(r.Context(), "events", fmtSource(id, now.Format(time.RFC3339Nano)+":"+kind), event, TTL(time.Duration(stored.Site.Config.SummaryRetentionDays)*24*time.Hour))
	reply(w, stored.Site.State, err)
}
