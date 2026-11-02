package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// Catalog health values mirror ChannelMonitorV2Health.Overall.
const (
	CatalogHealthHealthy  = "healthy"
	CatalogHealthWarning  = "warning"
	CatalogHealthCritical = "critical"
	CatalogHealthUnknown  = "unknown"
)

// CatalogStatusUnavailable is reported when the availability source failed but
// prices could still be built.
const CatalogStatusUnavailable = "status_unavailable"

var ErrCatalogRowNotFound = errors.New("channel status catalog row not found")

type catalogPlazaSource interface {
	ListGroups(ctx context.Context) ([]PlazaGroup, error)
}

type catalogStatusSource interface {
	Matrix(ctx context.Context, filter ChannelMonitorV2Filter, groupBy ChannelMonitorV2GroupBy, admin bool) (*ChannelMonitorV2Matrix, error)
	RecentCalls(ctx context.Context, filter ChannelMonitorV2Filter, window int) (*ChannelMonitorV2RecentCalls, error)
}

type catalogStripSource interface {
	GetCatalogStripConfig(ctx context.Context) CatalogStripConfig
}

// CatalogQuery carries the viewer-derived inputs of a catalog request.
// Filter.RestrictGroups/AllowedGroupIDs is the only visibility scope; handlers
// derive it server-side, never from query parameters.
type CatalogQuery struct {
	Filter         ChannelMonitorV2Filter
	UserRates      map[int64]float64
	AcceptLanguage string
}

type ChannelStatusCatalogCapabilities struct {
	Fingerprint bool `json:"fingerprint"`
}

// Reasons a bucket carries no health verdict. A judged bucket has none.
const (
	// CatalogBucketReasonNoData marks a slot of the timeline without any aggregated traffic.
	CatalogBucketReasonNoData = "no_data"
	// CatalogBucketReasonLowSample marks a bucket with fewer calls than the
	// strip config's min_calls.
	CatalogBucketReasonLowSample = "low_sample"
)

// maxCatalogTimelineBuckets bounds the filled timeline; coverage that would
// need more slots falls back to the sparse bucket list.
const maxCatalogTimelineBuckets = 2000

type CatalogBucket struct {
	// Start is omitted in calls mode, where blocks are not tied to time.
	Start       time.Time `json:"start,omitzero"`
	Health      string    `json:"health"`
	SuccessRate *float64  `json:"success_rate"`
	// Reason explains an unknown bucket: no_data or low_sample.
	Reason string `json:"reason,omitempty"`
	// CacheLevel is high/mid/low/none; omitted for rows not billed per token.
	CacheLevel string   `json:"cache_level,omitempty"`
	CacheRate  *float64 `json:"cache_rate,omitempty"`
	// TTFTMs is the mean time to first token of the block's successful calls.
	TTFTMs *float64 `json:"ttft_ms,omitempty"`
}

type CatalogGroupSummary struct {
	ModelCount int `json:"model_count"`
	// Health is the worst known row status; unknown only when no row has data.
	Health string `json:"health"`
	// AvailabilityRate is the mean success rate of rows with data (0–1), nil
	// when none has data. Request volume is redacted for ordinary viewers, so
	// it cannot be weighted.
	AvailabilityRate *float64 `json:"availability_rate"`
	DegradedCount    int      `json:"degraded_count"`
	DownCount        int      `json:"down_count"`
}

type CatalogModelRow struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	// BillingMode is "token", "per_request", "image" or "video".
	BillingMode string `json:"billing_mode"`
	// InputPrice is the paid standard-period first-tier input price per token
	// (already multiplied by the effective rate); nil means not configured.
	InputPrice *float64 `json:"input_price"`
	// OutputPrice is the matching paid output price per token; nil means not configured.
	OutputPrice *float64 `json:"output_price"`
	// PerRequestPrice is the lowest paid per-unit price for non-token models.
	PerRequestPrice *float64                         `json:"per_request_price"`
	PriceConflict   bool                             `json:"price_conflict"`
	Health          string                           `json:"health"`
	SuccessRate     *float64                         `json:"success_rate"`
	Buckets         []CatalogBucket                  `json:"buckets"`
	EventCount      int                              `json:"event_count"`
	Fingerprint     ModelDetectionFingerprintSummary `json:"fingerprint"`
	// Intelligence is the recent quiz score; nil when the row has no quiz results.
	Intelligence *CatalogIntelligenceSummary `json:"intelligence"`
}

