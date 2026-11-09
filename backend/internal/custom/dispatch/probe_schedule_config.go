package dispatch

import "errors"

type ProbeScheduleConfig struct {
	IdleIntervalSeconds int `json:"idle_interval_seconds"`
	BusyIntervalSeconds int `json:"busy_interval_seconds"`
	PassiveMinSamples   int `json:"passive_min_samples"`
}

func normalizeProbeScheduleConfig(policy *ProbeScheduleConfig, probe *ProbeConfig, window *HealthWindowConfig, telemetry *TelemetryConfig) (*ProbeScheduleConfig, error) {
	if policy == nil {
		return nil, nil
	}
	if probe == nil || window == nil || telemetry == nil {
		return nil, errors.New("dispatch probe schedule requires probes, health windows and live telemetry")
	}
	p := *policy
	if p.IdleIntervalSeconds == 0 {
		p.IdleIntervalSeconds = 30
	}
	if p.BusyIntervalSeconds == 0 {
		p.BusyIntervalSeconds = 300
	}
	if p.PassiveMinSamples == 0 {
		p.PassiveMinSamples = 20
	}
	if p.IdleIntervalSeconds < 30 || p.IdleIntervalSeconds > 3600 ||
		p.BusyIntervalSeconds < p.IdleIntervalSeconds || p.BusyIntervalSeconds > 3600 ||
		p.PassiveMinSamples < 20 || p.PassiveMinSamples > 100000 {
		return nil, errors.New("invalid dispatch probe schedule limits")
	}
	return &p, nil
}
