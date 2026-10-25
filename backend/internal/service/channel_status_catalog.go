package service

import (
	"context"
	"errors"
	"fmt"
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
	// CatalogBucketReasonLowSample marks a bucket with traffic below the V2 minimum
	// sample, which V2 refuses to judge.
	CatalogBucketReasonLowSample = "low_sample"
)

// maxCatalogTimelineBuckets bounds the filled timeline; coverage that would
// need more slots falls back to the sparse bucket list.
const maxCatalogTimelineBuckets = 2000

type CatalogBucket struct {
	Start       time.Time `json:"start"`
	Health      string    `json:"health"`
	SuccessRate *float64  `json:"success_rate"`
	// Reason explains an unknown bucket: no_data or low_sample.
	Reason string `json:"reason,omitempty"`
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
	// PerRequestPrice is the lowest paid per-unit price for non-token models.
	PerRequestPrice *float64                         `json:"per_request_price"`
	PriceConflict   bool                             `json:"price_conflict"`
	Health          string                           `json:"health"`
	SuccessRate     *float64                         `json:"success_rate"`
	Buckets         []CatalogBucket                  `json:"buckets"`
	EventCount      int                              `json:"event_count"`
	Fingerprint     ModelDetectionFingerprintSummary `json:"fingerprint"`
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
	// StatusErrorCode is set when availability data could not be loaded; rows
	// then carry unknown status and prices remain valid.
	StatusErrorCode string `json:"status_error_code,omitempty"`
}

// ChannelStatusCatalogService joins model-plaza prices with V2 availability.
type ChannelStatusCatalogService struct {
	plaza       catalogPlazaSource
	status      catalogStatusSource
	fingerprint ModelDetectionFingerprintProvider
}

func NewChannelStatusCatalogService(plaza *ModelPlazaService, status *ChannelMonitorV2Service) *ChannelStatusCatalogService {
	return newChannelStatusCatalogService(plaza, status, NoopModelDetectionFingerprintProvider{})
}

func newChannelStatusCatalogService(plaza catalogPlazaSource, status catalogStatusSource, fp ModelDetectionFingerprintProvider) *ChannelStatusCatalogService {
	if fp == nil {
		fp = NoopModelDetectionFingerprintProvider{}
	}
	return &ChannelStatusCatalogService{plaza: plaza, status: status, fingerprint: fp}
}

