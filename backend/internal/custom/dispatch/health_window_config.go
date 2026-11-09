package dispatch

import "errors"

type HealthWindowConfig struct {
	MinSamples int `json:"min_samples"`
	// ProtocolFacts 只开启 009 事实的读取与报告计数；false 时省略，旧摘要不变。
	ProtocolFacts bool `json:"protocol_facts,omitempty"`
}

func normalizeHealthWindowConfig(policy *HealthWindowConfig, routing *RoutingConfig, telemetry *TelemetryConfig) (*HealthWindowConfig, error) {
	if policy == nil {
		return nil, nil
	}
	if routing == nil || telemetry == nil {
		return nil, errors.New("dispatch health window requires routing and live telemetry")
	}
	normalized := *policy
	if normalized.MinSamples == 0 {
		normalized.MinSamples = 20
	}
	if normalized.MinSamples < 20 || normalized.MinSamples > 100000 {
		return nil, errors.New("invalid dispatch health window minimum samples")
	}
	return &normalized, nil
}
