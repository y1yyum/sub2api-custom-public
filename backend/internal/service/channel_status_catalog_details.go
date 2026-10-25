package service

import (
	"context"
	"sort"
	"strings"
)

// Detail block error codes. A failed block reports its code while the other
// blocks are returned as usual.
const (
	CatalogBlockStatusUnavailable = "status_unavailable"
	CatalogBlockRowNoData         = "no_data"

	catalogDetailEventLimit = 3
)

// CatalogBlock carries one detail block or the reason it is missing.
type CatalogBlock[T any] struct {
	Data      *T     `json:"data"`
	ErrorCode string `json:"error_code,omitempty"`
}

type CatalogOverview struct {
	Health        string                           `json:"health"`
	SuccessRate   *float64                         `json:"success_rate"`
	AvgResponseMs *float64                         `json:"avg_response_ms"`
	EventCount    int                              `json:"event_count"`
	Buckets       []CatalogBucket                  `json:"buckets"`
	Fingerprint   ModelDetectionFingerprintSummary `json:"fingerprint"`
}

type CatalogRateBreakdown struct {
	GroupRate     float64  `json:"group_rate"`
	UserRate      *float64 `json:"user_rate"`
	EffectiveRate float64  `json:"effective_rate"`
	// Independent is true when an independent image/video rate replaces the
	// group and user rate for this billing mode.
	Independent bool `json:"independent"`
}

type CatalogPeak struct {
	Start      string  `json:"start"`
	End        string  `json:"end"`
	Multiplier float64 `json:"multiplier"`
}

type CatalogTimePeriod struct {
	StartTime  string  `json:"start_time"`
	EndTime    string  `json:"end_time"`
	Multiplier float64 `json:"multiplier"`
}

type CatalogTimePricing struct {
	Timezone     string              `json:"timezone"`
	WeekdaysOnly bool                `json:"weekdays_only"`
	Periods      []CatalogTimePeriod `json:"periods"`
}

// CatalogPaidPrices holds per-token prices already multiplied by the effective rate.
type CatalogPaidPrices struct {
	Input        *float64 `json:"input"`
	Output       *float64 `json:"output"`
	CacheRead    *float64 `json:"cache_read"`
	CacheWrite   *float64 `json:"cache_write"`
	CacheWrite1h *float64 `json:"cache_write_1h"`
}

type CatalogContextTier struct {
	Label     string            `json:"label"`
	MinTokens int               `json:"min_tokens"`
	MaxTokens *int              `json:"max_tokens"`
	Paid      CatalogPaidPrices `json:"paid"`
}

type CatalogRequestTier struct {
	Label string   `json:"label"`
	Price *float64 `json:"price"`
}

type CatalogOfficialPrices struct {
	Input        *float64 `json:"input"`
	Output       *float64 `json:"output"`
	CacheRead    *float64 `json:"cache_read"`
	CacheWrite   *float64 `json:"cache_write"`
	CacheWrite1h *float64 `json:"cache_write_1h"`
}

type CatalogPricing struct {
	BillingMode string               `json:"billing_mode"`
	Rate        CatalogRateBreakdown `json:"rate"`
	Paid        CatalogPaidPrices    `json:"paid"`
	// PerRequest is the paid base per-unit price (per_request/image/video).
	PerRequest       *float64             `json:"per_request"`
	ImageInput       *float64             `json:"image_input"`
	ImageOutput      *float64             `json:"image_output"`
	RequestTiers     []CatalogRequestTier `json:"request_tiers"`
	LongContext      []CatalogContextTier `json:"long_context"`
	LongContextBasis string               `json:"long_context_basis,omitempty"`
	// LongContextEnabled is the group switch; when false the tiers show the lowest tier only.
	LongContextEnabled bool                   `json:"long_context_enabled"`
	Peak               *CatalogPeak           `json:"peak"`
	TimePricing        *CatalogTimePricing    `json:"time_pricing"`
	ReasoningEffort    map[string]float64     `json:"reasoning_effort_multipliers,omitempty"`
	Official           *CatalogOfficialPrices `json:"official"`
}

type CatalogEvents struct {
	Total int               `json:"total"`
	Items []CatalogIncident `json:"items"`
}

type CatalogDetails struct {
	Key       string `json:"key"`
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
	Platform  string `json:"platform"`
	Model     string `json:"model"`

	Overview CatalogBlock[CatalogOverview] `json:"overview"`
	Pricing  CatalogBlock[CatalogPricing]  `json:"pricing"`
	Events   CatalogBlock[CatalogEvents]   `json:"events"`
}

