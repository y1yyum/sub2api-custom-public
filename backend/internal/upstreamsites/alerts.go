package upstreamsites

import (
	"context"
	"fmt"
	"time"
)

// alerts.go persists threshold warnings without changing provider eligibility.
// Low-balance and failure episodes rearm after healthy evidence; daily warnings
// deduplicate by local date. Email delivery uses one stable host idempotency key.

// AlertState records active warning identities and consecutive source failures.
type AlertState struct {
	Active   map[string]bool `json:"active"`   // Currently active warning episodes.
	Failures int             `json:"failures"` // Consecutive unsuccessful wallet rounds.
}

// alert evaluates notification thresholds after a durable observation is recorded.
func (p *Manager) alert(ctx context.Context, s Site, o Observation, stats []DailyStat) error {
	if o.Source != "balance" {
		return nil
	}
	policy := s.Config.Alerts
	state := AlertState{Active: map[string]bool{}}
	_, e := p.read(ctx, "alert-state", fmtSource(s.ID, "current"), &state)
	if e != nil && e != ErrNotFound {
		return e
	}
	if state.Active == nil {
		state.Active = map[string]bool{}
	}
	if o.Outcome == Healthy {
		state.Failures = 0
	} else {
		state.Failures++
	}
	values := map[string]string{}
	thresholds := map[string]string{}
	if policy.Failures > 0 && state.Failures >= policy.Failures {
		values["failures"] = fmt.Sprint(state.Failures)
		thresholds["failures"] = fmt.Sprint(policy.Failures)
	}
	if o.Outcome == Healthy {
		for _, b := range o.Balances {
			if b.Unit == policy.Unit && policy.LowBalance != "" {
				v, e1 := Amount(b.Available)
				limit, e2 := Amount(policy.LowBalance)
				if e1 == nil && e2 == nil && v.LessThan(limit) {
					values["low_balance"] = b.Available
					thresholds["low_balance"] = policy.LowBalance
				}
			}
		}
	}
	location, _ := time.LoadLocation(s.Config.Timezone)
	if location == nil {
		location = time.UTC
	}
	today := o.At.In(location).Format(time.DateOnly)
	for _, d := range stats {
		if d.Date != today || d.Unit != policy.Unit || d.WalletEpoch != s.WalletEpoch || d.StatisticsEpoch != s.StatisticsEpoch || policy.DailySpend == "" {
			continue
		}
		amount := d.EstimatedSpend
		if policy.DailyMetric == "account" {
			amount = d.AccountSpend
		}
		if amount != nil {
			v, e1 := Amount(*amount)
			limit, e2 := Amount(policy.DailySpend)
			if e1 == nil && e2 == nil && v.GreaterThanOrEqual(limit) {
				name := "daily_" + today
				values[name] = *amount
				thresholds[name] = policy.DailySpend
			}
		}
	}
	for name, active := range state.Active {
		if !active {
			continue
		}
		if _, found := values[name]; !found && (name != "low_balance" || o.Outcome == Healthy) {
			delete(state.Active, name)
		}
	}
	for name, value := range values {
		if state.Active[name] {
			continue
		}
		key := fmtSource(s.ID, o.RunID+":"+name)
		event := map[string]any{"site_id": s.ID, "kind": name, "at": o.At, "value": value, "unit": policy.Unit, "threshold": thresholds[name], "delivery": deliveryFor(policy.Email), "email": policy.Email, "site_name": s.Config.Name}
		_, e = p.kv.Put(ctx, "events", key, event, TTL(time.Duration(s.Config.SummaryRetentionDays)*24*time.Hour))
		if e != nil {
			return e
		}
		state.Active[name] = true
	}
	_, e = p.kv.Put(ctx, "alert-state", fmtSource(s.ID, "current"), state)
	return e
}
