package relaymonitor

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func currentBalance(s Station, tokens []Token, latest []Snapshot) (*string, *string, int, bool) {
	byID := map[int64]Token{}
	var selected int64
	for _, t := range tokens {
		if t.Enabled {
			byID[t.ID] = t
			if selected == 0 {
				selected = t.ID
			}
		}
	}
	if s.BalanceTokenID != nil {
		selected = *s.BalanceTokenID
	}
	balance, today := decimal.Zero, decimal.Zero
	knownBalance, knownToday := false, false
	failed := 0
	estimated := false
	seen := map[int64]bool{}
	for _, snap := range latest {
		t, ok := byID[snap.TokenID]
		if !ok {
			continue
		}
		if s.BalanceMode == "shared" && snap.TokenID != selected {
			continue
		}
		seen[snap.TokenID] = true
		if !snap.OK {
			failed++
			estimated = true
			continue
		}
		rate := t.EffectiveCNYPerUSD
		if s.BalanceMode == "shared" {
			rate = s.CNYPerUSD
		}
		if value, err := convertCNY(snap.Remaining, snap.Currency, rate); err == nil && value != nil && s.BalanceMode != "quota" && snap.BalanceKind != "quota" {
			balance = balance.Add(decimal.RequireFromString(*value))
			knownBalance = true
		}
		if snap.TodayUsedCNY != nil {
			if value, err := decimal.NewFromString(*snap.TodayUsedCNY); err == nil {
				today = today.Add(value)
				knownToday = true
			}
		}
		estimated = estimated || snap.Estimated
	}
	for id := range byID {
		if (s.BalanceMode != "shared" || id == selected) && !seen[id] {
			failed++
			estimated = true
		}
	}
	var b, c *string
	if knownBalance {
		value := balance.String()
		b = &value
	}
	if knownToday {
		value := today.String()
		c = &value
	}
	return b, c, failed, estimated
}

