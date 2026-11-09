package dispatch

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func protocolObservation(target Target, at time.Time, outcome Outcome, phase, fact string, usageMissing bool) Observation {
	o := testObservation(target, at, outcome)
	o.ProviderEvidence = outcome != Unknown && outcome != ClientCanceled
	o.Phase, o.ProtocolFact, o.UsageMissing = phase, fact, usageMissing
	return o
}

func TestDispatchProtocolFactsValidationMirrorsStorage(t *testing.T) {
	now := healthWindowStart()
	target := testTarget("a", 1)
	for _, tc := range []struct {
		name         string
		outcome      Outcome
		phase, fact  string
		usageMissing bool
		ttft         int64
		valid        bool
	}{
		{"legacy_empty", UpstreamFailure, "", "", false, 0, true},
		{"phase_only", UpstreamFailure, "headers", "", false, 0, true},
		{"semantic_failure", Unknown, "terminal", "semantic_failure", false, 0, true},
		{"incomplete", Unknown, "terminal", "incomplete", false, 0, true},
		{"non_text_success", Success, "terminal", "non_text_terminal", true, 0, true},
		{"non_text_unknown", Unknown, "terminal", "non_text_terminal", false, 0, true},
		{"stream_break_headers", Unknown, "headers", "stream_break", false, 0, true},
		{"stream_break_content", Unknown, "content", "stream_break", false, 0, true},
		{"local_timeout_connect", Unknown, "connect", "local_timeout", false, 0, true},
		{"bridge_deadline_unphased", ClientCanceled, "", "bridge_deadline", false, 0, true},
		{"usage_missing_success", Success, "terminal", "", true, 10, true},
		{"unknown_phase", Unknown, "body", "", false, 0, false},
		{"unknown_fact", Unknown, "terminal", "provider_error", false, 0, false},
		{"semantic_failure_as_failure", UpstreamFailure, "terminal", "semantic_failure", false, 0, false},
		{"semantic_failure_as_success", Success, "terminal", "semantic_failure", false, 0, false},
		{"semantic_failure_wrong_phase", Unknown, "content", "semantic_failure", false, 0, false},
		{"non_text_with_ttft", Success, "terminal", "non_text_terminal", false, 10, false},
		{"stream_break_terminal", Unknown, "terminal", "stream_break", false, 0, false},
		{"stream_break_as_failure", UpstreamFailure, "content", "stream_break", false, 0, false},
		{"local_timeout_as_failure", UpstreamFailure, "connect", "local_timeout", false, 0, false},
		{"local_timeout_terminal", Unknown, "terminal", "local_timeout", false, 0, false},
		{"bridge_deadline_as_unknown", Unknown, "connect", "bridge_deadline", false, 0, false},
		{"usage_missing_without_terminal", Success, "content", "", true, 0, false},
		{"usage_missing_on_failure_fact", Unknown, "terminal", "semantic_failure", true, 0, false},
		{"usage_missing_as_failure", UpstreamFailure, "terminal", "", true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := protocolObservation(target, now, tc.outcome, tc.phase, tc.fact, tc.usageMissing)
			o.TTFTMillis, o.LatencyMillis = tc.ttft, tc.ttft
			require.Equal(t, tc.valid, validateObservation(o, target, now) == nil)
		})
	}
	require.Equal(t, []string{"stream_break.content"}, protocolFactKeys(Observation{Phase: "content", ProtocolFact: "stream_break"}))
	require.Equal(t, []string{"bridge_deadline"}, protocolFactKeys(Observation{ProtocolFact: "bridge_deadline"}))
	require.Equal(t, []string{"non_text_terminal", "usage_missing"}, protocolFactKeys(Observation{Phase: "terminal", ProtocolFact: "non_text_terminal", UsageMissing: true}))
	require.Empty(t, protocolFactKeys(Observation{Phase: "headers"}))
}

func stripProtocolFacts(o Observation) Observation {
	o.Phase, o.ProtocolFact, o.UsageMissing = "", "", false
	return o
}

