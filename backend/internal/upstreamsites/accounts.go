package upstreamsites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const AccountRestrictionKey = "upstream_site_restriction"

func (p *Manager) providers(ctx context.Context) ([]ProviderSummary, error) {
	rows, err := p.kv.db.QueryContext(ctx, `SELECT id,name,COALESCE(credentials->>'base_url',''),status,schedulable FROM accounts WHERE deleted_at IS NULL AND type='apikey' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []ProviderSummary{}
	for rows.Next() {
		var a ProviderSummary
		if err = rows.Scan(&a.ID, &a.Name, &a.BaseURL, &a.Status, &a.Schedulable); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (p *Manager) reconcile(ctx context.Context) error {
	bindings := map[string]int64{}
	if _, err := p.read(ctx, "meta", "bindings", &bindings); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	sites, err := p.sites(ctx)
	if err != nil {
		return err
	}
	byID := map[int64]Site{}
	for _, s := range sites {
		byID[s.Site.ID] = s.Site
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		return err
	}
	rows, err := p.kv.db.QueryContext(ctx, `SELECT id,schedulable,extra->'upstream_site_restriction',type FROM accounts
 WHERE deleted_at IS NULL AND (extra ? 'upstream_site_restriction' OR $1::jsonb ? id::text) ORDER BY id FOR UPDATE`, string(raw))
	if err != nil {
		return err
	}
	type row struct {
		id          int64
		manual      bool
		restriction []byte
		accountType string
	}
	accounts := []row{}
	for rows.Next() {
		var a row
		if err = rows.Scan(&a.id, &a.manual, &a.restriction, &a.accountType); err != nil {
			_ = rows.Close()
			return err
		}
		accounts = append(accounts, a)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, a := range accounts {
		s, exists := byID[bindings[strconv.FormatInt(a.id, 10)]]
		blocked := exists && !s.Archived && s.State.Blocked() && a.accountType == "apikey"
		var previous struct {
			SiteID int64 `json:"site_id"`
		}
		if len(a.restriction) > 0 {
			if err = json.Unmarshal(a.restriction, &previous); err != nil {
				return err
			}
		}
		if blocked && previous.SiteID == s.ID {
			continue
		}
		if !blocked && previous.SiteID == 0 {
			continue
		}
		kind := "release"
		if blocked {
			kind = "suspend"
			_, err = p.kv.db.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{upstream_site_restriction}',jsonb_build_object('site_id',$2::bigint)),updated_at=NOW() WHERE id=$1`, a.id, s.ID)
		} else {
			_, err = p.kv.db.ExecContext(ctx, `UPDATE accounts SET extra=extra-'upstream_site_restriction',updated_at=NOW() WHERE id=$1`, a.id)
			s = byID[previous.SiteID]
		}
		if err != nil {
			return err
		}
		if _, err = p.kv.db.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id,created_at) VALUES ('account_changed',$1,NOW())`, a.id); err != nil {
			return err
		}
		now := time.Now().UTC()
		recipient := ""
		if s.Config.Alerts.Control {
			recipient = s.Config.Alerts.Email
		}
		event := map[string]any{"site_id": s.ID, "site_name": s.Config.Name, "kind": kind, "at": now, "provider_id": a.id, "manual_schedulable": a.manual, "email": recipient, "delivery": deliveryFor(recipient)}
		if _, err = p.kv.Put(ctx, "events", fmtSource(s.ID, fmt.Sprintf("%s:%d:%s", now.Format(time.RFC3339Nano), a.id, kind)), event, TTL(time.Duration(s.Config.SummaryRetentionDays)*24*time.Hour)); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) controlHTTP(w http.ResponseWriter, r *http.Request) {
	rows, err := p.kv.db.QueryContext(r.Context(), `SELECT id,schedulable,(extra->'upstream_site_restriction'->>'site_id')::bigint FROM accounts WHERE deleted_at IS NULL AND extra ? 'upstream_site_restriction' ORDER BY id`)
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer func() { _ = rows.Close() }()
	holds := []map[string]any{}
	for rows.Next() {
		var id, site int64
		var manual bool
		if err = rows.Scan(&id, &manual, &site); err != nil {
			reply(w, nil, err)
			return
		}
		holds = append(holds, map[string]any{"provider_id": id, "site_id": site, "manual_schedulable": manual})
	}
	reply(w, map[string]any{"available": true, "status": map[string]any{"holds": holds}}, rows.Err())
}

func (p *Manager) deleteHTTP(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		reply(w, nil, ErrInvalid)
		return
	}
	var stored StoredSite
	version, err := p.read(r.Context(), "sites", strconv.FormatInt(id, 10), &stored)
	if err != nil {
		reply(w, nil, err)
		return
	}
	expected, err := strconv.ParseInt(r.URL.Query().Get("config_version"), 10, 64)
	if err != nil || expected != stored.Site.ConfigVersion {
		reply(w, nil, ErrConflict)
		return
	}
	var bindings map[string]int64
	if _, err = p.read(r.Context(), "meta", "bindings", &bindings); err != nil && !errors.Is(err, ErrNotFound) {
		reply(w, nil, err)
		return
	}
	for _, site := range bindings {
		if site == id {
			reply(w, nil, ErrBound)
			return
		}
	}
	removed, err := p.kv.Delete(r.Context(), "sites", strconv.FormatInt(id, 10), version)
	if err == nil && !removed {
		err = ErrConflict
	}
	if err == nil {
		_, err = p.kv.db.ExecContext(r.Context(), `DELETE FROM upstream_site_records WHERE collection IN ('history','daily','events','alert-state') AND starts_with(key,$1)`, fmtSource(id, ""))
	}
	reply(w, map[string]any{"deleted": removed}, err)
}
