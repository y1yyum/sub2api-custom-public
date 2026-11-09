package dispatch

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDispatchProbeBudgetDeepCopyAndCanonicalOrder(t *testing.T) {
	cfg := probeBudgetTestConfig()
	price := cfg.ProbeBudget.Prices[0]
	for _, pair := range [][2]string{{"z", "a"}, {"a", "z"}, {"a", "a"}, {"provider:a", "b"}, {"provider", "a:b"}} {
		extra := price
		extra.ProviderRef, extra.Model = pair[0], pair[1]
		cfg.ProbeBudget.Prices = append(cfg.ProbeBudget.Prices, extra)
	}
	before := *cfg.ProbeBudget
	before.Prices = slices.Clone(before.Prices)
	got, err := normalizeConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, &before, cfg.ProbeBudget, "sorting must not mutate shared input")
	require.NotSame(t, cfg.ProbeBudget, got.ProbeBudget)
	var pairs [][2]string
	for _, bound := range got.ProbeBudget.Prices {
		pairs = append(pairs, [2]string{bound.ProviderRef, bound.Model})
	}
	require.Equal(t, [][2]string{{"a", "a"}, {"a", "z"}, {"provider", "a:b"}, {"provider", "gpt-fixture"}, {"provider:a", "b"}, {"z", "a"}}, pairs)
	reverse := before
	reverse.Prices = slices.Clone(before.Prices)
	slices.Reverse(reverse.Prices)
	cfg.ProbeBudget = &reverse
	other, err := normalizeConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, got.ProbeBudget, other.ProbeBudget)
	firstDigest, err := runtimeConfigDigest(got, got.ProbeLimits())
	require.NoError(t, err)
	secondDigest, err := runtimeConfigDigest(other, other.ProbeLimits())
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)
	got.ProbeBudget.Currency = "EUR"
	got.ProbeBudget.Prices[0].Revision = "changed"
	require.Equal(t, "USD", other.ProbeBudget.Currency)
	require.Equal(t, "bound-v1", other.ProbeBudget.Prices[0].Revision, "separate normalized copies cannot share price storage")
	require.Equal(t, "bound-v1", before.Prices[0].Revision)
}

func TestDispatchProbeBudgetNilPreservesLegacyJSONAndDigest(t *testing.T) {
	// 固定预算字段加入前的声明顺序；不能通过删除新字段来构造期望值。
	type legacyConfig struct {
		Version                  int                  `json:"version"`
		CoreInstance             string               `json:"core_instance"`
		ConfigRevision           int64                `json:"config_revision"`
		SnapshotFile             string               `json:"snapshot_file"`
		SnapshotTTLSeconds       int                  `json:"snapshot_ttl_seconds"`
		PollIntervalMilliseconds int                  `json:"poll_interval_milliseconds"`
		Targets                  []Target             `json:"targets"`
		Probe                    *ProbeConfig         `json:"probe,omitempty"`
		Telemetry                *TelemetryConfig     `json:"telemetry,omitempty"`
		Quality                  *QualityConfig       `json:"quality,omitempty"`
		Scheduled                *ScheduledConfig     `json:"scheduled,omitempty"`
		Routing                  *RoutingConfig       `json:"routing,omitempty"`
		HealthWindow             *HealthWindowConfig  `json:"health_window,omitempty"`
		ProbeSchedule            *ProbeScheduleConfig `json:"probe_schedule,omitempty"`
	}
	cfg := probeBudgetTestConfig()
	cfg.ProbeBudget = nil
	cfg.Targets = append(cfg.Targets, testTarget("b", 2))
	slices.Reverse(cfg.Targets)
	for _, controls := range []bool{false, true} {
		t.Run(fmt.Sprintf("controls_%t", controls), func(t *testing.T) {
			c := cfg
			if controls {
				c.Telemetry, c.Quality, c.Scheduled = &TelemetryConfig{}, &QualityConfig{}, &ScheduledConfig{}
				c.Routing, c.HealthWindow, c.ProbeSchedule = &RoutingConfig{}, &HealthWindowConfig{}, &ProbeScheduleConfig{}
			} else {
				c.Probe = nil
			}
			old := legacyConfig{c.Version, c.CoreInstance, c.ConfigRevision, c.SnapshotFile, c.SnapshotTTLSeconds,
				c.PollIntervalMilliseconds, c.Targets, c.Probe, c.Telemetry, c.Quality, c.Scheduled, c.Routing, c.HealthWindow, c.ProbeSchedule}
			oldJSON, err := json.Marshal(old)
			require.NoError(t, err)
			newJSON, err := json.Marshal(c)
			require.NoError(t, err)
			require.Equal(t, string(oldJSON), string(newJSON))
			oldDigestBody, err := json.Marshal(struct {
				Targets       []Target
				Limits        ProbeLimits
				Telemetry     *TelemetryConfig      `json:",omitempty"`
				Quality       *qualityConfigVersion `json:",omitempty"`
				Scheduled     *ScheduledConfig      `json:",omitempty"`
				Routing       *RoutingConfig        `json:",omitempty"`
				HealthWindow  *HealthWindowConfig   `json:",omitempty"`
				ProbeSchedule *ProbeScheduleConfig  `json:",omitempty"`
			}{[]Target{c.Targets[1], c.Targets[0]}, c.ProbeLimits(), c.Telemetry, c.Quality.version(), c.Scheduled, c.Routing, c.HealthWindow, c.ProbeSchedule})
			require.NoError(t, err)
			digest, err := runtimeConfigDigest(c, c.ProbeLimits())
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(oldDigestBody)), digest)
			oldPolicyJSON, err := json.Marshal(struct {
				ProbeLimits
				Routing       *RoutingConfig       `json:"routing,omitempty"`
				HealthWindow  *HealthWindowConfig  `json:"health_window,omitempty"`
				ProbeSchedule *ProbeScheduleConfig `json:"probe_schedule,omitempty"`
			}{c.ProbeLimits(), c.Routing, c.HealthWindow, c.ProbeSchedule})
			require.NoError(t, err)
			newPolicyJSON, err := json.Marshal(persistedProbePolicy{
				ProbeLimits: c.ProbeLimits(), Routing: c.Routing, HealthWindow: c.HealthWindow, ProbeSchedule: c.ProbeSchedule,
			})
			require.NoError(t, err)
			require.Equal(t, string(oldPolicyJSON), string(newPolicyJSON))
			var restored persistedProbePolicy
			require.NoError(t, json.Unmarshal(oldPolicyJSON, &restored))
			require.Nil(t, restored.ProbeBudget)
		})
	}
}