func TestDispatchProtocolFactsAreReportOnly(t *testing.T) {
	start := healthWindowStart()
	target := testTarget("a", 1)
	var observations []Observation
	for i := range 40 {
		at := start.Add(time.Duration(i) * time.Second)
		var o Observation
		switch i % 8 {
		case 0:
			o = protocolObservation(target, at, Unknown, "content", "stream_break", false)
		case 1:
			o = protocolObservation(target, at, Unknown, "terminal", "semantic_failure", false)
		case 2:
			o = protocolObservation(target, at, Success, "terminal", "non_text_terminal", true)
		case 3:
			o = protocolObservation(target, at, Unknown, "connect", "local_timeout", false)
		case 4:
			o = protocolObservation(target, at, ClientCanceled, "headers", "bridge_deadline", false)
		case 5:
			o = protocolObservation(target, at, UpstreamFailure, "headers", "", false)
		default:
			o = protocolObservation(target, at, Success, "terminal", "", true)
			o.TTFTMillis, o.LatencyMillis = 200, 400
		}
		o.EventID = "event-" + at.Format("150405")
		observations = append(observations, o)
	}
	run := func(policy *HealthWindowConfig, strip, probe bool) Health {
		h := InitialHealth(target, start)
		for _, o := range observations {
			if strip {
				o = stripProtocolFacts(o)
			}
			require.NoError(t, h.ApplyWithWindow(target, o, o.At, probe && o.Outcome != Unknown && o.Outcome != ClientCanceled, !probe, policy, 1000))
		}
		h.AdvanceHealthWindow(start.Add(90*time.Second), policy, 1000)
		return h
	}
	enabled := &HealthWindowConfig{MinSamples: 20, ProtocolFacts: true}
	legacy := &HealthWindowConfig{MinSamples: 20}
	counted := run(enabled, false, false)
	baseline := run(legacy, true, false)
	report := counted.HealthWindowReport(start.Add(40*time.Second), *enabled, 1000)
	require.Equal(t, map[string]int{
		"stream_break.content": 5, "semantic_failure": 5, "non_text_terminal": 5, "local_timeout.connect": 5,
		"bridge_deadline.headers": 5, "usage_missing": 15,
	}, report.Baseline.Facts)
	want := baseline.HealthWindowReport(start.Add(40*time.Second), *legacy, 1000)
	report.Hot.Facts, report.Baseline.Facts = nil, nil
	require.Equal(t, want, report, "facts never change samples, failures, timeouts or factor")
	for i := range counted.Rolling.Buckets {
		counted.Rolling.Buckets[i].Facts = nil
	}
	countedJSON, err := json.Marshal(counted)
	require.NoError(t, err)
	baselineJSON, err := json.Marshal(baseline)
	require.NoError(t, err)
	require.Equal(t, string(baselineJSON), string(countedJSON), "health state is identical apart from report counts")

	off := run(legacy, false, false)
	offJSON, err := json.Marshal(off)
	require.NoError(t, err)
	require.Equal(t, string(baselineJSON), string(offJSON), "disabled policy never counts facts")
	require.NotContains(t, string(offJSON), "facts")

	probed := run(enabled, false, true)
	for _, bucket := range probed.Rolling.Buckets {
		require.Nil(t, bucket.Facts, "probe observations are not passive facts")
	}

	legacyHealth, factHealth := InitialHealth(target, start), InitialHealth(target, start)
	for _, o := range observations {
		require.NoError(t, legacyHealth.ApplyWithWindow(target, stripProtocolFacts(o), o.At, false, true, nil, 0))
		require.NoError(t, factHealth.ApplyWithWindow(target, o, o.At, false, true, nil, 0))
	}
	legacyJSON, err := json.Marshal(legacyHealth)
	require.NoError(t, err)
	factJSON, err := json.Marshal(factHealth)
	require.NoError(t, err)
	require.Equal(t, string(legacyJSON), string(factJSON), "nil health policy keeps legacy health bytes")
}

func TestDispatchProtocolFactsObservationJSONOmitsDefaults(t *testing.T) {
	o := testObservation(testTarget("a", 1), healthWindowStart(), Success)
	raw, err := json.Marshal(o)
	require.NoError(t, err)
	for _, field := range []string{"phase", "protocol_fact", "usage_missing"} {
		require.NotContains(t, string(raw), field)
	}
	o.Phase, o.UsageMissing = "terminal", true
	raw, err = json.Marshal(o)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"phase":"terminal"`)
	require.Contains(t, string(raw), `"usage_missing":true`)
}

func TestDispatchProtocolFactsConfigIsExplicitAndDigested(t *testing.T) {
	routing, telemetry := &RoutingConfig{}, &TelemetryConfig{}
	policy, err := normalizeHealthWindowConfig(&HealthWindowConfig{ProtocolFacts: true}, routing, telemetry)
	require.NoError(t, err)
	require.True(t, policy.ProtocolFacts)
	body, err := json.Marshal(policy)
	require.NoError(t, err)
	require.JSONEq(t, `{"min_samples":20,"protocol_facts":true}`, string(body))

	var decoded HealthWindowConfig
	require.NoError(t, decodeJSON([]byte(`{"min_samples":20,"protocol_facts":true}`), &decoded))
	require.True(t, decoded.ProtocolFacts)
	for _, raw := range []string{
		`{"min_samples":20,"Protocol_Facts":true}`,
		`{"min_samples":20,"protocolFacts":true}`,
		`{"min_samples":20,"protocol_facts":true,"protocol_facts":true}`,
		`{"min_samples":20,"protocol_facts":"true"}`,
		`{"min_samples":20,"protocol_fact":true}`,
	} {
		require.Error(t, decodeJSON([]byte(raw), new(HealthWindowConfig)), raw)
	}

	cfg, err := normalizeConfig(probeBudgetTestConfig())
	require.NoError(t, err)
	cfg.Telemetry, cfg.Routing = &TelemetryConfig{}, &RoutingConfig{}
	cfg.HealthWindow = &HealthWindowConfig{MinSamples: 20}
	off, err := runtimeConfigDigest(cfg, cfg.ProbeLimits())
	require.NoError(t, err)
	explicitFalse := cfg
	explicitFalse.HealthWindow = &HealthWindowConfig{MinSamples: 20, ProtocolFacts: false}
	same, err := runtimeConfigDigest(explicitFalse, cfg.ProbeLimits())
	require.NoError(t, err)
	require.Equal(t, off, same)
	on := cfg
	on.HealthWindow = &HealthWindowConfig{MinSamples: 20, ProtocolFacts: true}
	changed, err := runtimeConfigDigest(on, cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotEqual(t, off, changed, "enabling protocol facts requires a new config revision")
}
