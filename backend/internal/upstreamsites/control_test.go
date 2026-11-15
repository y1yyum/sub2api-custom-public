package upstreamsites

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// control_test.go verifies wallet hysteresis, source isolation, and unknown evidence.
// Tests use explicit clocks and round identities so policy transitions do not
// depend on wall time, networking, provider manual switches, or worker scheduling.

// TestDelayedPollingStillSuspends records consecutive failures separated by worker capacity delays.
// Old successes cannot recover the site, but queueing must not erase every failure candidate indefinitely.
func TestDelayedPollingStillSuspends(t *testing.T) {
	c := DefaultConfig()
	c.Policy.AutoRecover = true
	c.Policy.Mode = "probe"
	c.Policy.MinSuspendSeconds = 0
	c.Probes = []Probe{{ID: "health", Required: true, IntervalMinutes: 1}}
	at := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	state := State{}
	for i := range 3 {
		state = Evaluate(c, state, Observation{RunID: fmt.Sprint(i), Source: "health", At: at.Add(time.Duration(i) * 5 * time.Minute), Outcome: Unreachable})
	}
	require.True(t, state.Blocked())
	for i := range 2 {
		state = Evaluate(c, state, Observation{RunID: fmt.Sprint(i + 3), Source: "health", At: at.Add(time.Duration(i+3) * 5 * time.Minute), Outcome: Healthy})
	}
	require.True(t, state.Blocked())
	state = Evaluate(c, state, Observation{RunID: "fresh", Source: "health", At: at.Add(21 * time.Minute), Outcome: Healthy})
	require.False(t, state.Blocked())
}

// TestRecoveryRequiresSelectedCurrency restores connectivity with an unrelated wallet denomination.
// Endpoint health alone cannot reopen the site until its configured currency supplies sufficient recovery evidence.
func TestRecoveryRequiresSelectedCurrency(t *testing.T) {
	c := DefaultConfig()
	c.Policy.AutoRecover = true
	c.Policy.Mode = "balance"
	c.Policy.MinSuspendSeconds = 0
	c.Policy.FailureCount = 1
	c.Policy.Thresholds = []Threshold{{Unit: "USD", Low: "10", Recover: "15"}}
	at := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	s := Evaluate(c, State{}, Observation{RunID: "outage", Source: "balance", At: at, Outcome: Unreachable})
	for i, unit := range []string{"CNY", "CNY", "USD", "USD"} {
		o := Observation{RunID: fmt.Sprint(i), Source: "balance", At: at.Add(time.Duration(i+1) * time.Minute), Outcome: Healthy, Balances: []Balance{{Unit: unit, Available: "20"}}}
		s = Evaluate(c, s, o)
		require.Equal(t, i < 3, s.Blocked())
	}
}

// TestEvaluateCombinedIsolation models depletion followed by probe and wallet recovery.
// It protects independent reasons and keeps manual suspension after automatic recovery succeeds.
func TestEvaluateCombinedIsolation(t *testing.T) {
	c := DefaultConfig()
	c.Policy.AutoRecover = true
	c.Policy.Mode = "combined"
	c.Policy.MinSuspendSeconds = 0
	c.Policy.Thresholds = []Threshold{{Unit: "USD", Low: "10", Recover: "15"}}
	c.Probes = []Probe{{ID: "models", Required: true, IntervalMinutes: 5}}
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	s := State{Manual: true}
	observe := func(source, outcome, amount string) {
		now = now.Add(time.Minute)
		s = Evaluate(c, s, Observation{RunID: fmt.Sprint(now.Unix()), Source: source, At: now, Outcome: outcome, Balances: []Balance{{Unit: "USD", Available: amount}}})
	}
	observe("balance", Healthy, "9")
	require.Contains(t, s.Reasons, "balance_low")
	for range 3 {
		observe("models", Unreachable, "")
	}
	require.Len(t, s.Reasons, 2)
	for range 2 {
		observe("models", Healthy, "")
	}
	require.Contains(t, s.Reasons, "balance_low")
	require.Len(t, s.Reasons, 1)
	observe("balance", Healthy, "15")
	observe("balance", Authentication, "")
	observe("balance", Healthy, "15")
	require.Contains(t, s.Reasons, "balance_low")
	observe("balance", Healthy, "15")
	require.Empty(t, s.Reasons)
	require.True(t, s.Blocked())
}

func TestManualSuspensionDoesNotBypassAutomaticRecoveryEvidence(t *testing.T) {
	c := DefaultConfig()
	c.Policy.Mode = "combined"
	c.Policy.AutoRecover = true
	c.Policy.MinSuspendSeconds = 0
	c.Policy.RecoveryCount = 1
	c.Policy.Thresholds = []Threshold{{Unit: "USD", Low: "1", Recover: "5"}}
	c.Probes = []Probe{{ID: "health", Required: true, IntervalMinutes: 5}}
	now := time.Now().UTC()
	state := State{Manual: true, Reasons: map[string]time.Time{"balance_low": now.Add(-time.Hour)}}
	state = Evaluate(c, state, Observation{RunID: "wallet", Source: "balance", At: now, Outcome: Healthy, Balances: []Balance{{Unit: "USD", Available: "10"}}})
	require.Contains(t, state.Reasons, "balance_low", "人工禁用时仍需完整的自动恢复证据")
	state = Evaluate(c, state, Observation{RunID: "probe", Source: "health", At: now.Add(time.Minute), Outcome: Healthy})
	state = Evaluate(c, state, Observation{RunID: "wallet-next", Source: "balance", At: now.Add(2 * time.Minute), Outcome: Healthy, Balances: []Balance{{Unit: "USD", Available: "10"}}})
	require.Empty(t, state.Reasons)
	require.True(t, state.Manual)
	require.True(t, state.Blocked())
}

