package dispatch

import (
	"errors"
	"time"
)

type Outcome string

const (
	Success           Outcome = "success"
	UpstreamAuth      Outcome = "upstream_auth"
	UpstreamRateLimit Outcome = "upstream_rate_limit"
	UpstreamFailure   Outcome = "upstream_failure"
	Unknown           Outcome = "unknown"
	LocalRejection    Outcome = "local_rejection"
	ClientCanceled    Outcome = "client_canceled"
)

// Observation 由可信适配器提供；HTTP 状态本身不能证明错误来自 Provider。
type Observation struct {
	EventID            string    `json:"event_id"`
	TargetRef          string    `json:"target_ref"`
	AccountID          int64     `json:"account_id"`
	CredentialRevision int64     `json:"credential_revision"`
	Outcome            Outcome   `json:"outcome"`
	At                 time.Time `json:"at"`
	ProviderEvidence   bool      `json:"provider_evidence"`
	RetryAfterSeconds  int       `json:"retry_after_seconds"`
	LatencyMillis      int64     `json:"latency_millis"`
	TTFTMillis         int64     `json:"ttft_millis,omitempty"`
	FailureClass       string    `json:"failure_class,omitempty"`
	TimedOut           bool      `json:"timed_out,omitempty"`
	Phase              string    `json:"phase,omitempty"`
	ProtocolFact       string    `json:"protocol_fact,omitempty"`
	UsageMissing       bool      `json:"usage_missing,omitempty"`
}

type Health struct {
	State               State               `json:"state"`
	Reason              string              `json:"reason"`
	CredentialRevision  int64               `json:"credential_revision"`
	AuthBlocked         bool                `json:"auth_blocked"`
	ConsecutiveFailures int                 `json:"consecutive_failures"`
	ProbeSuccesses      int                 `json:"probe_successes"`
	RecoveryStep        int                 `json:"recovery_step"`
	Backoff             int                 `json:"backoff"`
	CoolUntil           time.Time           `json:"cool_until"`
	UpdatedAt           time.Time           `json:"updated_at"`
	LatencyMillis       int64               `json:"latency_millis"`
	TTFTMillis          int64               `json:"ttft_millis,omitempty"`
	TTFTObservedAt      time.Time           `json:"ttft_observed_at,omitzero"`
	Routing             *routingWeightState `json:"routing,omitempty"`
	WindowAt            time.Time           `json:"window_at"`
	Samples             int                 `json:"samples"`
	Failures            int                 `json:"failures"`
	GoodWindows         int                 `json:"good_windows"`
	BadWindows          int                 `json:"bad_windows"`
	HoldRevisions       map[string]int64    `json:"hold_revisions,omitempty"`
	Rolling             *RollingHealth      `json:"rolling,omitempty"`
}

func InitialHealth(target Target, now time.Time) Health {
	return Health{State: Healthy, Reason: "healthy", CredentialRevision: target.CredentialRevision, UpdatedAt: now, WindowAt: now}
}

func (h Health) hasFreshTTFT(now time.Time) bool {
	return h.TTFTMillis > 0 && !h.TTFTObservedAt.IsZero() &&
		!h.TTFTObservedAt.After(now) && now.Sub(h.TTFTObservedAt) <= 5*time.Minute
}

func validateObservation(o Observation, target Target, now time.Time) error {
	if !validRef(o.EventID) || o.TargetRef != target.Ref || o.AccountID != target.AccountID || o.CredentialRevision != target.CredentialRevision ||
		o.At.IsZero() || o.At.After(now.Add(5*time.Second)) || o.At.Before(now.Add(-5*time.Minute)) ||
		o.RetryAfterSeconds < 0 || o.RetryAfterSeconds > 600 || o.LatencyMillis < 0 || o.LatencyMillis > 900000 ||
		o.TTFTMillis < 0 || o.TTFTMillis > 900000 || !validFailureClass(o) || !validProtocolFacts(o) ||
		(o.TimedOut && (!o.ProviderEvidence || o.Outcome != UpstreamFailure)) ||
		(o.TTFTMillis > 0 && (o.Outcome != Success || !o.ProviderEvidence || o.TTFTMillis > o.LatencyMillis)) {
		return errors.New("invalid or stale dispatch observation")
	}
	switch o.Outcome {
	case Success, UpstreamAuth, UpstreamRateLimit, UpstreamFailure, Unknown, LocalRejection, ClientCanceled:
		return nil
	default:
		return errors.New("invalid dispatch observation outcome")
	}
}