func (m *Module) overview(c *gin.Context) {
	ctx := c.Request.Context()
	cfg, err := m.settings(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	stations, err := m.stations(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	total, today, average := decimal.Zero, decimal.Zero, decimal.Zero
	knownBalance, knownCost, knownAverage := false, false, false
	low, exhausted, errors, unknown := 0, 0, 0, 0
	partial := false
	items := []gin.H{}
	now := time.Now().UTC()
	for _, s := range stations {
		if !s.Enabled {
			continue
		}
		tokens, e := m.tokens(ctx, s.ID)
		if e != nil {
			fail(c, e)
			return
		}
		latest, e := m.latest(ctx, s.ID)
		if e != nil {
			fail(c, e)
			return
		}
		todaySamples := todaySnapshots(latest, now, cfg.Timezone)
		b, cost, failed, estimated := currentBalance(s, tokens, todaySamples)
		partial = partial || estimated || b == nil || cost == nil
		if b != nil {
			d := decimal.RequireFromString(*b)
			total = total.Add(d)
			knownBalance = true
			if !d.IsPositive() {
				exhausted++
			} else if d.LessThanOrEqual(decimal.RequireFromString(s.LowBalance)) {
				low++
			}
		} else if s.BalanceMode != "quota" {
			unknown++
		}
		if cost != nil {
			today = today.Add(decimal.RequireFromString(*cost))
			knownCost = true
		}
		if failed > 0 {
			errors++
		}
		history, e := m.history(ctx, s.ID, now.Add(-48*time.Hour), now)
		if e != nil {
			fail(c, e)
			return
		}
		var chosen int64
		for _, t := range tokens {
			if t.Enabled {
				chosen = t.ID
				break
			}
		}
		if s.BalanceTokenID != nil {
			chosen = *s.BalanceTokenID
		}
		selected := []Snapshot{}
		for _, snap := range history {
			if snap.TokenID == chosen {
				selected = append(selected, snap)
			}
		}
		prediction := PredictExhaustion(predictionHistory(selected, s.CNYPerUSD))
		if s.BalanceMode == "independent" {
			prediction = independentPrediction(history, tokens, b)
			selected = aggregateBalanceHistory(history, tokens)
		}
		if prediction != nil {
			if burn, ok := prediction["burnPerDay"].(float64); ok {
				average = average.Add(decimal.NewFromFloat(burn))
				knownAverage = true
			}
		}
		var last *Snapshot
		for i := range latest {
			if latest[i].TokenID == chosen {
				copy := latest[i]
				last = &copy
				break
			}
		}
		stale := last == nil || now.Sub(last.CheckedAt) > time.Duration(cfg.RefreshSeconds*3)*time.Second
		items = append(items, gin.H{"id": s.ID, "name": s.Name, "type": s.Type, "enabled": s.Enabled, "balance_mode": s.BalanceMode, "cny_per_usd": s.CNYPerUSD, "latest": last, "balance_cny": b, "today_cost_cny": cost, "stale": stale, "estimated": estimated, "forecast": prediction, "history": selected})
	}
	var balanceValue, costValue, averageValue *string
	if knownBalance {
		value := total.String()
		balanceValue = &value
	}
	if knownCost {
		value := today.String()
		costValue = &value
	}
	if knownAverage {
		value := average.String()
		averageValue = &value
	}
	response.Success(c, gin.H{"balance_cny": balanceValue, "today_cost_cny": costValue, "average_daily_cost_cny": averageValue, "low_count": low, "exhausted_count": exhausted, "error_count": errors, "unknown_count": unknown, "partial": partial, "stations": items, "updated_at": now, "enabled": cfg.Enabled})
}

func independentPrediction(history []Snapshot, tokens []Token, balance *string) map[string]any {
	burn := 0.0
	samples := 0
	span := 0.0
	count := 0
	for _, token := range tokens {
		if !token.Enabled {
			continue
		}
		subset := []Snapshot{}
		for _, snap := range history {
			if snap.TokenID == token.ID {
				subset = append(subset, snap)
			}
		}
		p := PredictExhaustion(predictionHistory(subset, token.EffectiveCNYPerUSD))
		if p == nil {
			continue
		}
		count++
		if value, ok := p["burnPerDay"].(float64); ok {
			burn += value
		}
		if value, ok := p["samples"].(int); ok {
			samples += value
		}
		if value, ok := p["spanHours"].(float64); ok && value > span {
			span = value
		}
	}
	if count == 0 {
		return nil
	}
	out := map[string]any{"burnPerDay": burn, "samples": samples, "spanHours": span, "basis": "token_sum", "etaDays": nil, "etaAt": nil}
	if balance != nil && burn > 0 {
		value, err := decimal.NewFromString(*balance)
		if err == nil {
			days, _ := value.Div(decimal.NewFromFloat(burn)).Float64()
			out["etaDays"] = days
			if days >= 0 && days < 3650 {
				out["etaAt"] = time.Now().Add(time.Duration(days * 24 * float64(time.Hour))).UTC().Format(time.RFC3339)
			}
		}
	}
	return out
}

func predictionHistory(history []Snapshot, rate string) []Snapshot {
	out := make([]Snapshot, 0, len(history))
	for _, sample := range history {
		if sample.BalanceKind == "quota" {
			continue
		}
		if sample.Remaining != nil {
			value, err := convertCNY(sample.Remaining, sample.Currency, rate)
			if err != nil {
				continue
			}
			sample.RemainingCNY = value
		}
		out = append(out, sample)
	}
	return out
}

func aggregateBalanceHistory(history []Snapshot, tokens []Token) []Snapshot {
	enabled := map[int64]bool{}
	for _, t := range tokens {
		if t.Enabled {
			enabled[t.ID] = true
		}
	}
	values := map[int64]decimal.Decimal{}
	out := []Snapshot{}
	for _, snap := range history {
		if !enabled[snap.TokenID] || !snap.OK || snap.RemainingCNY == nil || snap.BalanceKind == "quota" {
			continue
		}
		d, err := decimal.NewFromString(*snap.RemainingCNY)
		if err != nil {
			continue
		}
		values[snap.TokenID] = d
		if len(values) != len(enabled) {
			continue
		}
		sum := decimal.Zero
		for _, v := range values {
			sum = sum.Add(v)
		}
		total := sum.String()
		out = append(out, Snapshot{StationID: snap.StationID, CheckedAt: snap.CheckedAt, OK: true, RemainingCNY: &total, QueryResult: QueryResult{Remaining: &total, Currency: "CNY", BalanceKind: "independent"}})
	}
	return out
}

func todaySnapshots(latest []Snapshot, now time.Time, timezone string) []Snapshot {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	today := now.In(loc).Format("2006-01-02")
	out := append([]Snapshot(nil), latest...)
	for i := range out {
		if out[i].CheckedAt.In(loc).Format("2006-01-02") != today {
			out[i].TodayUsedCNY = nil
		}
	}
	return out
}
