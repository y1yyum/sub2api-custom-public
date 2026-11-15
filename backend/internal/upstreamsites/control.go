package upstreamsites

import (
	"maps"
	"strings"
	"time"
)

// control.go evaluates durable restrictions from independent monitoring sources.
// A healthy probe only clears its own outage reason; wallet recovery must also
// satisfy its configured hysteresis. Unknown results reset candidate counters
// while preserving established restrictions, because authentication or parser
// failures cannot establish either a depleted wallet or a recovered endpoint.
//
// Repeated evidence counts at most once per thirty seconds, including manual
// requests; changed conditions still reset the opposite streak immediately.
// Recovery requires enough recent successes and a minimum suspension
// duration. No expiration timer removes reasons, and the manual restriction
// remains independent from every automatic transition and configuration reset.

// Evaluate derives a copied state from one version-accepted observation.
// Repository completion must atomically persist this state with that observation;
// callers must serialize observations for the site before evaluating the result.
// Each source owns its counters and reasons, so asynchronous healthy responses
// from another probe cannot erase a low balance or a different outage. Unknown
// evidence interrupts consecutive runs without changing the established reasons.
func Evaluate(config Config, previous State, observation Observation) State {
	state := previous
	state.Reasons = maps.Clone(previous.Reasons)
	state.Counters = maps.Clone(previous.Counters)
	if state.Reasons == nil {
		state.Reasons = map[string]time.Time{}
	}
	if state.Counters == nil {
		state.Counters = map[string]Counter{}
	}
	source := observation.Source
	counter := state.Counters[source]
	if counter.LastRun == observation.RunID || (!counter.LastCounted.IsZero() && observation.At.Sub(counter.LastCounted) < 30*time.Second && sameEvidence(config, counter, observation)) {
		return state
	}
	interval, required := sourceInterval(config, source)
	if interval == 0 {
		return state
	}
	if observation.At.Sub(counter.LastCounted) > time.Duration(2*interval)*time.Minute {
		// Delayed polling invalidates recovery evidence, but completed consecutive
		// failures still count; a busy worker must not permanently prevent suspension.
		counter.Successes = 0
		counter.Recovered = 0
		counter.LastHealthy = time.Time{}
	}
	counter.LastRun = observation.RunID
	counter.LastCounted = observation.At
	switch observation.Outcome {
	case Healthy:
		counter.Failures = 0
		counter.Successes++
		counter.LastHealthy = observation.At
	case Unreachable, ProbeUnhealthy:
		counter.Failures++
		counter.Successes = 0
		counter.Low = 0
		counter.Recovered = 0
	default:
		counter.Failures = 0
		counter.Successes = 0
		counter.Low = 0
		counter.Recovered = 0
	}
	p := config.Policy
	if required && p.Mode != "off" {
		reason := "probe_unreachable:" + source
		if source == "balance" {
			reason = "balance_endpoint_unreachable"
		}
		if counter.Failures >= p.FailureCount {
			addReason(&state, reason, observation.At)
		}
		if observation.Outcome == Healthy && counter.Successes >= p.RecoveryCount {
			clearReason(&state, reason, p, observation.At)
		}
	}
	if source == "balance" && config.balanceControl() && observation.Outcome == Healthy {
		low, recovered := balanceDecision(p, observation.Balances)
		if low {
			counter.Low++
		} else {
			counter.Low = 0
		}
		if recovered {
			counter.Recovered++
		} else {
			counter.Recovered = 0
		}
		if counter.Low >= p.LowCount {
			addReason(&state, "balance_low", observation.At)
		}
		if counter.Recovered >= p.RecoveryCount {
			clearReason(&state, "balance_low", p, observation.At)
		}
	}
	state.Counters[source] = counter
	if len(previous.Reasons) > 0 && len(state.Reasons) == 0 && !freshRecoveryEvidence(config, state, observation.At) {
		// Clearing the last reason requires fresh evidence from every required
		// source; an older healthy observation cannot authorize a combined recovery.
		state.Reasons = maps.Clone(previous.Reasons)
	}
	state.Version++
	return state
}