// TestEvaluateFailureAndRoundBoundaries exercises repeated clicks and interrupted failure runs.
// Authentication errors must neither suspend the site nor supply evidence for recovery.
func TestEvaluateFailureAndRoundBoundaries(t *testing.T) {
	c := DefaultConfig()
	c.Policy.AutoRecover = true
	c.Policy.Mode = "balance"
	c.Policy.Thresholds = []Threshold{{Unit: "USD", Low: "0", Recover: "0"}}
	at := time.Now().UTC()
	s := State{}
	for i, outcome := range []string{Unreachable, Authentication, Unreachable, Unreachable} {
		s = Evaluate(c, s, Observation{RunID: fmt.Sprint(i), Source: "balance", At: at.Add(time.Duration(i) * time.Minute), Outcome: outcome})
	}
	require.False(t, s.Blocked())
	o := Observation{RunID: "last", Source: "balance", At: at.Add(4 * time.Minute), Outcome: Unreachable}
	s = Evaluate(c, s, o)
	require.True(t, s.Blocked())
	version := s.Version
	s = Evaluate(c, s, o)
	require.Equal(t, version, s.Version)
	o.RunID = "clicked-again"
	o.At = o.At.Add(time.Second)
	o.Outcome = Healthy
	o.Balances = []Balance{{Unit: "USD", Available: "99"}}
	s = Evaluate(c, s, o)
	require.True(t, s.Blocked())
	require.Equal(t, 1, s.Counters["balance"].Recovered)
	version = s.Version
	o.RunID = "another-rapid-click"
	o.At = o.At.Add(time.Second)
	s = Evaluate(c, s, o)
	require.Equal(t, version, s.Version)
}

// TestRapidConditionChangesAreNotDiscarded observes depletion immediately after a healthy wallet.
// Throttling must not delay the first low balance or hide an intervening authentication failure during recovery.
func TestRapidConditionChangesAreNotDiscarded(t *testing.T) {
	c := DefaultConfig()
	c.Policy.AutoRecover = true
	c.Policy.Mode = "balance"
	c.Policy.MinSuspendSeconds = 0
	c.Policy.Thresholds = []Threshold{{Unit: "USD", Low: "10", Recover: "15"}}
	at := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	state := State{}
	for i, entry := range []struct {
		seconds         int
		outcome, amount string
		blocked         bool
	}{
		{0, Healthy, "100", false}, {1, Healthy, "0", true}, {2, Healthy, "100", true}, {3, Authentication, "", true}, {4, Healthy, "100", true}, {5, Healthy, "100", true}, {35, Healthy, "100", false},
	} {
		state = Evaluate(c, state, Observation{RunID: fmt.Sprint(i), Source: "balance", At: at.Add(time.Duration(entry.seconds) * time.Second), Outcome: entry.outcome, Balances: []Balance{{Unit: "USD", Available: entry.amount}}})
		require.Equal(t, entry.blocked, state.Blocked(), "observation %d", i)
	}
}

// TestBalanceDecisionCurrencies keeps incomplete currency observations distinct from zero balances.
// It also checks strict suspension boundaries and inclusive recovery at equal thresholds.
func TestBalanceDecisionCurrencies(t *testing.T) {
	p := Policy{BalanceMatch: "any", Thresholds: []Threshold{{Unit: "USD", Low: "10", Recover: "10"}, {Unit: "CNY", Low: "20", Recover: "25"}}}
	low, recovered := balanceDecision(p, []Balance{{Unit: "USD", Available: "10"}})
	require.False(t, low)
	require.False(t, recovered)
	low, recovered = balanceDecision(p, []Balance{{Unit: "USD", Available: "9"}})
	require.True(t, low)
	require.False(t, recovered)
	p.BalanceMatch = "all"
	low, recovered = balanceDecision(p, []Balance{{Unit: "USD", Available: "10"}})
	require.False(t, low)
	require.True(t, recovered)
}

// TestReconfigureRetainsEnabledReasons resets candidates after a credential or policy edit.
// Disabling one source releases its reasons while keeping manual and unrelated restrictions.
func TestReconfigureRetainsEnabledReasons(t *testing.T) {
	c := DefaultConfig()
	c.Policy.AutoRecover = true
	c.Policy.Mode = "balance"
	s := State{Manual: true, Reasons: map[string]time.Time{"balance_low": time.Now(), "probe_unreachable:p": time.Now()}, Counters: map[string]Counter{"balance": {Recovered: 2}}}
	s = Reconfigure(c, s)
	require.True(t, s.Manual)
	require.Contains(t, s.Reasons, "balance_low")
	require.Len(t, s.Reasons, 1)
	require.Empty(t, s.Counters)
}
