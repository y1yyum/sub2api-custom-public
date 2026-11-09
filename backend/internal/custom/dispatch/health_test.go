package dispatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDispatchProbeUnknownPreservesMonotonicTime(t *testing.T) {
	now := time.Now().UTC()
	target := testTarget("a", 1)
	health := InitialHealth(target, now)
	health.State = Probing
	health.UpdatedAt = now.Add(time.Second)
	observation := Observation{EventID: "unknown-probe", TargetRef: target.Ref, AccountID: target.AccountID,
		CredentialRevision: target.CredentialRevision, At: health.UpdatedAt, Outcome: Unknown}
	require.NoError(t, health.Apply(target, observation, now, true))
	require.Equal(t, observation.At, health.UpdatedAt)
	require.Equal(t, Tripped, health.State)
	require.Equal(t, "probe_unattributed", health.Reason)
}

func testObservation(target Target, now time.Time, outcome Outcome) Observation {
	return Observation{EventID: "event", TargetRef: target.Ref, AccountID: target.AccountID, CredentialRevision: target.CredentialRevision, Outcome: outcome, At: now, ProviderEvidence: true}
}

func TestDispatchHealthIgnoresUnattributedAndClientFailures(t *testing.T) {
	now := time.Now().UTC()
	target := testTarget("a", 1)
	for _, outcome := range []Outcome{LocalRejection, ClientCanceled, Unknown} {
		health := InitialHealth(target, now)
		for n := range 10 {
			o := testObservation(target, now.Add(time.Duration(n)*time.Second), outcome)
			require.NoError(t, health.Apply(target, o, o.At, false))
		}
		require.Equal(t, Healthy, health.State)
		require.Zero(t, health.Samples)
	}
	health := InitialHealth(target, now)
	observation := testObservation(target, now, UpstreamAuth)
	observation.ProviderEvidence = false
	require.NoError(t, health.Apply(target, observation, now, false))
	require.Equal(t, Healthy, health.State)
	require.False(t, health.AuthBlocked)
}

func TestDispatchHealthCredentialAndRateLimit(t *testing.T) {
	now := time.Now().UTC()
	target := testTarget("a", 1)
	health := InitialHealth(target, now)
	require.NoError(t, health.Apply(target, testObservation(target, now, UpstreamAuth), now, false))
	require.True(t, health.AuthBlocked)
	require.Equal(t, Tripped, health.State)
	require.Zero(t, health.Candidate(target).EffectiveWeight)
	require.NoError(t, health.Apply(target, testObservation(target, now.Add(time.Minute), Success), now.Add(time.Minute), true))
	require.True(t, health.AuthBlocked)
	health = InitialHealth(target, now)
	observation := testObservation(target, now, UpstreamRateLimit)
	observation.RetryAfterSeconds = 120
	require.NoError(t, health.Apply(target, observation, now, false))
	require.Equal(t, now.Add(120*time.Second), health.CoolUntil)
	observation.RetryAfterSeconds = 601
	require.Error(t, health.Apply(target, observation, now, false))
}

func TestDispatchHealthFiveFailuresRequireTwoVerifiedProbes(t *testing.T) {
	now := time.Now().UTC()
	target := testTarget("a", 1)
	health := InitialHealth(target, now)
	for n := range 5 {
		at := now.Add(time.Duration(n) * time.Second)
		require.NoError(t, health.Apply(target, testObservation(target, at, UpstreamFailure), at, false))
	}
	require.Equal(t, Tripped, health.State)
	require.Equal(t, 1, health.Backoff)
	require.Equal(t, now.Add(34*time.Second), health.CoolUntil)
	// 被动成功不能使熔断账号自动重新接流。
	at := now.Add(35 * time.Second)
	require.NoError(t, health.Apply(target, testObservation(target, at, Success), at, false))
	require.Equal(t, Tripped, health.State)
	health.State = Probing
	require.NoError(t, health.Apply(target, testObservation(target, at, Success), at, true))
	require.Equal(t, Tripped, health.State)
	require.Equal(t, 1, health.ProbeSuccesses)
	at = at.Add(30 * time.Second)
	health.State = Probing
	require.NoError(t, health.Apply(target, testObservation(target, at, Success), at, true))
	require.Equal(t, Degraded, health.State)
	require.Equal(t, int64(10), health.Candidate(target).EffectiveWeight)
	health.State = Probing
	at = at.Add(time.Second)
	require.NoError(t, health.Apply(target, testObservation(target, at, Unknown), at, true))
	require.Equal(t, Tripped, health.State)
	require.Zero(t, health.ProbeSuccesses)
}

func TestDispatchHealthWindowsAndLatency(t *testing.T) {
	now := time.Now().UTC()
	target := testTarget("a", 1)
	health := InitialHealth(target, now)
	for window := range 2 {
		for sample := range 20 {
			at := now.Add(time.Duration(window)*time.Minute + time.Duration(sample)*time.Second)
			outcome := Success
			if sample%3 == 0 {
				outcome = UpstreamFailure
			}
			require.NoError(t, health.Apply(target, testObservation(target, at, outcome), at, false))
		}
	}
	at := now.Add(2 * time.Minute)
	require.NoError(t, health.Apply(target, testObservation(target, at, Success), at, false))
	require.Equal(t, Degraded, health.State)
	for window := range 2 {
		for sample := range 20 {
			at := now.Add(time.Duration(window+2)*time.Minute + time.Duration(sample)*time.Second)
			o := testObservation(target, at, Success)
			o.LatencyMillis = 2000
			require.NoError(t, health.Apply(target, o, at, false))
		}
	}
	at = now.Add(4 * time.Minute)
	require.NoError(t, health.Apply(target, testObservation(target, at, Success), at, false))
	require.Equal(t, Healthy, health.State)
	require.Equal(t, int64(50), health.Candidate(target).EffectiveWeight)
}

func TestDispatchHealthRejectsWrongAccountStaleAndFuture(t *testing.T) {
	now := time.Now().UTC()
	target := testTarget("a", 1)
	for name, change := range map[string]func(*Observation){
		"account": func(o *Observation) { o.AccountID = 2 }, "target": func(o *Observation) { o.TargetRef = "b" }, "credential": func(o *Observation) { o.CredentialRevision = 2 },
		"future": func(o *Observation) { o.At = now.Add(6 * time.Second) }, "stale": func(o *Observation) { o.At = now.Add(-6 * time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			h := InitialHealth(target, now)
			o := testObservation(target, now, UpstreamFailure)
			change(&o)
			require.Error(t, h.Apply(target, o, now, false))
			require.Equal(t, Healthy, h.State)
		})
	}
}