func TestDispatchProbeBudgetSameRevisionDigestBindsAllFields(t *testing.T) {
	cfg, err := normalizeConfig(probeBudgetTestConfig())
	require.NoError(t, err)
	digest, err := runtimeConfigDigest(cfg, cfg.ProbeLimits())
	require.NoError(t, err)
	for name, change := range map[string]func(*ProbeBudgetConfig){
		"currency":                       func(b *ProbeBudgetConfig) { b.Currency = "EUR" },
		"daily":                          func(b *ProbeBudgetConfig) { b.DailyMicrousd++ },
		"provider":                       func(b *ProbeBudgetConfig) { b.ProviderDailyMicrousd++ },
		"activation":                     func(b *ProbeBudgetConfig) { b.ActivationEvidenceRef = "another-drain" },
		"price_provider":                 func(b *ProbeBudgetConfig) { b.Prices[0].ProviderRef = "another-provider" },
		"model":                          func(b *ProbeBudgetConfig) { b.Prices[0].Model = "another-model" },
		"revision":                       func(b *ProbeBudgetConfig) { b.Prices[0].Revision = "bound-v2" },
		"evidence":                       func(b *ProbeBudgetConfig) { b.Prices[0].EvidenceRef = "another-evidence" },
		"same_price_revision_new_amount": func(b *ProbeBudgetConfig) { b.Prices[0].MaxMicrousd++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := cfg
			budget := *cfg.ProbeBudget
			budget.Prices = slices.Clone(budget.Prices)
			change(&budget)
			changed.ProbeBudget = &budget
			require.Equal(t, cfg.ConfigRevision, changed.ConfigRevision)
			require.Equal(t, cfg.ProbeLimits(), changed.ProbeLimits())
			got, err := runtimeConfigDigest(changed, changed.ProbeLimits())
			require.NoError(t, err)
			require.NotEqual(t, digest, got, "same-version changes must reach the persisted configuration fence")
		})
	}
	without := cfg
	without.ProbeBudget = nil
	legacy, err := runtimeConfigDigest(without, without.ProbeLimits())
	require.NoError(t, err)
	require.NotEqual(t, digest, legacy)
	policy := persistedProbePolicy{ProbeLimits: cfg.ProbeLimits(), ProbeBudget: cfg.ProbeBudget}
	body, err := json.Marshal(policy)
	require.NoError(t, err)
	require.Contains(t, string(body), `"probe_budget":`)
	var restored persistedProbePolicy
	require.NoError(t, json.Unmarshal(body, &restored))
	require.Equal(t, policy, restored)
}
