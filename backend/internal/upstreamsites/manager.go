package upstreamsites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
	_ "time/tzdata"
)

// 管理器复用宿主数据库和加密服务，公开响应不包含凭据密文。

// StoredSite wraps public site metadata and its separately encrypted credentials.
type StoredSite struct {
	Site       Site   `json:"site"`       // Public site configuration.
	Ciphertext string `json:"ciphertext"` // Private authenticated encrypted JSON.
}

// read decodes one namespaced record and returns its optimistic storage version.
func (p *Manager) read(ctx context.Context, c, k string, out any) (int64, error) {
	r, err := p.kv.Get(ctx, c, k)
	if err != nil {
		return 0, err
	}
	if !r.Found {
		return 0, ErrNotFound
	}
	return r.Version, json.Unmarshal(r.ValueJson, out)
}

// records follows host pagination without interpreting private record contents.
func (p *Manager) records(ctx context.Context, c, prefix string) ([]*Record, error) {
	var out []*Record
	cursor := ""
	for {
		r, err := p.kv.List(ctx, &ListRequest{Collection: c, Prefix: prefix, Cursor: cursor, Limit: 500})
		if err != nil {
			return nil, err
		}
		out = append(out, r.Items...)
		if r.NextCursor == "" {
			return out, nil
		}
		cursor = r.NextCursor
	}
}

// sites lists encrypted aggregates for internal consumers; HTTP projects public fields.
func (p *Manager) sites(ctx context.Context) ([]StoredSite, error) {
	rows, err := p.records(ctx, "sites", "")
	if err != nil {
		return nil, err
	}
	out := []StoredSite{}
	for _, r := range rows {
		var s StoredSite
		if err = json.Unmarshal(r.ValueJson, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Site.ID < out[j].Site.ID })
	return out, nil
}

// fmtSource constructs stable numeric prefixes shared by history and statistics.
func fmtSource(id int64, s string) string { return fmt.Sprintf("%020d:%s", id, s) }

