package upstreamsites

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// statistics.go derives daily wallet observations without inventing exact bills.
// Only comparable successful samples contribute differences. A recharge contributes
// no estimated consumption, while a long gap, changed wallet, changed calendar,
// or missing currency breaks continuity. Total funds take precedence over available
// funds so an upstream freeze is not counted as spending when totals are known.
//
// Intervals crossing a calendar boundary are allocated by elapsed seconds and
// explicitly marked estimated. Calendar boundaries use the sample's IANA timezone,
// including daylight-saving changes. Account cumulative spending remains a separate
// metric and is never inferred from key usage or from a wallet's balance decline.

// DailyStat describes one wallet, calendar, day, and currency observation segment.
type DailyStat struct {
	AlgorithmVersion int     `json:"algorithm_version"` // immutable aggregation algorithm identifier
	Date             string  `json:"date"`              // local calendar date
	Timezone         string  `json:"timezone"`          // immutable segment timezone
	Unit             string  `json:"unit"`              // wallet currency denomination
	WalletEpoch      int64   `json:"wallet_epoch"`      // account identity segment
	StatisticsEpoch  int64   `json:"statistics_epoch"`  // timezone identity segment
	EstimatedSpend   *string `json:"estimated_spend"`   // null until comparable observations
	UpstreamCost     *string `json:"upstream_cost"`
	AccountSpend     *string `json:"account_spend"`   // null without trustworthy account counter
	NetChange        *string `json:"net_change"`      // observed net wallet change
	CoveredSeconds   int64   `json:"covered_seconds"` // accepted observation interval coverage
	Gap              bool    `json:"gap"`             // excluded long observation interval
	CrossDay         bool    `json:"cross_day"`       // proportional calendar boundary allocation
	AvailableOnly    bool    `json:"available_only"`  // frozen funds can affect estimate
}

// DailyStats aggregates comparable intervals from an immutable observation history.
// The caller supplies successful and failed rounds in any order; only validated
// wallet observations become samples. The result keeps wallet and calendar epochs
// separate and leaves insufficiently observed days unknown. Cross-midnight intervals
// use actual elapsed seconds, while gaps over twice the earlier sample's cadence
// contribute only net change and can never trigger estimated daily spending.
func DailyStats(history []Observation) []DailyStat {
	samples := make([]Observation, 0, len(history))
	for _, o := range history {
		if o.Source == "balance" && o.Outcome == Healthy {
			samples = append(samples, o)
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })
	stats := map[string]*DailyStat{}
	for i, current := range samples {
		location, err := time.LoadLocation(current.Timezone)
		if err != nil {
			continue
		}
		for _, balance := range current.Balances {
			entry := dailyEntry(stats, current, balance, current.At.In(location))
			if i == 0 {
				continue
			}
			previous := samples[i-1]
			if previous.WalletEpoch != current.WalletEpoch || previous.StatisticsEpoch != current.StatisticsEpoch || previous.Timezone != current.Timezone || !current.At.After(previous.At) {
				continue
			}
			var prior *Balance
			for j := range previous.Balances {
				if previous.Balances[j].Unit == balance.Unit {
					prior = &previous.Balances[j]
					break
				}
			}
			if prior == nil || (prior.Total == nil) != (balance.Total == nil) {
				continue
			}
			oldAmount, newAmount := prior.Available, balance.Available
			if prior.Total != nil {
				oldAmount = *prior.Total
				newAmount = *balance.Total
			}
			oldValue, e1 := Amount(oldAmount)
			newValue, e2 := Amount(newAmount)
			if e1 != nil || e2 != nil {
				continue
			}
			net := newValue.Sub(oldValue)
			spend := decimal.Max(decimal.Zero, net.Neg())
			elapsed := current.At.Sub(previous.At)
			if elapsed > time.Duration(2*previous.IntervalMinutes)*time.Minute {
				entry.Gap = true
				addDecimal(&entry.NetChange, net)
				// 缺测区间的成本归入本次查询日，不推测期间每日消费。
				addDecimal(&entry.UpstreamCost, spend)
				continue
			}
			var used *decimal.Decimal
			if prior.Used != nil && balance.Used != nil {
				a, e1 := Amount(*prior.Used)
				b, e2 := Amount(*balance.Used)
				if e1 == nil && e2 == nil && !b.LessThan(a) {
					delta := b.Sub(a)
					used = &delta
				}
			}
			start := previous.At.In(location)
			end := current.At.In(location)
			cross := start.Format(time.DateOnly) != end.Format(time.DateOnly)
			remainingCost := spend
			for start.Before(end) {
				nextDay := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, location)
				stop := end
				if nextDay.Before(stop) {
					stop = nextDay
				}
				part := dailyEntry(stats, current, balance, start)
				seconds := stop.Sub(start).Seconds()
				// Allocation describes observed elapsed time, not the unknown instant
				// upstream spending happened; every split day retains this quality marker.
				fraction := decimal.NewFromFloat(seconds).Div(decimal.NewFromFloat(elapsed.Seconds()))
				addDecimal(&part.EstimatedSpend, spend.Mul(fraction).Round(10))
				cost := spend.Mul(fraction).Round(10)
				if stop.Equal(end) {
					cost = remainingCost
				}
				addDecimal(&part.UpstreamCost, cost)
				remainingCost = remainingCost.Sub(cost)
				addDecimal(&part.NetChange, net.Mul(fraction).Round(10))
				if used != nil {
					addDecimal(&part.AccountSpend, used.Mul(fraction).Round(10))
				}
				part.CoveredSeconds += int64(seconds)
				part.CrossDay = part.CrossDay || cross
				start = stop
			}
		}
	}
	result := make([]DailyStat, 0, len(stats))
	for _, value := range stats {
		result = append(result, *value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Date == result[j].Date {
			return result[i].Unit < result[j].Unit
		}
		return result[i].Date > result[j].Date
	})
	return result
}