// Details returns the three detail blocks of one row. The key must resolve to
// a row of the viewer's own catalog (same visibility scope); anything else is
// ErrCatalogRowNotFound, so a key can never reveal data the list would hide.
// Pricing is built without the availability source; Overview and Events share
// one availability read and fail together.
func (s *ChannelStatusCatalogService) Details(ctx context.Context, q CatalogQuery, key CatalogRowKey) (*CatalogDetails, error) {
	scope := q.Filter
	scope.GroupIDs, scope.Platforms, scope.Models = nil, nil, nil
	groups, err := s.visibleGroups(ctx, scope)
	if err != nil {
		return nil, err
	}
	pg, models := findCatalogModels(groups, key)
	if pg == nil {
		return nil, ErrCatalogRowNotFound
	}

	name, _ := pg.I18n.Localize(q.AcceptLanguage, pg.Name, pg.Description)
	out := &CatalogDetails{
		Key:       key.String(),
		GroupID:   pg.ID,
		GroupName: name,
		Platform:  models[0].Platform,
		Model:     models[0].Name,
	}
	out.Pricing = CatalogBlock[CatalogPricing]{Data: buildCatalogPricing(pg, q.UserRates, models)}

	statusFilter := scope
	statusFilter.GroupIDs = []int64{key.GroupID}
	statusFilter.Platforms = []string{models[0].Platform}
	statusFilter.Models = []string{models[0].Name}
	idx, statusErr := s.loadStatus(ctx, statusFilter)
	if statusErr != nil && isCatalogFatalStatusError(statusErr) {
		return nil, statusErr
	}
	var item *ChannelMonitorV2MatrixRow
	if idx != nil {
		item = idx.rows[key]
	}
	switch {
	case statusErr != nil:
		out.Overview.ErrorCode = CatalogBlockStatusUnavailable
		out.Events.ErrorCode = CatalogBlockStatusUnavailable
	case item == nil:
		out.Overview.ErrorCode = CatalogBlockRowNoData
		out.Events.ErrorCode = CatalogBlockRowNoData
	default:
		buckets := catalogBuckets(item.Buckets, idx.coverage)
		incidents := catalogIncidents(buckets, idx.coverage.BucketSeconds)
		health := normalizeCatalogHealth(item.Health.Overall)
		ov := &CatalogOverview{
			Health:        health,
			AvgResponseMs: item.Metrics.Duration.AvgMs,
			EventCount:    len(incidents),
			Buckets:       buckets,
			Fingerprint:   NotCheckedFingerprintSummary(),
		}
		if health != CatalogHealthUnknown {
			rate := item.Metrics.SuccessRate
			ov.SuccessRate = &rate
		}
		out.Overview.Data = ov
		out.Events.Data = recentCatalogEvents(incidents)
	}
	s.fillDetailFingerprint(ctx, out, key, statusFilter)
	return out, nil
}

func (s *ChannelStatusCatalogService) fillDetailFingerprint(ctx context.Context, d *CatalogDetails, key CatalogRowKey, f ChannelMonitorV2Filter) {
	if d.Overview.Data == nil {
		return
	}
	sums, err := s.fingerprint.Summaries(ctx, []CatalogRowKey{key}, ModelDetectionWindow{Start: f.Start, End: f.End})
	if err != nil {
		return
	}
	if sum, ok := sums[key]; ok {
		d.Overview.Data.Fingerprint = sum
	}
}

func recentCatalogEvents(incidents []CatalogIncident) *CatalogEvents {
	sorted := append([]CatalogIncident(nil), incidents...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start.After(sorted[j].Start) })
	ev := &CatalogEvents{Total: len(sorted), Items: []CatalogIncident{}}
	for i := 0; i < len(sorted) && i < catalogDetailEventLimit; i++ {
		ev.Items = append(ev.Items, sorted[i])
	}
	return ev
}

// findCatalogModels returns the group and every plaza entry matching key.
func findCatalogModels(groups []PlazaGroup, key CatalogRowKey) (*PlazaGroup, []PlazaModel) {
	for gi := range groups {
		if groups[gi].ID != key.GroupID {
			continue
		}
		var found []PlazaModel
		for mi := range groups[gi].Models {
			m := groups[gi].Models[mi]
			if NewCatalogRowKey(groups[gi].ID, m.Platform, m.Name) == key {
				found = append(found, m)
			}
		}
		if len(found) == 0 {
			return nil, nil
		}
		return &groups[gi], found
	}
	return nil, nil
}

