package dispatch

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type RoutingConfig struct {
	TTFTTargetMillis       int64 `json:"ttft_target_millis"`
	WeightSmoothingSeconds int   `json:"weight_smoothing_seconds"`
}

func normalizeRoutingConfig(routing *RoutingConfig, telemetry *TelemetryConfig) (*RoutingConfig, error) {
	if routing == nil {
		return nil, nil
	}
	if telemetry == nil {
		return nil, errors.New("dispatch routing requires live telemetry")
	}
	r := *routing
	if r.TTFTTargetMillis == 0 {
		r.TTFTTargetMillis = 1000
	}
	if r.WeightSmoothingSeconds == 0 {
		r.WeightSmoothingSeconds = 10
	}
	if r.TTFTTargetMillis < 1 || r.TTFTTargetMillis > 900000 || r.WeightSmoothingSeconds < 1 || r.WeightSmoothingSeconds > 300 {
		return nil, errors.New("invalid dispatch routing limits")
	}
	return &r, nil
}

// 新策略只追加可省略字段，保持未启用时的摘要逐字兼容。
func runtimeConfigDigest(cfg Config, limits ProbeLimits) (string, error) {
	ordered := append([]Target{}, cfg.Targets...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Ref < ordered[j].Ref })
	encoded, err := json.Marshal(struct {
		Targets       []Target
		Limits        ProbeLimits
		Telemetry     *TelemetryConfig      `json:",omitempty"`
		Quality       *qualityConfigVersion `json:",omitempty"`
		Scheduled     *ScheduledConfig      `json:",omitempty"`
		Routing       *RoutingConfig        `json:",omitempty"`
		HealthWindow  *HealthWindowConfig   `json:",omitempty"`
		ProbeSchedule *ProbeScheduleConfig  `json:",omitempty"`
		ProbeBudget   *ProbeBudgetConfig    `json:",omitempty"`
	}{ordered, limits, cfg.Telemetry, cfg.Quality.version(), cfg.Scheduled, cfg.Routing, cfg.HealthWindow, cfg.ProbeSchedule, cfg.ProbeBudget})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

type persistedProbePolicy struct {
	ProbeLimits
	Routing       *RoutingConfig       `json:"routing,omitempty"`
	HealthWindow  *HealthWindowConfig  `json:"health_window,omitempty"`
	ProbeSchedule *ProbeScheduleConfig `json:"probe_schedule,omitempty"`
	ProbeBudget   *ProbeBudgetConfig   `json:"probe_budget,omitempty"`
}
