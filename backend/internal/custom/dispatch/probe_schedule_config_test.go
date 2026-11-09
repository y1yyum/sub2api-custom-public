package dispatch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDispatchProbeScheduleConfigDefaultsAndBounds(t *testing.T) {
	probe, window, telemetry := &ProbeConfig{}, &HealthWindowConfig{}, &TelemetryConfig{}
	policy, err := normalizeProbeScheduleConfig(nil, nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, policy)
	input := &ProbeScheduleConfig{}
	policy, err = normalizeProbeScheduleConfig(input, probe, window, telemetry)
	require.NoError(t, err)
	require.Equal(t, &ProbeScheduleConfig{30, 300, 20}, policy)
	require.Equal(t, &ProbeScheduleConfig{}, input, "normalization must not change shared configuration")
	for _, bounds := range []ProbeScheduleConfig{{30, 30, 20}, {3600, 3600, 100000}} {
		normalized, err := normalizeProbeScheduleConfig(&bounds, probe, window, telemetry)
		require.NoError(t, err)
		require.Equal(t, &bounds, normalized)
	}
	for name, limits := range map[string]ProbeScheduleConfig{
		"idle_negative": {-1, 300, 20}, "idle_short": {29, 300, 20}, "idle_long": {3601, 3601, 20},
		"busy_negative": {30, -1, 20}, "busy_short": {60, 59, 20}, "busy_long": {30, 3601, 20},
		"samples_negative": {30, 300, -1}, "samples_few": {30, 300, 19}, "samples_many": {30, 300, 100001},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeProbeScheduleConfig(&limits, probe, window, telemetry)
			require.Error(t, err)
		})
	}
	for name, deps := range map[string]struct {
		probe     *ProbeConfig
		window    *HealthWindowConfig
		telemetry *TelemetryConfig
	}{
		"probe": {nil, window, telemetry}, "window": {probe, nil, telemetry}, "telemetry": {probe, window, nil},
	} {
		t.Run("missing_"+name, func(t *testing.T) {
			_, err := normalizeProbeScheduleConfig(input, deps.probe, deps.window, deps.telemetry)
			require.Error(t, err)
		})
	}
}

func TestDispatchProbeScheduleConfigJSONContract(t *testing.T) {
	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"probe_schedule":{"idle_interval_seconds":60,"busy_interval_seconds":600,"passive_min_samples":40}}`), &cfg))
	require.Equal(t, &ProbeScheduleConfig{60, 600, 40}, cfg.ProbeSchedule)
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.Contains(t, string(body), `"probe_schedule":{"idle_interval_seconds":60,"busy_interval_seconds":600,"passive_min_samples":40}`)
	cfg.ProbeSchedule = nil
	body, err = json.Marshal(cfg)
	require.NoError(t, err)
	require.NotContains(t, string(body), `"probe_schedule"`)
}
