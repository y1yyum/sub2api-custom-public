package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// SettingKeyCatalogStripConfig stores the strip rules of the "models and
// channels" page as one JSON value.
const SettingKeyCatalogStripConfig = "catalog_strip_config"

// Strip modes: time buckets follow the page range; calls splits the latest
// call_window calls into blocks regardless of when they happened.
const (
	CatalogStripModeTime  = "time"
	CatalogStripModeCalls = "calls"
)

// Cache levels drive the block height. Rows that are not billed per token
// carry no level and are drawn at full height.
const (
	CatalogCacheLevelHigh = "high"
	CatalogCacheLevelMid  = "mid"
	CatalogCacheLevelLow  = "low"
	CatalogCacheLevelNone = "none"
)

const (
	catalogStripMinCallWindow = 20
	catalogStripMaxCallWindow = 2000
	catalogStripMinBlocks     = 10
	catalogStripMaxBlocks     = 48
	catalogStripMaxMinCalls   = 1000
)

type CatalogAvailabilitySteps struct {
	Green  float64 `json:"green"`
	Yellow float64 `json:"yellow"`
}

type CatalogCacheSteps struct {
	High float64 `json:"high"`
	Mid  float64 `json:"mid"`
	Low  float64 `json:"low"`
}

// CatalogStripConfig decides how strip blocks are counted and coloured. It is
// returned to every viewer: none of it is operational or per-user data.
type CatalogStripConfig struct {
	Mode         string                   `json:"mode"`
	CallWindow   int                      `json:"call_window"`
	Blocks       int                      `json:"blocks"`
	MinCalls     int                      `json:"min_calls"`
	Availability CatalogAvailabilitySteps `json:"availability"`
	Cache        CatalogCacheSteps        `json:"cache"`
}

func DefaultCatalogStripConfig() CatalogStripConfig {
	return CatalogStripConfig{
		Mode:         CatalogStripModeTime,
		CallWindow:   100,
		Blocks:       20,
		MinCalls:     1,
		Availability: CatalogAvailabilitySteps{Green: 0.95, Yellow: 0.80},
		Cache:        CatalogCacheSteps{High: 0.50, Mid: 0.20, Low: 0.01},
	}
}

func catalogStripInvalid(format string, args ...any) error {
	return infraerrors.BadRequest("CATALOG_STRIP_CONFIG_INVALID", fmt.Sprintf(format, args...))
}

func inUnitRange(v float64) bool { return v >= 0 && v <= 1 }

func (c CatalogStripConfig) Validate() error {
	switch {
	case c.Mode != CatalogStripModeTime && c.Mode != CatalogStripModeCalls:
		return catalogStripInvalid("mode must be %q or %q", CatalogStripModeTime, CatalogStripModeCalls)
	case c.CallWindow < catalogStripMinCallWindow || c.CallWindow > catalogStripMaxCallWindow:
		return catalogStripInvalid("call_window must be between %d and %d", catalogStripMinCallWindow, catalogStripMaxCallWindow)
	case c.Blocks < catalogStripMinBlocks || c.Blocks > catalogStripMaxBlocks:
		return catalogStripInvalid("blocks must be between %d and %d", catalogStripMinBlocks, catalogStripMaxBlocks)
	case c.CallWindow < c.Blocks:
		return catalogStripInvalid("call_window must not be smaller than blocks")
	case c.MinCalls < 1 || c.MinCalls > catalogStripMaxMinCalls:
		return catalogStripInvalid("min_calls must be between 1 and %d", catalogStripMaxMinCalls)
	case !inUnitRange(c.Availability.Green) || !inUnitRange(c.Availability.Yellow) || c.Availability.Yellow > c.Availability.Green:
		return catalogStripInvalid("availability steps must satisfy 0 <= yellow <= green <= 1")
	case !inUnitRange(c.Cache.High) || !inUnitRange(c.Cache.Mid) || !inUnitRange(c.Cache.Low) ||
		c.Cache.Low > c.Cache.Mid || c.Cache.Mid > c.Cache.High:
		return catalogStripInvalid("cache steps must satisfy 0 <= low <= mid <= high <= 1")
	}
	return nil
}

// GetCatalogStripConfig never fails: a missing, unreadable or invalid value
// falls back to the defaults so the page keeps rendering.
func (s *SettingService) GetCatalogStripConfig(ctx context.Context) CatalogStripConfig {
	def := DefaultCatalogStripConfig()
	if s == nil || s.settingRepo == nil {
		return def
	}
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyCatalogStripConfig})
	if err != nil {
		slog.Warn("catalog_strip_config_read_failed", "error", err)
		return def
	}
	raw, ok := vals[SettingKeyCatalogStripConfig]
	if !ok || raw == "" {
		return def
	}
	cfg := def
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil || cfg.Validate() != nil {
		slog.Warn("catalog_strip_config_invalid", "error", err)
		return def
	}
	return cfg
}

func (s *SettingService) UpdateCatalogStripConfig(ctx context.Context, cfg CatalogStripConfig) (CatalogStripConfig, error) {
	if err := cfg.Validate(); err != nil {
		return CatalogStripConfig{}, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return CatalogStripConfig{}, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyCatalogStripConfig, string(raw)); err != nil {
		return CatalogStripConfig{}, fmt.Errorf("save catalog strip config: %w", err)
	}
	return cfg, nil
}