type CatalogGroup struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	Platform           string   `json:"platform"`
	RateMultiplier     float64  `json:"rate_multiplier"`
	UserRateMultiplier *float64 `json:"user_rate_multiplier"`
	PeakRateEnabled    bool     `json:"peak_rate_enabled"`
	PeakStart          string   `json:"peak_start"`
	PeakEnd            string   `json:"peak_end"`
	PeakRateMultiplier float64  `json:"peak_rate_multiplier"`
	IsExclusive        bool     `json:"is_exclusive"`

	Summary CatalogGroupSummary `json:"summary"`
	Models  []CatalogModelRow   `json:"models"`
}

type ChannelStatusCatalog struct {
	Groups       []CatalogGroup                   `json:"groups"`
	Coverage     *ChannelMonitorV2Coverage        `json:"coverage"`
	Capabilities ChannelStatusCatalogCapabilities `json:"capabilities"`
	Strip        CatalogStripConfig               `json:"strip"`
	// StatusErrorCode is set when availability data could not be loaded; rows
	// then carry unknown status and prices remain valid.
	StatusErrorCode string `json:"status_error_code,omitempty"`
}

// ChannelStatusCatalogService joins model-plaza prices with V2 availability.
type ChannelStatusCatalogService struct {
	plaza        catalogPlazaSource
	status       catalogStatusSource
	fingerprint  ModelDetectionFingerprintProvider
	intelligence *catalogIntelligenceReader
	strip        catalogStripSource
	calls        *catalogCallCache
}

func NewChannelStatusCatalogService(plaza *ModelPlazaService, status *ChannelMonitorV2Service, intelligence CatalogIntelligenceRepository, accounts AccountRepository, settings *SettingService) *ChannelStatusCatalogService {
	s := newChannelStatusCatalogService(plaza, status, NoopModelDetectionFingerprintProvider{})
	s.intelligence = &catalogIntelligenceReader{repo: intelligence, accounts: accounts}
	s.strip = settings
	return s
}

func newChannelStatusCatalogService(plaza catalogPlazaSource, status catalogStatusSource, fp ModelDetectionFingerprintProvider) *ChannelStatusCatalogService {
	if fp == nil {
		fp = NoopModelDetectionFingerprintProvider{}
	}
	return &ChannelStatusCatalogService{plaza: plaza, status: status, fingerprint: fp, calls: newCatalogCallCache()}
}

// StripConfig returns the strip rules shared by the list and the drawer.
func (s *ChannelStatusCatalogService) StripConfig(ctx context.Context) CatalogStripConfig {
	if s.strip == nil {
		return DefaultCatalogStripConfig()
	}
	return s.strip.GetCatalogStripConfig(ctx)
}

// Catalog builds the grouped model list for the viewer described by q.
func (s *ChannelStatusCatalogService) Catalog(ctx context.Context, q CatalogQuery) (*ChannelStatusCatalog, error) {
	groups, err := s.visibleGroups(ctx, q.Filter)
	if err != nil {
		return nil, err
	}
	strip := s.StripConfig(ctx)
	out := &ChannelStatusCatalog{Groups: []CatalogGroup{}, Strip: strip}
	status, statusErr := s.loadStatus(ctx, q.Filter, strip)
	if statusErr != nil {
		if isCatalogFatalStatusError(statusErr) {
			return nil, statusErr
		}
		out.StatusErrorCode = CatalogStatusUnavailable
	}
	if status != nil {
		out.Coverage = status.coverage
	}

	var keys []CatalogRowKey
	for gi := range groups {
		pg := &groups[gi]
		rows := mergeCatalogModels(pg, q.UserRates, q.Filter)
		if len(rows) == 0 {
			continue
		}
		for ri := range rows {
			applyCatalogStatus(&rows[ri], pg.ID, status)
			keys = append(keys, catalogRowKeyOf(pg.ID, &rows[ri]))
		}
		sortCatalogRows(rows)
		cg := buildCatalogGroup(pg, q)
		cg.Models = rows
		cg.Summary = summarizeCatalogRows(rows)
		out.Groups = append(out.Groups, cg)
	}

	s.fillFingerprints(ctx, out, keys, q.Filter)
	s.fillIntelligence(ctx, out)
	return out, nil
}

