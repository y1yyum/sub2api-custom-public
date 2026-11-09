package dispatch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDispatchHealthWindowConfig(t *testing.T) {
	policy, err := normalizeHealthWindowConfig(nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, policy)
	routing, telemetry := &RoutingConfig{}, &TelemetryConfig{}
	original := HealthWindowConfig{}
	policy, err = normalizeHealthWindowConfig(&original, routing, telemetry)
	require.NoError(t, err)
	require.Equal(t, 20, policy.MinSamples)
	require.Zero(t, original.MinSamples, "normalization must not mutate caller state")
	for _, minimum := range []int{20, 100000} {
		policy, err := normalizeHealthWindowConfig(&HealthWindowConfig{MinSamples: minimum}, routing, telemetry)
		require.NoError(t, err)
		require.Equal(t, minimum, policy.MinSamples)
	}
	for _, minimum := range []int{-1, 1, 19, 100001} {
		_, err := normalizeHealthWindowConfig(&HealthWindowConfig{MinSamples: minimum}, routing, telemetry)
		require.Error(t, err)
	}
	_, err = normalizeHealthWindowConfig(&original, nil, telemetry)
	require.Error(t, err)
	_, err = normalizeHealthWindowConfig(&original, routing, nil)
	require.Error(t, err)
	body, err := json.Marshal(policy)
	require.NoError(t, err)
	require.JSONEq(t, `{"min_samples":20}`, string(body))
}
