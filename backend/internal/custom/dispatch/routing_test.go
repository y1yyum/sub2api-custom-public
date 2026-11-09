package dispatch

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func routingTestConfig() Config {
	return Config{CoreInstance: "core", ConfigRevision: 1,
		Telemetry: &TelemetryConfig{SlotTTLSeconds: 1800, LoadMaxAgeSeconds: 5, MaxObservations: 100},
		Routing:   &RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}}
}

func TestDispatchRoutingConfigDefaultsBoundsAndLegacyDigest(t *testing.T) {
	base := Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, SnapshotFile: "snapshot.json",
		Targets: []Target{testTarget("b", 2), testTarget("a", 1)}, Telemetry: &TelemetryConfig{}, Routing: &RoutingConfig{}}
	cfg, err := normalizeConfig(base)
	require.NoError(t, err)
	require.Equal(t, &RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}, cfg.Routing)
	require.Zero(t, base.Routing.TTFTTargetMillis, "normalization must not mutate shared input")
	for name, change := range map[string]func(*Config){
		"telemetry":          func(c *Config) { c.Telemetry = nil },
		"negative_ttft":      func(c *Config) { c.Routing.TTFTTargetMillis = -1 },
		"large_ttft":         func(c *Config) { c.Routing.TTFTTargetMillis = 900001 },
		"negative_smoothing": func(c *Config) { c.Routing.WeightSmoothingSeconds = -1 },
		"large_smoothing":    func(c *Config) { c.Routing.WeightSmoothingSeconds = 301 },
	} {
		t.Run(name, func(t *testing.T) {
			c := cfg
			r := *c.Routing
			c.Routing = &r
			change(&c)
			_, err := normalizeConfig(c)
			require.Error(t, err)
		})
	}
	for _, limits := range []RoutingConfig{{1, 1}, {900000, 300}} {
		c := cfg
		c.Routing = &limits
		_, err := normalizeConfig(c)
		require.NoError(t, err)
	}
	legacy := cfg
	legacy.Routing = nil
	for _, telemetry := range []*TelemetryConfig{nil, cfg.Telemetry} {
		legacy.Telemetry = telemetry
		body, err := json.Marshal(struct {
			Targets   []Target
			Limits    ProbeLimits
			Telemetry *TelemetryConfig      `json:",omitempty"`
			Quality   *qualityConfigVersion `json:",omitempty"`
			Scheduled *ScheduledConfig      `json:",omitempty"`
		}{[]Target{legacy.Targets[1], legacy.Targets[0]}, legacy.ProbeLimits(), telemetry, nil, nil})
		require.NoError(t, err)
		digest, err := runtimeConfigDigest(legacy, legacy.ProbeLimits())
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(body)), digest)
		encoded, err := json.Marshal(legacy)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), `"routing"`)
	}
	digest, err := runtimeConfigDigest(cfg, cfg.ProbeLimits())
	require.NoError(t, err)
	legacyDigest, err := runtimeConfigDigest(legacy, legacy.ProbeLimits())
	require.NoError(t, err)
	require.NotEqual(t, legacyDigest, digest)
}

func TestDispatchRoutingTTFTTrustedEWMAAndUnknownCompatibility(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	target := testTarget("a", 1)
	h := InitialHealth(target, now)
	o := testObservation(target, now, Success)
	o.LatencyMillis, o.TTFTMillis = 9000, 1000
	require.NoError(t, h.Apply(target, o, now, false))
	require.Equal(t, int64(1000), h.TTFTMillis)
	require.Equal(t, now, h.TTFTObservedAt)
	o.At, o.TTFTMillis = now.Add(time.Second), 2000
	require.NoError(t, h.Apply(target, o, o.At, false))
	require.Equal(t, int64(1200), h.TTFTMillis)
	fresh := h.TTFTObservedAt
	o.At, o.TTFTMillis = now.Add(2*time.Second), 0
	require.NoError(t, h.Apply(target, o, o.At, false))
	require.Equal(t, int64(1200), h.TTFTMillis)
	require.Equal(t, fresh, h.TTFTObservedAt)
	o.Outcome, o.ProviderEvidence = Unknown, false
	require.NoError(t, h.Apply(target, o, o.At, false))
	require.Equal(t, fresh, h.TTFTObservedAt)
	for name, change := range map[string]func(*Observation){
		"negative":        func(o *Observation) { o.TTFTMillis = -1 },
		"large":           func(o *Observation) { o.TTFTMillis, o.LatencyMillis = 900001, 900000 },
		"exceeds_latency": func(o *Observation) { o.TTFTMillis = o.LatencyMillis + 1 },
		"failure":         func(o *Observation) { o.Outcome = UpstreamFailure },
		"unknown":         func(o *Observation) { o.Outcome = Unknown },
		"untrusted":       func(o *Observation) { o.ProviderEvidence = false },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := h
			o := testObservation(target, now.Add(3*time.Second), Success)
			o.LatencyMillis, o.TTFTMillis = 9000, 1000
			change(&o)
			require.Error(t, candidate.Apply(target, o, o.At, false))
			require.Equal(t, h, candidate)
		})
	}
	empty := InitialHealth(target, now)
	body, err := json.Marshal(empty)
	require.NoError(t, err)
	require.NotContains(t, string(body), "ttft")
	o = testObservation(target, now, Success)
	body, err = json.Marshal(o)
	require.NoError(t, err)
	require.NotContains(t, string(body), "ttft")
}