// fillIntelligence adds quiz scores; a failed read only leaves them empty.
func (s *ChannelStatusCatalogService) fillIntelligence(ctx context.Context, cat *ChannelStatusCatalog) {
	if s.intelligence == nil {
		return
	}
	rows := map[CatalogRowKey]string{}
	for gi := range cat.Groups {
		g := &cat.Groups[gi]
		for ri := range g.Models {
			rows[catalogRowKeyOf(g.ID, &g.Models[ri])] = g.Models[ri].Name
		}
	}
	summaries, err := s.intelligence.Summaries(ctx, rows)
	if err != nil {
		slog.Warn("channel_status_catalog_intelligence_failed", "error", err)
		return
	}
	for gi := range cat.Groups {
		g := &cat.Groups[gi]
		for ri := range g.Models {
			if sum, ok := summaries[catalogRowKeyOf(g.ID, &g.Models[ri])]; ok {
				g.Models[ri].Intelligence = &sum
			}
		}
	}
}

func (s *ChannelStatusCatalogService) fillFingerprints(ctx context.Context, cat *ChannelStatusCatalog, keys []CatalogRowKey, f ChannelMonitorV2Filter) {
	summaries, err := s.fingerprint.Summaries(ctx, keys, ModelDetectionWindow{Start: f.Start, End: f.End})
	if err != nil {
		summaries = nil
	}
	for gi := range cat.Groups {
		g := &cat.Groups[gi]
		for ri := range g.Models {
			row := &g.Models[ri]
			sum, ok := summaries[catalogRowKeyOf(g.ID, row)]
			if !ok {
				sum = NotCheckedFingerprintSummary()
			}
			row.Fingerprint = sum
		}
	}
}

func isCatalogFatalStatusError(err error) bool {
	return errors.Is(err, ErrChannelMonitorDisabled) || errors.Is(err, ErrChannelMonitorModeMismatch)
}

// visibleGroups lists plaza groups the viewer may see, applying the group
// filter. Visibility is decided before any status is joined.
func (s *ChannelStatusCatalogService) visibleGroups(ctx context.Context, f ChannelMonitorV2Filter) ([]PlazaGroup, error) {
	all, err := s.plaza.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list catalog groups: %w", err)
	}
	allowed := int64Set(f.AllowedGroupIDs)
	wanted := int64Set(f.GroupIDs)
	out := make([]PlazaGroup, 0, len(all))
	for i := range all {
		g := all[i]
		if f.RestrictGroups {
			if _, ok := allowed[g.ID]; !ok {
				continue
			}
		}
		if len(wanted) > 0 {
			if _, ok := wanted[g.ID]; !ok {
				continue
			}
		}
		out = append(out, g)
	}
	return out, nil
}

func int64Set(values []int64) map[int64]struct{} {
	m := make(map[int64]struct{}, len(values))
	for _, v := range values {
		m[v] = struct{}{}
	}
	return m
}

func buildCatalogGroup(pg *PlazaGroup, q CatalogQuery) CatalogGroup {
	name, _ := pg.I18n.Localize(q.AcceptLanguage, pg.Name, pg.Description)
	cg := CatalogGroup{
		ID:                 pg.ID,
		Name:               name,
		Platform:           pg.Platform,
		RateMultiplier:     pg.RateMultiplier,
		PeakRateEnabled:    pg.PeakRateEnabled,
		PeakStart:          pg.PeakStart,
		PeakEnd:            pg.PeakEnd,
		PeakRateMultiplier: pg.PeakRateMultiplier,
		IsExclusive:        pg.IsExclusive,
		Models:             []CatalogModelRow{},
	}
	if rate, ok := q.UserRates[pg.ID]; ok {
		r := rate
		cg.UserRateMultiplier = &r
	}
	return cg
}