func buildCatalogPricing(pg *PlazaGroup, userRates map[int64]float64, models []PlazaModel) *CatalogPricing {
	// Several entries for one key: use the one with the lowest paid price.
	best := &models[0]
	for i := 1; i < len(models); i++ {
		_, bi, bp := catalogRowPrices(pg, userRates, best)
		_, ci, cp := catalogRowPrices(pg, userRates, &models[i])
		if lowerPrice(ci, bi) || lowerPrice(cp, bp) {
			best = &models[i]
		}
	}
	m := best
	mode := catalogBillingMode(m.Pricing)
	rate := catalogEffectiveRate(pg, userRates, mode)
	out := &CatalogPricing{
		BillingMode:        string(mode),
		LongContextEnabled: pg.LongContextPricingEnabled,
		RequestTiers:       []CatalogRequestTier{},
		LongContext:        []CatalogContextTier{},
		Rate: CatalogRateBreakdown{
			GroupRate:     pg.RateMultiplier,
			EffectiveRate: rate,
			Independent:   (mode == BillingModeImage && pg.ImageRateIndependent) || (mode == BillingModeVideo && pg.VideoRateIndependent),
		},
	}
	if r, ok := userRates[pg.ID]; ok {
		u := r
		out.Rate.UserRate = &u
	}
	if pg.PeakRateEnabled {
		out.Peak = &CatalogPeak{Start: pg.PeakStart, End: pg.PeakEnd, Multiplier: pg.PeakRateMultiplier}
	}
	out.TimePricing = catalogTimePricing(m.TimePricing)
	out.Official = catalogOfficialPrices(m.OfficialPricing)

	p := m.Pricing
	if p == nil {
		return out
	}
	out.ReasoningEffort = reasoningEffortMultipliersFromPricing(p)
	if mode == BillingModeToken {
		out.Paid = CatalogPaidPrices{
			Input:        scaledPtr(p.InputPrice, rate),
			Output:       scaledPtr(p.OutputPrice, rate),
			CacheRead:    scaledPtr(p.CacheReadPrice, rate),
			CacheWrite:   scaledPtr(p.CacheWritePrice, rate),
			CacheWrite1h: scaledPtr(p.CacheWrite1hPrice, rate),
		}
		if len(p.Intervals) > 1 {
			out.LongContextBasis = string(m.LongContextBasis)
			for i := range p.Intervals {
				iv := &p.Intervals[i]
				out.LongContext = append(out.LongContext, CatalogContextTier{
					Label:     iv.TierLabel,
					MinTokens: iv.MinTokens,
					MaxTokens: iv.MaxTokens,
					Paid: CatalogPaidPrices{
						Input:        scaledPtr(iv.InputPrice, rate),
						Output:       scaledPtr(iv.OutputPrice, rate),
						CacheRead:    scaledPtr(iv.CacheReadPrice, rate),
						CacheWrite:   scaledPtr(iv.CacheWritePrice, rate),
						CacheWrite1h: scaledPtr(iv.CacheWrite1hPrice, rate),
					},
				})
			}
		}
		return out
	}
	out.PerRequest = scaledPtr(p.PerRequestPrice, rate)
	out.ImageInput = scaledPtr(p.ImageInputPrice, rate)
	out.ImageOutput = scaledPtr(p.ImageOutputPrice, rate)
	for i := range p.Intervals {
		iv := &p.Intervals[i]
		if iv.PerRequestPrice == nil {
			continue
		}
		out.RequestTiers = append(out.RequestTiers, CatalogRequestTier{Label: iv.TierLabel, Price: scaledPtr(iv.PerRequestPrice, rate)})
	}
	return out
}

func lowerPrice(a, b *float64) bool {
	return a != nil && (b == nil || *a < *b)
}

func catalogTimePricing(t *TimePricingSchedule) *CatalogTimePricing {
	if t == nil || len(t.Periods) == 0 {
		return nil
	}
	out := &CatalogTimePricing{Timezone: t.Timezone, WeekdaysOnly: t.WeekdaysOnly, Periods: make([]CatalogTimePeriod, 0, len(t.Periods))}
	for _, p := range t.Periods {
		out.Periods = append(out.Periods, CatalogTimePeriod(p))
	}
	return out
}

func catalogOfficialPrices(o *PlazaOfficialPricing) *CatalogOfficialPrices {
	if o == nil {
		return nil
	}
	return &CatalogOfficialPrices{
		Input: o.InputPrice, Output: o.OutputPrice, CacheRead: o.CacheReadPrice,
		CacheWrite: o.CacheWritePrice, CacheWrite1h: o.CacheWrite1hPrice,
	}
}

// ParseCatalogRowKey builds a key from request parameters.
func ParseCatalogRowKey(groupID int64, platform, model string) (CatalogRowKey, bool) {
	platform, model = strings.TrimSpace(platform), strings.TrimSpace(model)
	if groupID <= 0 || platform == "" || model == "" {
		return CatalogRowKey{}, false
	}
	return NewCatalogRowKey(groupID, platform, model), true
}