// Catalog builds the grouped model list for the viewer described by q.
func (s *ChannelStatusCatalogService) Catalog(ctx context.Context, q CatalogQuery) (*ChannelStatusCatalog, error) {
	groups, err := s.visibleGroups(ctx, q.Filter)
	if err != nil {
		return nil, err
	}
	out := &ChannelStatusCatalog{Groups: []CatalogGroup{}}
	status, statusErr := s.loadStatus(ctx, q.Filter)
	if statusErr != nil {
		if isCatalogFatalStatusError(statusErr) {
			return nil, statusErr
		}
		out.StatusErrorCode = CatalogStatusUnavailable
	}
	if status != nil {
		cov := status.coverage
		out.Coverage = &cov
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
	return out, nil
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

// catalogRowPrices returns (input, perRequest) paid prices; nil when unpriced.
func catalogRowPrices(pg *PlazaGroup, userRates map[int64]float64, m *PlazaModel) (BillingMode, *float64, *float64) {
	mode := catalogBillingMode(m.Pricing)
	if m.Pricing == nil {
		return mode, nil, nil
	}
	rate := catalogEffectiveRate(pg, userRates, mode)
	if mode == BillingModeToken {
		return mode, scaledPtr(m.Pricing.InputPrice, rate), nil
	}
	lowest := m.Pricing.PerRequestPrice
	for i := range m.Pricing.Intervals {
		lowest = minPtr(lowest, m.Pricing.Intervals[i].PerRequestPrice)
	}
	return mode, nil, scaledPtr(lowest, rate)
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
		mode, input, perReq := catalogRowPrices(pg, userRates, m)
		if at, seen := index[key]; seen {
			row := &rows[at]
			if !samePrice(row.InputPrice, input) || !samePrice(row.PerRequestPrice, perReq) {
				row.PriceConflict = true
			}
			row.InputPrice = minPtr(row.InputPrice, input)
			row.PerRequestPrice = minPtr(row.PerRequestPrice, perReq)
			continue
		}
		index[key] = len(rows)
		rows = append(rows, CatalogModelRow{
			Key:             key.String(),
			Name:            m.Name,
			Platform:        m.Platform,
			BillingMode:     string(mode),
			InputPrice:      input,
			PerRequestPrice: perReq,
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

// catalogStatusIndex indexes matrix rows by composite key.
type catalogStatusIndex struct {
	rows     map[CatalogRowKey]*ChannelMonitorV2MatrixRow
	coverage ChannelMonitorV2Coverage
}

func (s *ChannelStatusCatalogService) loadStatus(ctx context.Context, f ChannelMonitorV2Filter) (*catalogStatusIndex, error) {
	matrix, err := s.status.Matrix(ctx, f, ChannelMonitorV2GroupByPlatformGroupModel, false)
	if err != nil {
		return nil, err
	}
	idx := &catalogStatusIndex{rows: map[CatalogRowKey]*ChannelMonitorV2MatrixRow{}}
	if matrix == nil {
		return idx, nil
	}
	idx.coverage = matrix.Coverage
	for i := range matrix.Items {
		item := &matrix.Items[i]
		if item.GroupID == nil {
			continue
		}
		idx.rows[NewCatalogRowKey(*item.GroupID, item.Platform, item.Model)] = item
	}
	return idx, nil
}

func applyCatalogStatus(row *CatalogModelRow, groupID int64, idx *catalogStatusIndex) {
	if idx == nil {
		return
	}
	item, ok := idx.rows[catalogRowKeyOf(groupID, row)]
	if !ok {
		row.Buckets = catalogBuckets(nil, idx.coverage)
		return
	}
	row.Health = normalizeCatalogHealth(item.Health.Overall)
	if row.Health != CatalogHealthUnknown {
		rate := item.Metrics.SuccessRate
		row.SuccessRate = &rate
	}
	row.Buckets = catalogBuckets(item.Buckets, idx.coverage)
	row.EventCount = len(catalogIncidents(row.Buckets, idx.coverage.BucketSeconds))
}

func normalizeCatalogHealth(v string) string {
	switch v {
	case CatalogHealthHealthy, CatalogHealthWarning, CatalogHealthCritical:
		return v
	}
	return CatalogHealthUnknown
}

// catalogBucket converts one aggregated bucket; unknown health means V2 saw
// traffic but too little of it to judge.
func catalogBucket(in ChannelMonitorV2TrendPoint) CatalogBucket {
	b := CatalogBucket{Start: in.BucketStart, Health: normalizeCatalogHealth(in.Health.Overall)}
	if b.Health == CatalogHealthUnknown {
		b.Reason = CatalogBucketReasonLowSample
		return b
	}
	rate := in.Metrics.SuccessRate
	b.SuccessRate = &rate
	return b
}

// catalogBuckets lays the aggregated buckets on the requested window
// (coverage.requested_start..requested_end in bucket_seconds steps) so every
// row and the detail drawer share one fixed-length timeline. Slots without
// aggregated traffic are explicit unknown/no_data placeholders; the V2
// minimum-sample rule is untouched. Without usable coverage the sparse list
// is returned as is.
func catalogBuckets(in []ChannelMonitorV2TrendPoint, cov ChannelMonitorV2Coverage) []CatalogBucket {
	step := time.Duration(cov.BucketSeconds) * time.Second
	window := cov.RequestedEnd.Sub(cov.RequestedStart)
	if step <= 0 || window <= 0 || window/step+1 > maxCatalogTimelineBuckets {
		out := make([]CatalogBucket, 0, len(in))
		for i := range in {
			out = append(out, catalogBucket(in[i]))
		}
		return out
	}
	slots := int((window + step - 1) / step)
	out := make([]CatalogBucket, slots)
	for i := range out {
		out[i] = CatalogBucket{
			Start:  cov.RequestedStart.Add(time.Duration(i) * step),
			Health: CatalogHealthUnknown,
			Reason: CatalogBucketReasonNoData,
		}
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
		b := catalogBucket(in[i])
		if cur := out[slot]; cur.Reason != CatalogBucketReasonNoData && catalogSeverity(cur.Health) <= catalogSeverity(b.Health) {
			continue
		}
		out[slot] = b
	}
	return out
}

// CatalogIncident is a run of consecutive degraded or down buckets.
type CatalogIncident struct {
	Start           time.Time `json:"start"`
	Level           string    `json:"level"`
	DurationSeconds int64     `json:"duration_seconds"`
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