// catalogEffectiveRate mirrors the plaza table: independent image/video rates
// override the group/user rate for those billing modes.
func catalogEffectiveRate(pg *PlazaGroup, userRates map[int64]float64, mode BillingMode) float64 {
	switch {
	case mode == BillingModeVideo && pg.VideoRateIndependent:
		if pg.VideoRateMultiplier < 0 {
			return 0
		}
		return pg.VideoRateMultiplier
	case mode == BillingModeImage && pg.ImageRateIndependent:
		return pg.ImageRateMultiplier
	}
	if rate, ok := userRates[pg.ID]; ok {
		return rate
	}
	return pg.RateMultiplier
}

func catalogBillingMode(p *ChannelModelPricing) BillingMode {
	if p == nil || p.BillingMode == "" {
		return BillingModeToken
	}
	return p.BillingMode
}

func scaledPtr(v *float64, rate float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v * rate
	return &out
}

func minPtr(a, b *float64) *float64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case *b < *a:
		return b
	}
	return a
}

type catalogRowPrice struct {
	input, output, perRequest *float64
}

// catalogRowPrices returns the paid list prices of a model; nil when unpriced.
func catalogRowPrices(pg *PlazaGroup, userRates map[int64]float64, m *PlazaModel) (BillingMode, catalogRowPrice) {
	mode := catalogBillingMode(m.Pricing)
	if m.Pricing == nil {
		return mode, catalogRowPrice{}
	}
	rate := catalogEffectiveRate(pg, userRates, mode)
	if mode == BillingModeToken {
		return mode, catalogRowPrice{input: scaledPtr(m.Pricing.InputPrice, rate), output: scaledPtr(m.Pricing.OutputPrice, rate)}
	}
	lowest := m.Pricing.PerRequestPrice
	for i := range m.Pricing.Intervals {
		lowest = minPtr(lowest, m.Pricing.Intervals[i].PerRequestPrice)
	}
	return mode, catalogRowPrice{perRequest: scaledPtr(lowest, rate)}
}

// mergeCatalogModels turns a plaza group into rows keyed by platform+model.
// Duplicate entries for one key keep the lowest paid price and set the
// conflict marker only when the prices actually differ.
func mergeCatalogModels(pg *PlazaGroup, userRates map[int64]float64, f ChannelMonitorV2Filter) []CatalogModelRow {
	platforms := make(map[string]struct{}, len(f.Platforms))
	for _, p := range f.Platforms {
		platforms[strings.ToLower(strings.TrimSpace(p))] = struct{}{}
	}
	models := make(map[string]struct{}, len(f.Models))
	for _, m := range f.Models {
		models[NormalizeCatalogModel(m)] = struct{}{}
	}
	index := map[CatalogRowKey]int{}
	rows := make([]CatalogModelRow, 0, len(pg.Models))
	for i := range pg.Models {
		m := &pg.Models[i]
		key := NewCatalogRowKey(pg.ID, m.Platform, m.Name)
		if len(platforms) > 0 {
			if _, ok := platforms[key.Platform]; !ok {
				continue
			}
		}
		if len(models) > 0 {
			if _, ok := models[key.Model]; !ok {
				continue
			}
		}
		mode, price := catalogRowPrices(pg, userRates, m)
		if at, seen := index[key]; seen {
			row := &rows[at]
			if !samePrice(row.InputPrice, price.input) || !samePrice(row.OutputPrice, price.output) || !samePrice(row.PerRequestPrice, price.perRequest) {
				row.PriceConflict = true
			}
			row.InputPrice = minPtr(row.InputPrice, price.input)
			row.OutputPrice = minPtr(row.OutputPrice, price.output)
			row.PerRequestPrice = minPtr(row.PerRequestPrice, price.perRequest)
			continue
		}
		index[key] = len(rows)
		rows = append(rows, CatalogModelRow{
			Key:             key.String(),
			Name:            m.Name,
			Platform:        m.Platform,
			BillingMode:     string(mode),
			InputPrice:      price.input,
			OutputPrice:     price.output,
			PerRequestPrice: price.perRequest,
			Health:          CatalogHealthUnknown,
			Buckets:         []CatalogBucket{},
			Fingerprint:     NotCheckedFingerprintSummary(),
		})
	}
	return rows
}