// sameEvidence identifies repeated evidence eligible for manual-count throttling.
// A changed condition must still reset the opposite streak, and a newly observed
// low wallet must suspend immediately when one low observation is sufficient.
func sameEvidence(config Config, counter Counter, o Observation) bool {
	switch o.Outcome {
	case Unreachable, ProbeUnhealthy:
		return counter.Failures > 0
	case Healthy:
		if counter.Successes == 0 {
			return false
		}
		if o.Source == "balance" && config.balanceControl() {
			low, recovered := balanceDecision(config.Policy, o.Balances)
			if low {
				return counter.Low > 0
			}
			if recovered {
				return counter.Recovered > 0
			}
			return counter.Low == 0 && counter.Recovered == 0
		}
		return true
	default:
		return counter.Failures == 0 && counter.Successes == 0
	}
}

// freshRecoveryEvidence requires current successful rounds from every enabled control source.
func freshRecoveryEvidence(config Config, state State, now time.Time) bool {
	sources := []string{}
	if config.balanceControl() {
		sources = append(sources, "balance")
	}
	if config.probeControl() {
		for _, probe := range config.Probes {
			if probe.Required {
				sources = append(sources, probe.ID)
			}
		}
	}
	for _, source := range sources {
		interval, _ := sourceInterval(config, source)
		counter := state.Counters[source]
		if counter.Successes < config.Policy.RecoveryCount || now.Sub(counter.LastHealthy) > time.Duration(interval*2)*time.Minute {
			return false
		}
		if source == "balance" && counter.Recovered < config.Policy.RecoveryCount {
			return false
		}
	}
	return true
}

// balanceDecision evaluates multi-currency thresholds with explicit missing-data rules.
// Any-low policies recover only when every selected currency has enough balance;
// all-low policies recover when one selected currency disproves the low condition.
// Missing observations never supply zero, low, or recovery evidence themselves.
// The two returned booleans can both be false in the hysteresis band or when
// incomplete observations cannot establish either side of the configured rule.
func balanceDecision(policy Policy, balances []Balance) (bool, bool) {
	values := make(map[string]string, len(balances))
	for _, balance := range balances {
		values[balance.Unit] = balance.Available
	}
	lowCount, recoveryCount := 0, 0
	for _, threshold := range policy.Thresholds {
		value, err := Amount(values[threshold.Unit])
		if err != nil {
			continue
		}
		low, _ := Amount(threshold.Low)
		recoverAt, _ := Amount(threshold.Recover)
		if value.LessThan(low) {
			lowCount++
		}
		if value.GreaterThanOrEqual(recoverAt) {
			recoveryCount++
		}
	}
	if policy.BalanceMatch == "all" {
		return lowCount == len(policy.Thresholds), recoveryCount > 0
	}
	return lowCount > 0, recoveryCount == len(policy.Thresholds)
}

// Reconfigure removes disabled reasons and invalidates previous candidate evidence.
// Existing reasons for enabled sources survive configuration changes, forcing new
// observations before recovery. Removed rules release only their own restrictions;
// a manual site suspension is preserved regardless of the resulting policy mode.
func Reconfigure(config Config, state State) State {
	state.Reasons = maps.Clone(state.Reasons)
	state.Counters = map[string]Counter{}
	for reason := range state.Reasons {
		keep := false
		switch {
		case reason == "balance_low" || reason == "balance_endpoint_unreachable":
			keep = config.balanceControl()
		case strings.HasPrefix(reason, "probe_unreachable:"):
			_, keep = sourceInterval(config, strings.TrimPrefix(reason, "probe_unreachable:"))
		}
		if !keep {
			delete(state.Reasons, reason)
		}
	}
	state.Version++
	return state
}

// sourceInterval returns an existing source's cadence and control responsibility.
func sourceInterval(config Config, source string) (int, bool) {
	if source == "balance" {
		return config.IntervalMinutes, config.balanceControl()
	}
	for _, probe := range config.Probes {
		if probe.ID == source {
			return probe.IntervalMinutes, probe.Required && config.probeControl()
		}
	}
	return 0, false
}

// addReason preserves the original timestamp when a restriction already exists.
func addReason(state *State, reason string, at time.Time) {
	if _, exists := state.Reasons[reason]; !exists {
		state.Reasons[reason] = at
	}
}

// clearReason releases an automatic reason only after its configured minimum duration.
func clearReason(state *State, reason string, policy Policy, now time.Time) {
	if since, exists := state.Reasons[reason]; exists && policy.AutoRecover && now.Sub(since) >= time.Duration(policy.MinSuspendSeconds)*time.Second {
		delete(state.Reasons, reason)
	}
}