func TestDispatchRoutingFactorsHardVetoAndLegacyLatency(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	cfg := routingTestConfig()
	target := testTarget("a", 1)
	target.BaseWeight, target.MaxInflight = 10000, 100
	for _, tc := range []struct {
		name   string
		ttft   int64
		active int
		weight int64
	}{
		{"missing_neutral", 0, 0, 10000}, {"fast_no_gain", 500, 0, 10000},
		{"ttft_half", 2000, 0, 5000}, {"ttft_floor", 900000, 0, 2500},
		{"load_half", 0, 50, 5000}, {"load_floor", 0, 99, 500},
		{"composed", 2000, 50, 2500}, {"full", 0, 100, 0}, {"overfull", 0, 101, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := InitialHealth(target, now)
			h.LatencyMillis, h.TTFTMillis, h.TTFTObservedAt = 900000, tc.ttft, now
			c := h.routingCandidate(target, cfg, tc.active, now)
			require.Equal(t, tc.weight, c.EffectiveWeight)
			require.Equal(t, now, h.UpdatedAt)
			require.Equal(t, now, h.WindowAt)
		})
	}
	for name, change := range map[string]func(*Health, *Target){
		"disabled": func(_ *Health, t *Target) { t.Enabled = false },
		"auth":     func(h *Health, _ *Target) { h.AuthBlocked = true },
		"tripped":  func(h *Health, _ *Target) { h.State = Tripped },
		"probing":  func(h *Health, _ *Target) { h.State = Probing },
	} {
		t.Run(name, func(t *testing.T) {
			target := target
			h := InitialHealth(target, now)
			h.Routing = &routingWeightState{Key: routingKey(cfg, target), Factor: 1, UpdatedAt: now}
			change(&h, &target)
			c := h.routingCandidate(target, cfg, 0, now)
			require.Zero(t, c.EffectiveWeight)
			require.Zero(t, h.Routing.Factor)
			snapshot := Snapshot{Candidates: []Candidate{c}}
			applyQualityWeights(&snapshot, []QualityReport{{TargetRef: target.Ref, FactorMilli: 850}})
			require.Zero(t, snapshot.Candidates[0].EffectiveWeight)
		})
	}
	h := InitialHealth(target, now)
	h.LatencyMillis = 4000
	require.Equal(t, int64(2500), h.Candidate(target).EffectiveWeight)
	h.State, h.RecoveryStep = Degraded, 1
	c := h.routingCandidate(target, cfg, 0, now)
	require.Equal(t, int64(1000), c.EffectiveWeight)
	snapshot := Snapshot{Candidates: []Candidate{c}}
	applyQualityWeights(&snapshot, []QualityReport{{TargetRef: target.Ref, FactorMilli: 850}})
	require.Equal(t, int64(850), snapshot.Candidates[0].EffectiveWeight)
}