func samePrice(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func catalogRowKeyOf(groupID int64, row *CatalogModelRow) CatalogRowKey {
	return NewCatalogRowKey(groupID, row.Platform, row.Name)
}

// catalogStatusIndex holds the judged status of every row with traffic.
type catalogStatusIndex struct {
	rows     map[CatalogRowKey]*catalogRowStatus
	coverage *ChannelMonitorV2Coverage
	strip    CatalogStripConfig
}

// emptyBuckets is the strip of a row without traffic.
func (idx *catalogStatusIndex) emptyBuckets() []CatalogBucket {
	if idx.strip.Mode == CatalogStripModeCalls {
		return catalogEmptyCallBuckets(idx.strip)
	}
	var cov ChannelMonitorV2Coverage
	if idx.coverage != nil {
		cov = *idx.coverage
	}
	return catalogBuckets(nil, cov, catalogStripJudge{cfg: idx.strip})
}

func (s *ChannelStatusCatalogService) loadStatus(ctx context.Context, f ChannelMonitorV2Filter, strip CatalogStripConfig) (*catalogStatusIndex, error) {
	if strip.Mode == CatalogStripModeCalls {
		return s.loadCallStatus(ctx, f, strip)
	}
	// The admin view keeps the counts the strip rules need; counts are never returned.
	matrix, err := s.status.Matrix(ctx, f, ChannelMonitorV2GroupByPlatformGroupModel, true)
	if err != nil {
		return nil, err
	}
	idx := &catalogStatusIndex{rows: map[CatalogRowKey]*catalogRowStatus{}, strip: strip}
	if matrix == nil {
		return idx, nil
	}
	cov := matrix.Coverage
	idx.coverage = &cov
	for i := range matrix.Items {
		item := &matrix.Items[i]
		if item.GroupID == nil {
			continue
		}
		idx.rows[NewCatalogRowKey(*item.GroupID, item.Platform, item.Model)] = catalogTimeStatus(item, cov, strip)
	}
	return idx, nil
}

func (s *ChannelStatusCatalogService) loadCallStatus(ctx context.Context, f ChannelMonitorV2Filter, strip CatalogStripConfig) (*catalogStatusIndex, error) {
	recent, err := s.calls.get(ctx, f, strip.CallWindow, s.status.RecentCalls)
	if err != nil {
		return nil, err
	}
	idx := &catalogStatusIndex{rows: map[CatalogRowKey]*catalogRowStatus{}, strip: strip}
	if recent == nil {
		return idx, nil
	}
	idx.coverage = recent.Coverage
	for key, minutes := range catalogCallMinutesByRow(recent.Minutes) {
		idx.rows[key] = catalogCallStatus(minutes, strip)
	}
	return idx, nil
}

func applyCatalogStatus(row *CatalogModelRow, groupID int64, idx *catalogStatusIndex) {
	if idx == nil {
		return
	}
	st, ok := idx.rows[catalogRowKeyOf(groupID, row)]
	if !ok {
		row.Buckets = catalogBucketsForBilling(idx.emptyBuckets(), row.BillingMode)
		return
	}
	row.Health = st.health
	row.SuccessRate = st.successRate
	row.Buckets = catalogBucketsForBilling(st.buckets, row.BillingMode)
	row.EventCount = len(st.incidents)
}

// catalogBuckets lays the aggregated buckets on the requested window
// (coverage.requested_start..requested_end in bucket_seconds steps) so every
// row and the detail drawer share one fixed-length timeline. Slots without
// aggregated traffic are explicit unknown/no_data placeholders. Without
// usable coverage the sparse list is returned as is.
func catalogBuckets(in []ChannelMonitorV2TrendPoint, cov ChannelMonitorV2Coverage, judge catalogStripJudge) []CatalogBucket {
	step := time.Duration(cov.BucketSeconds) * time.Second
	window := cov.RequestedEnd.Sub(cov.RequestedStart)
	if step <= 0 || window <= 0 || window/step+1 > maxCatalogTimelineBuckets {
		out := make([]CatalogBucket, 0, len(in))
		for i := range in {
			out = append(out, judge.trendBucket(in[i]))
		}
		return out
	}
	slots := int((window + step - 1) / step)
	out := make([]CatalogBucket, slots)
	for i := range out {
		out[i] = judge.emptyBucket(cov.RequestedStart.Add(time.Duration(i) * step))
	}
	for i := range in {
		offset := in[i].BucketStart.Sub(cov.RequestedStart)
		if offset < 0 {
			// A bucket that began before an unaligned window start still covers its first slot.
			if offset+step <= 0 {
				continue
			}
			offset = 0
		}
		slot := int(offset / step)
		if slot >= slots {
			continue
		}
		b := judge.trendBucket(in[i])
		if cur := out[slot]; cur.Reason != CatalogBucketReasonNoData && catalogSeverity(cur.Health) <= catalogSeverity(b.Health) {
			continue
		}
		out[slot] = b
	}
	return out
}

// CatalogIncident is a run of consecutive degraded or down buckets.
// DurationSeconds is omitted in calls mode, where it would reveal the call rate.
type CatalogIncident struct {
	Start           time.Time `json:"start"`
	Level           string    `json:"level"`
	DurationSeconds int64     `json:"duration_seconds,omitempty"`
	MinSuccessRate  *float64  `json:"min_success_rate"`
}

// catalogIncidents groups consecutive warning/critical buckets. Healthy and
// unknown buckets both end a run: unknown never counts as an incident.
func catalogIncidents(buckets []CatalogBucket, bucketSeconds int) []CatalogIncident {
	var out []CatalogIncident
	var cur *CatalogIncident
	count := 0
	flush := func() {
		if cur != nil {
			cur.DurationSeconds = int64(count) * int64(bucketSeconds)
			out = append(out, *cur)
			cur, count = nil, 0
		}
	}
	for _, b := range buckets {
		if b.Health != CatalogHealthWarning && b.Health != CatalogHealthCritical {
			flush()
			continue
		}
		if cur == nil {
			cur = &CatalogIncident{Start: b.Start, Level: b.Health}
		}
		count++
		if b.Health == CatalogHealthCritical {
			cur.Level = CatalogHealthCritical
		}
		if b.SuccessRate != nil && (cur.MinSuccessRate == nil || *b.SuccessRate < *cur.MinSuccessRate) {
			r := *b.SuccessRate
			cur.MinSuccessRate = &r
		}
	}
	flush()
	return out
}

func catalogSeverity(health string) int {
	switch health {
	case CatalogHealthCritical:
		return 0
	case CatalogHealthWarning:
		return 1
	case CatalogHealthHealthy:
		return 2
	}
	return 3
}

func sortCatalogRows(rows []CatalogModelRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		si, sj := catalogSeverity(rows[i].Health), catalogSeverity(rows[j].Health)
		if si != sj {
			return si < sj
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Platform < rows[j].Platform
	})
}

func summarizeCatalogRows(rows []CatalogModelRow) CatalogGroupSummary {
	sum := CatalogGroupSummary{ModelCount: len(rows), Health: CatalogHealthUnknown}
	var total float64
	var known int
	for i := range rows {
		r := &rows[i]
		if catalogSeverity(r.Health) < catalogSeverity(sum.Health) {
			sum.Health = r.Health
		}
		switch r.Health {
		case CatalogHealthWarning:
			sum.DegradedCount++
		case CatalogHealthCritical:
			sum.DownCount++
		}
		if r.SuccessRate != nil {
			total += *r.SuccessRate
			known++
		}
	}
	if known > 0 {
		avg := total / float64(known)
		sum.AvailabilityRate = &avg
	}
	return sum
}