// history returns all retained observations in time order for deterministic rollups.
func (p *Manager) history(ctx context.Context, id int64) ([]Observation, error) {
	rows, err := p.records(ctx, "history", fmtSource(id, ""))
	if err != nil {
		return nil, err
	}
	out := []Observation{}
	for _, r := range rows {
		var o Observation
		if err = json.Unmarshal(r.ValueJson, &o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// 每个已接受区间仅累计一次；原始记录清理或保留期修改不重算历史成本。
// 调用方在同一事务中校验版本、写入观测并更新最后成功余额。
func (p *Manager) appendDaily(ctx context.Context, s Site, previous *Observation, next Observation) error {
	if next.Source != "balance" || next.Outcome != Healthy {
		return nil
	}
	samples := []Observation{}
	if previous != nil && previous.WalletEpoch == next.WalletEpoch && previous.StatisticsEpoch == next.StatisticsEpoch {
		samples = append(samples, *previous)
	}
	samples = append(samples, next)
	loc, _ := time.LoadLocation(next.Timezone)
	date := next.At.In(loc).Format(time.DateOnly)
	days := map[string][]DailyStat{}
	for _, d := range DailyStats(samples) {
		if d.Date != date && d.UpstreamCost == nil {
			continue
		}
		days[d.Date] = append(days[d.Date], d)
	}
	for day, deltas := range days {
		key := fmtSource(s.ID, day)
		values := []DailyStat{}
		if _, err := p.read(ctx, "daily", key, &values); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		for _, delta := range deltas {
			found := false
			for i := range values {
				v := &values[i]
				if v.WalletEpoch == delta.WalletEpoch && v.StatisticsEpoch == delta.StatisticsEpoch && v.Unit == delta.Unit {
					mergeDailyStat(v, delta)
					found = true
					break
				}
			}
			if !found {
				values = append(values, delta)
			}
		}
		if err := p.writeDaily(ctx, s, key, values); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) writeDaily(ctx context.Context, s Site, key string, values []DailyStat) error {
	now := time.Now()
	retained := values[:0]
	var expiry time.Time
	for _, value := range values {
		at, err := summaryExpiresAt(value, s.Config.SummaryRetentionDays)
		if err != nil {
			return err
		}
		if at.After(now) {
			retained = append(retained, value)
			if at.After(expiry) {
				expiry = at
			}
		}
	}
	if len(retained) == 0 {
		_, err := p.kv.db.ExecContext(ctx, `DELETE FROM upstream_site_records WHERE collection='daily' AND key=$1`, key)
		return err
	}
	_, err := p.kv.Put(ctx, "daily", key, retained, ExpiresAt(expiry))
	return err
}

func (p *Manager) retainDaily(ctx context.Context, s Site) error {
	rows, err := p.records(ctx, "daily", fmtSource(s.ID, ""))
	if err != nil {
		return err
	}
	for _, row := range rows {
		var values []DailyStat
		if err = json.Unmarshal(row.ValueJson, &values); err != nil {
			return err
		}
		if err = p.writeDaily(ctx, s, row.Key, values); err != nil {
			return err
		}
	}
	return nil
}

// 同进程同站点不重叠查询；多副本通过存储版本拒绝迟到结果。
// 观测、统计、限制和调度事件在完成阶段的同一事务中提交。
func (p *Manager) query(ctx context.Context, id int64, source string) (Observation, error) {
	if _, busy := p.busy.LoadOrStore(id, true); busy {
		return Observation{}, ErrBusy
	}
	defer p.busy.Delete(id)
	var stored StoredSite
	expectedVersion, err := p.read(ctx, "sites", strconv.FormatInt(id, 10), &stored)
	if err != nil {
		return Observation{}, err
	}
	s := stored.Site
	if s.Archived {
		return Observation{}, ErrInvalid
	}
	raw, err := p.cipher.Decrypt(stored.Ciphertext)
	if err != nil {
		return Observation{}, errors.New("credential decryption failed")
	}
	var secret Secrets
	if err = json.Unmarshal([]byte(raw), &secret); err != nil {
		return Observation{}, err
	}
	o := p.observe(ctx, s.Config, secret, source)
	o.ConfigVersion = s.ConfigVersion
	o.WalletEpoch = s.WalletEpoch
	o.StatisticsEpoch = s.StatisticsEpoch
	o.Timezone = s.Config.Timezone
	o.IntervalMinutes = s.Config.IntervalMinutes
	return p.complete(ctx, s, o, expectedVersion)
}

func (p *Manager) complete(parent context.Context, s Site, o Observation, expectedVersion int64) (Observation, error) {
	id, source := s.ID, o.Source
	err := p.transaction(parent, func(p *Manager, ctx context.Context) error {
		var current StoredSite
		version, err := p.read(ctx, "sites", strconv.FormatInt(id, 10), &current)
		if err != nil {
			return err
		}
		if version != expectedVersion || current.Site.ConfigVersion != s.ConfigVersion || current.Site.Archived {
			return ErrConflict
		}
		_, err = p.kv.Put(ctx, "history", fmtSource(id, o.At.UTC().Format(time.RFC3339Nano)+":"+o.RunID), o, IfAbsent(), TTL(time.Duration(s.Config.RawRetentionDays)*24*time.Hour))
		if err != nil {
			return err
		}
		current.Site.LastObservation = &o
		current.Site.State = Evaluate(current.Site.Config, current.Site.State, o)
		if err = p.appendDaily(ctx, current.Site, current.Site.Latest, o); err != nil {
			return err
		}
		if source == "balance" && o.Outcome == Healthy {
			current.Site.Latest = &o
		}
		if current.Site.NextDue == nil {
			current.Site.NextDue = map[string]time.Time{}
		}
		interval := s.Config.IntervalMinutes
		for _, probe := range s.Config.Probes {
			if probe.ID == source {
				interval = probe.IntervalMinutes
			}
		}
		current.Site.NextDue[source] = time.Now().Add(time.Duration(interval) * time.Minute)
		_, err = p.kv.Put(ctx, "sites", strconv.FormatInt(id, 10), current, IfVersion(version))
		if err != nil {
			return err
		}
		if err = p.reconcile(ctx); err != nil {
			return err
		}
		stats, err := p.daily(ctx, current.Site)
		if err != nil {
			return err
		}
		return p.alert(ctx, current.Site, o, stats)
	})
	return o, err
}

// RunTask retries persisted restrictions and checks each monitoring source's deadline.
func (p *Manager) RunTask(ctx context.Context, name string, at time.Time) error {
	if name != "monitor" {
		return ErrInvalid
	}
	if err := p.transaction(ctx, func(p *Manager, ctx context.Context) error { return p.reconcile(ctx) }); err != nil {
		return err
	}
	sites, err := p.sites(ctx)
	if err != nil {
		return err
	}
	jobs := make(chan StoredSite)
	var wg sync.WaitGroup
	var errsMu sync.Mutex
	var errs []error
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				s := item.Site
				if s.Archived {
					continue
				}
				sources := []string{}
				if s.Config.AutoQuery && !s.NextDue["balance"].After(at) {
					sources = append(sources, "balance")
				}
				for _, probe := range s.Config.Probes {
					if !s.NextDue[probe.ID].After(at) {
						sources = append(sources, probe.ID)
					}
				}
				for _, source := range sources {
					if _, e := p.query(ctx, s.ID, source); e != nil && !errors.Is(e, ErrBusy) {
						errsMu.Lock()
						errs = append(errs, fmt.Errorf("site %d: %w", s.ID, e))
						errsMu.Unlock()
					}
				}
			}
		}()
	}
	for _, s := range sites {
		select {
		case jobs <- s:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	return errors.Join(errs...)
}