func TestDispatchRoutingSmoothingClockRestartAndPolicyFence(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	cfg := routingTestConfig()
	target := testTarget("a", 1)
	target.BaseWeight = 10000
	seed := InitialHealth(target, now)
	seed.TTFTMillis, seed.TTFTObservedAt = 4000, now
	seed.Routing = &routingWeightState{Key: routingKey(cfg, target), Factor: 1, UpdatedAt: now}
	once := seed
	once.routingCandidate(target, cfg, 0, now.Add(10*time.Second))
	frequent := seed
	for i := 1; i <= 10; i++ {
		frequent.routingCandidate(target, cfg, 0, now.Add(time.Duration(i)*time.Second))
	}
	require.InDelta(t, 0.25+0.75*math.Exp(-1), once.Routing.Factor, 1e-12)
	require.InDelta(t, once.Routing.Factor, frequent.Routing.Factor, 1e-12)
	body, err := json.Marshal(frequent)
	require.NoError(t, err)
	var restart Health
	require.NoError(t, json.Unmarshal(body, &restart))
	a := frequent.routingCandidate(target, cfg, 0, now.Add(20*time.Second))
	b := restart.routingCandidate(target, cfg, 0, now.Add(20*time.Second))
	require.Equal(t, a, b)
	require.Equal(t, frequent.Routing, restart.Routing)
	require.Equal(t, now, restart.UpdatedAt)
	require.Equal(t, now, restart.WindowAt)
	for name, change := range map[string]func(*Config, *Target){
		"revision": func(c *Config, _ *Target) { c.ConfigRevision++ },
		"policy": func(c *Config, _ *Target) {
			c.Routing = &RoutingConfig{TTFTTargetMillis: 2000, WeightSmoothingSeconds: 10}
		},
		"identity": func(_ *Config, target *Target) { target.CredentialRevision++ },
	} {
		t.Run(name, func(t *testing.T) {
			h, c, target := seed, cfg, target
			oldKey := h.Routing.Key
			change(&c, &target)
			h.routingCandidate(target, c, 0, now)
			require.NotEqual(t, oldKey, h.Routing.Key)
			require.Less(t, h.Routing.Factor, 1.0)
		})
	}
	require.Equal(t, 0.3, smoothRoutingFactor(0.3, 1, -time.Second, 10))
	require.Equal(t, 0.25, smoothRoutingFactor(1, 0.25, 24*time.Hour, 10))
	seed.Routing = &routingWeightState{Key: routingKey(cfg, target), Factor: 0, UpdatedAt: now.Add(time.Hour)}
	require.Zero(t, seed.routingCandidate(target, cfg, 0, now).EffectiveWeight)
	require.Equal(t, now, seed.Routing.UpdatedAt)
	seed.Routing.Factor = math.NaN()
	seed.routingCandidate(target, cfg, 0, now)
	require.Equal(t, 0.25, seed.Routing.Factor)
}

func TestDispatchRoutingTTFTFreshnessAndHardZeroRecovery(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	cfg := routingTestConfig()
	target := testTarget("a", 1)
	for _, tc := range []struct {
		at     time.Time
		weight int64
	}{
		{time.Time{}, 100}, {now.Add(-5 * time.Minute), 25},
		{now.Add(-5*time.Minute - time.Nanosecond), 100}, {now.Add(time.Second), 100},
	} {
		h := InitialHealth(target, now)
		h.TTFTMillis, h.TTFTObservedAt = 900000, tc.at
		require.Equal(t, tc.weight, h.routingCandidate(target, cfg, 0, now).EffectiveWeight)
	}
	h := InitialHealth(target, now)
	require.Equal(t, int64(100), h.routingCandidate(target, cfg, 0, now).EffectiveWeight)
	require.Zero(t, h.routingCandidate(target, cfg, target.MaxInflight, now).EffectiveWeight)
	require.Zero(t, h.routingCandidate(target, cfg, 0, now).EffectiveWeight)
	require.LessOrEqual(t, h.routingCandidate(target, cfg, 0, now.Add(time.Second)).EffectiveWeight, int64(10))
	h.State, h.RecoveryStep = Degraded, 1
	require.LessOrEqual(t, h.routingCandidate(target, cfg, 0, now.Add(time.Hour)).EffectiveWeight, int64(10))
}

func TestDispatchRoutingTTFTDoesNotReviveExpiredHistory(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		at   time.Time
		want int64
	}{
		{"missing", time.Time{}, 1000},
		{"expired", now.Add(-5*time.Minute - time.Nanosecond), 1000},
		{"future", now.Add(time.Second), 1000},
		{"fresh", now.Add(-time.Second), 720200},
		{"boundary", now.Add(-5 * time.Minute), 720200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := testTarget("a", 1)
			h := InitialHealth(target, now)
			h.LatencyMillis, h.TTFTMillis, h.TTFTObservedAt = 900000, 900000, tc.at
			o := testObservation(target, now, Success)
			o.LatencyMillis, o.TTFTMillis = 8000, 1000
			require.NoError(t, h.Apply(target, o, now, false))
			require.Equal(t, tc.want, h.TTFTMillis)
			require.Equal(t, now, h.TTFTObservedAt)
		})
	}
}