// dailyEntry creates an unknown day until a valid interval supplies spending evidence.
func dailyEntry(stats map[string]*DailyStat, o Observation, b Balance, at time.Time) *DailyStat {
	date := at.Format(time.DateOnly)
	key := fmtSource(o.WalletEpoch, fmtSource(o.StatisticsEpoch, date+":"+b.Unit))
	if stats[key] == nil {
		stats[key] = &DailyStat{AlgorithmVersion: 2, Date: date, Timezone: o.Timezone, Unit: b.Unit, WalletEpoch: o.WalletEpoch, StatisticsEpoch: o.StatisticsEpoch, AvailableOnly: b.Total == nil}
	}
	return stats[key]
}

// addDecimal accumulates exact decimal values while retaining unknown before first evidence.
func addDecimal(target **string, value decimal.Decimal) {
	if *target != nil {
		old, _ := decimal.NewFromString(**target)
		value = value.Add(old)
	}
	text := value.String()
	*target = &text
}

type CostSummary struct {
	Unit   string  `json:"unit"`
	Amount *string `json:"amount"`
	Gap    bool    `json:"gap"`
}

func summaryExpiresAt(day DailyStat, days int) (time.Time, error) {
	location, err := time.LoadLocation(day.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	date, err := time.ParseInLocation(time.DateOnly, day.Date, location)
	if err != nil {
		return time.Time{}, err
	}
	return date.AddDate(0, 0, days), nil
}

func mergeDailyStat(total *DailyStat, delta DailyStat) {
	for _, field := range []struct {
		target **string
		value  *string
	}{
		{&total.UpstreamCost, delta.UpstreamCost},
		{&total.EstimatedSpend, delta.EstimatedSpend},
		{&total.AccountSpend, delta.AccountSpend},
		{&total.NetChange, delta.NetChange},
	} {
		if field.value != nil {
			addDecimal(field.target, decimal.RequireFromString(*field.value))
		}
	}
	total.CoveredSeconds += delta.CoveredSeconds
	total.Gap = total.Gap || delta.Gap
	total.CrossDay = total.CrossDay || delta.CrossDay
	total.AvailableOnly = total.AvailableOnly || delta.AvailableOnly
}

// 列表仅汇总当前钱包及统计时区；历史分段仍在每日记录中独立保留。
func (s *Site) summarizeCosts(stats []DailyStat, now time.Time) {
	s.Today = []DailyStat{}
	s.Costs = []CostSummary{}
	location, _ := time.LoadLocation(s.Config.Timezone)
	today := now.In(location).Format(time.DateOnly)
	byUnit := map[string]*CostSummary{}
	for _, day := range stats {
		if day.WalletEpoch != s.WalletEpoch || day.StatisticsEpoch != s.StatisticsEpoch {
			continue
		}
		if day.Date == today {
			s.Today = append(s.Today, day)
		}
		cost := byUnit[day.Unit]
		if cost == nil {
			cost = &CostSummary{Unit: day.Unit}
			byUnit[day.Unit] = cost
		}
		if day.UpstreamCost != nil {
			amount, _ := decimal.NewFromString(*day.UpstreamCost)
			addDecimal(&cost.Amount, amount)
		}
		cost.Gap = cost.Gap || day.Gap
	}
	for _, cost := range byUnit {
		s.Costs = append(s.Costs, *cost)
	}
	sort.Slice(s.Costs, func(i, j int) bool { return s.Costs[i].Unit < s.Costs[j].Unit })
}