func (h Health) Candidate(target Target) Candidate {
	weight := target.BaseWeight
	reason := h.Reason
	if !target.Enabled {
		reason = "disabled"
		weight = 0
	}
	if h.State == Tripped || h.State == Probing || h.AuthBlocked {
		weight = 0
	}
	if h.State == Degraded {
		percent := int64(50)
		if h.RecoveryStep > 0 {
			percent = []int64{10, 25, 50, 100}[min(h.RecoveryStep-1, 3)]
		}
		weight = max(1, weight*percent/100)
	}
	if h.LatencyMillis > 1000 {
		weight = max(1, weight*max(250, 1000000/h.LatencyMillis)/1000)
	}
	if !target.Enabled || h.AuthBlocked || h.State == Tripped || h.State == Probing {
		weight = 0
	}
	return Candidate{Target: target, State: h.State, Reason: reason, EffectiveWeight: weight}
}

func (h *Health) trip(now time.Time, reason string, retryAfter time.Duration) {
	h.State, h.Reason, h.ProbeSuccesses, h.RecoveryStep = Tripped, reason, 0, 0
	h.Backoff = min(h.Backoff+1, 6)
	cooldown := min(30*time.Second*time.Duration(1<<(h.Backoff-1)), 10*time.Minute)
	h.CoolUntil = now.Add(max(cooldown, retryAfter))
	if h.Rolling != nil {
		h.Rolling.CooldownStartedAt, h.Rolling.CooldownUntil = now.UTC(), h.CoolUntil.UTC()
	}
}

func (h *Health) Apply(target Target, observation Observation, now time.Time, probe bool) error {
	if err := validateObservation(observation, target, now); err != nil {
		return err
	}
	if observation.FailureClass != "" {
		return errors.New("classified dispatch failure requires persistent hold handling")
	}
	if observation.At.Before(h.UpdatedAt) {
		return nil
	}
	if observation.Outcome == LocalRejection || observation.Outcome == ClientCanceled || observation.Outcome == Unknown || !observation.ProviderEvidence {
		if probe && h.State == Probing {
			h.trip(now, "probe_unattributed", 0)
			h.UpdatedAt = observation.At
		}
		return nil
	}
	if h.CredentialRevision != target.CredentialRevision {
		return errors.New("dispatch credential revision mismatch")
	}
	if h.AuthBlocked {
		return nil
	}
	if observation.At.Sub(h.WindowAt) >= time.Minute {
		if h.Samples >= 20 {
			if h.Failures*2 >= h.Samples {
				h.trip(now, "failure_window", 0)
			} else if h.Failures*4 > h.Samples {
				h.BadWindows++
				h.GoodWindows = 0
			} else if h.Failures*10 <= h.Samples {
				h.GoodWindows++
				h.BadWindows = 0
			} else {
				h.GoodWindows, h.BadWindows = 0, 0
			}
			if h.State == Healthy && h.BadWindows >= 2 {
				h.State, h.Reason = Degraded, "degraded_window"
			}
			if h.State == Degraded && h.GoodWindows >= 2 {
				if h.RecoveryStep > 0 && h.RecoveryStep < 4 {
					h.RecoveryStep++
					h.GoodWindows = 0
				} else {
					h.State, h.Reason, h.Backoff = Healthy, "healthy", 0
				}
			}
		}
		h.WindowAt, h.Samples, h.Failures = observation.At, 0, 0
	}
	h.UpdatedAt = observation.At
	if observation.Outcome == UpstreamAuth {
		h.AuthBlocked = true
		h.trip(now, "credential_rejected", 0)
		return nil
	}
	if observation.Outcome == UpstreamRateLimit {
		h.trip(now, "rate_limited", time.Duration(observation.RetryAfterSeconds)*time.Second)
		return nil
	}
	h.Samples++
	if observation.Outcome == UpstreamFailure {
		h.Failures++
		h.ConsecutiveFailures++
		h.ProbeSuccesses = 0
		if h.ConsecutiveFailures >= 5 || h.State == Probing {
			h.trip(now, "upstream_failure", 0)
		}
		return nil
	}
	h.ConsecutiveFailures = 0
	if observation.LatencyMillis > 0 {
		if h.LatencyMillis == 0 {
			h.LatencyMillis = observation.LatencyMillis
		} else {
			h.LatencyMillis = (h.LatencyMillis*4 + observation.LatencyMillis) / 5
		}
	}
	if observation.TTFTMillis > 0 {
		if !h.hasFreshTTFT(now) {
			h.TTFTMillis = observation.TTFTMillis
		} else {
			h.TTFTMillis = (h.TTFTMillis*4 + observation.TTFTMillis) / 5
		}
		h.TTFTObservedAt = now.UTC()
	}
	if probe && h.State == Probing {
		h.ProbeSuccesses++
		if h.ProbeSuccesses >= 2 {
			h.State, h.Reason, h.RecoveryStep = Degraded, "probe_recovered", 1
		} else {
			h.State, h.Reason, h.CoolUntil = Tripped, "probe_confirm_required", now.Add(30*time.Second)
			if h.Rolling != nil {
				h.Rolling.CooldownStartedAt, h.Rolling.CooldownUntil = now.UTC(), h.CoolUntil.UTC()
			}
		}
	}
	return nil
}
