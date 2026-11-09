package dispatch

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func healthWindowStart() time.Time {
	return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
}

func applyWindowSample(t *testing.T, h *Health, target Target, at time.Time, outcome Outcome, passive bool) {
	t.Helper()
	o := testObservation(target, at, outcome)
	require.NoError(t, h.ApplyWithWindow(target, o, at, false, passive, &HealthWindowConfig{MinSamples: 20}, 1000))
}

func healthWindowSamples(t *testing.T, h *Health, target Target, start time.Time, failures, timeouts int, ttft int64) {
	t.Helper()
	for i := range 20 {
		at := start.Add(time.Duration(i) * time.Second)
		o := testObservation(target, at, Success)
		// 分散失败，避免与五次连续失败规则混淆。
		if i%2 == 0 && i/2 < failures {
			o.Outcome = UpstreamFailure
			o.TimedOut = i/2 < timeouts
		} else {
			o.TTFTMillis, o.LatencyMillis = ttft, ttft
		}
		require.NoError(t, h.ApplyWithWindow(target, o, at, false, true, &HealthWindowConfig{MinSamples: 20}, 1000))
	}
}

func TestDispatchHealthWindowLegacyBytesAndValidation(t *testing.T) {
	now := healthWindowStart()
	target := testTarget("a", 1)
	legacy, window := InitialHealth(target, now), InitialHealth(target, now)
	for i := range 45 {
		at := now.Add(time.Duration(i) * time.Second)
		o := testObservation(target, at, Success)
		o.TTFTMillis, o.LatencyMillis = 1500, 4000
		require.NoError(t, legacy.Apply(target, o, at, false))
		require.NoError(t, window.ApplyWithWindow(target, o, at, false, true, nil, 0))
	}
	before, err := json.Marshal(legacy)
	require.NoError(t, err)
	window.AdvanceHealthWindow(now.Add(time.Hour), nil, 0)
	after, err := json.Marshal(window)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NotContains(t, string(after), `"rolling"`)
	require.NotContains(t, string(after), `"timed_out"`)
	for _, outcome := range []Outcome{Success, Unknown, LocalRejection, ClientCanceled, UpstreamAuth, UpstreamRateLimit} {
		o := testObservation(target, now, outcome)
		o.TimedOut = true
		require.Error(t, validateObservation(o, target, now))
	}
	o := testObservation(target, now, UpstreamFailure)
	o.TimedOut = true
	require.NoError(t, validateObservation(o, target, now))
	o.ProviderEvidence = false
	require.Error(t, validateObservation(o, target, now))
	health := InitialHealth(target, now)
	o = testObservation(target, now, Success)
	require.Error(t, health.ApplyWithWindow(target, o, now, true, true, &HealthWindowConfig{MinSamples: 20}, 1000))
	require.Error(t, health.ApplyWithWindow(target, o, now, false, false, &HealthWindowConfig{}, 1000))
	require.Nil(t, health.Rolling, "invalid input must not mutate health")
}

func TestDispatchHealthWindowBucketsExpireAndKeepPassiveOrigin(t *testing.T) {
	now := healthWindowStart()
	target, policy := testTarget("a", 1), &HealthWindowConfig{MinSamples: 20}
	h := InitialHealth(target, now)
	for i := range 36 {
		at := now.Add(time.Duration(i) * healthBucketDuration)
		applyWindowSample(t, &h, target, at, Success, i%2 == 0)
	}
	require.Len(t, h.Rolling.Buckets, 30)
	report := h.HealthWindowReport(now.Add(350*time.Second), *policy, 1000)
	require.Equal(t, 6, report.Hot.Samples)
	require.Equal(t, 30, report.Baseline.Samples)
	require.Equal(t, 3, report.Hot.PassiveSamples)
	require.Equal(t, 15, report.Baseline.PassiveSamples)
	require.Equal(t, int64(1000), report.Hot.Factor)
	require.Equal(t, 3, h.RecentPassiveSamples(now.Add(350*time.Second)))
	h.AdvanceHealthWindow(now.Add(360*time.Second), policy, 1000)
	report = h.HealthWindowReport(now.Add(360*time.Second), *policy, 1000)
	require.Equal(t, 5, report.Hot.Samples, "boundary excludes oldest bucket and includes the empty current bucket")
	require.Equal(t, 29, report.Baseline.Samples)
	h.AdvanceHealthWindow(now.AddDate(10, 0, 0), policy, 1000)
	require.Empty(t, h.Rolling.Buckets)
	require.Zero(t, h.Rolling.GoodWindows)
	require.Zero(t, h.Rolling.BadWindows)
	require.Zero(t, h.RecentPassiveSamples(now.AddDate(10, 0, 0)))
}

func TestDispatchHealthWindowTrustedEvidenceAndTimeoutFactor(t *testing.T) {
	now := healthWindowStart()
	target, policy := testTarget("a", 1), &HealthWindowConfig{MinSamples: 20}
	h := InitialHealth(target, now)
	for _, outcome := range []Outcome{Unknown, LocalRejection, ClientCanceled} {
		applyWindowSample(t, &h, target, now, outcome, true)
	}
	o := testObservation(target, now, Success)
	o.ProviderEvidence = false
	require.NoError(t, h.ApplyWithWindow(target, o, now, false, true, policy, 1000))
	require.Empty(t, h.Rolling.Buckets)
	for i := range 20 {
		o := testObservation(target, now, Success)
		o.TTFTMillis, o.LatencyMillis = 1500, 4000
		if i%2 == 0 && i < 8 {
			o.Outcome, o.TTFTMillis = UpstreamFailure, 0
			o.TimedOut = i < 4
		}
		require.NoError(t, h.ApplyWithWindow(target, o, now, i == 19, i < 10, policy, 1000))
		if i == 18 {
			require.Equal(t, int64(1000), h.WindowFactor(now, *policy, 1000), "insufficient samples retain the prior")
		}
	}
	report := h.HealthWindowReport(now, *policy, 1000)
	require.Equal(t, 20, report.Hot.Samples)
	require.Equal(t, 4, report.Hot.Failures)
	require.Equal(t, 2, report.Hot.Timeouts)
	require.Equal(t, 10, report.Hot.PassiveSamples)
	require.Equal(t, int64(765), report.Hot.Factor, "1 - .55*.2 - .25*.1 - .20*.5")
	require.Equal(t, Healthy, h.State)
	// 最近桶已过期的观测不参与因子，完整响应耗时也不替代 TTFT。
	h.TTFTObservedAt = now.Add(-5*time.Minute - time.Nanosecond)
	require.Equal(t, int64(865), h.WindowFactor(now, *policy, 1000))
	h.TTFTObservedAt = now.Add(-5 * time.Minute)
	require.Equal(t, int64(765), h.WindowFactor(now, *policy, 1000))
	h.TTFTObservedAt = now.Add(time.Second)
	require.Equal(t, int64(865), h.WindowFactor(now, *policy, 1000))
	h.TTFTMillis, h.LatencyMillis = 0, 900000
	require.Equal(t, int64(865), h.WindowFactor(now, *policy, 1000))
}

func TestDispatchHealthWindowRequiresTwoFullMinutes(t *testing.T) {
	now := healthWindowStart().Add(5 * time.Second)
	target, policy := testTarget("a", 1), &HealthWindowConfig{MinSamples: 20}
	h := InitialHealth(target, now)
	healthWindowSamples(t, &h, target, now, 8, 8, 2000)
	h.AdvanceHealthWindow(now.Truncate(time.Minute).Add(time.Minute), policy, 1000)
	require.Equal(t, Healthy, h.State, "activation's incomplete minute must not qualify")
	require.Zero(t, h.Rolling.BadWindows)
	first := now.Truncate(time.Minute).Add(time.Minute)
	healthWindowSamples(t, &h, target, first, 8, 8, 2000)
	h.AdvanceHealthWindow(first.Add(time.Minute), policy, 1000)
	require.Equal(t, Healthy, h.State)
	require.Equal(t, 1, h.Rolling.BadWindows)
	h.AdvanceHealthWindow(first.Add(time.Minute), policy, 1000)
	require.Equal(t, 1, h.Rolling.BadWindows, "repeated publication cannot count the same minute twice")
	healthWindowSamples(t, &h, target, first.Add(time.Minute), 8, 8, 2000)
	h.AdvanceHealthWindow(first.Add(2*time.Minute), policy, 1000)
	require.Equal(t, Degraded, h.State)
	require.Equal(t, 2, h.Rolling.BadWindows)
	for minute := 2; minute < 4; minute++ {
		healthWindowSamples(t, &h, target, first.Add(time.Duration(minute)*time.Minute), 0, 0, 1000)
		h.AdvanceHealthWindow(first.Add(time.Duration(minute+1)*time.Minute), policy, 1000)
	}
	require.Equal(t, Healthy, h.State)
}

func TestDispatchHealthWindowGapsBreakConsecutiveMinutes(t *testing.T) {
	for _, gapSamples := range []int{0, 19} {
		t.Run(time.Duration(gapSamples).String(), func(t *testing.T) {
			now := healthWindowStart()
			target, policy := testTarget("a", 1), &HealthWindowConfig{MinSamples: 20}
			h := InitialHealth(target, now)
			healthWindowSamples(t, &h, target, now, 8, 8, 2000)
			h.AdvanceHealthWindow(now.Add(time.Minute), policy, 1000)
			require.Equal(t, 1, h.Rolling.BadWindows)
			for i := range gapSamples {
				applyWindowSample(t, &h, target, now.Add(time.Minute+time.Duration(i)*time.Second), Success, true)
			}
			h.AdvanceHealthWindow(now.Add(2*time.Minute), policy, 1000)
			require.Zero(t, h.Rolling.BadWindows)
			require.Zero(t, h.Rolling.GoodWindows)
			healthWindowSamples(t, &h, target, now.Add(2*time.Minute), 8, 8, 2000)
			h.AdvanceHealthWindow(now.Add(3*time.Minute), policy, 1000)
			require.Equal(t, Healthy, h.State)
			require.Equal(t, 1, h.Rolling.BadWindows)
			h.State, h.RecoveryStep = Degraded, 1
			h.Rolling.GoodWindows = 1
			h.AdvanceHealthWindow(now.Add(24*time.Hour), policy, 1000)
			require.Equal(t, Degraded, h.State)
			require.Equal(t, 1, h.RecoveryStep, "time alone cannot advance recovery")
			require.Zero(t, h.Rolling.GoodWindows)
		})
	}
}

func TestDispatchHealthWindowClockRollbackAndPersistence(t *testing.T) {
	now := healthWindowStart()
	target, policy := testTarget("a", 1), &HealthWindowConfig{MinSamples: 20}
	h := InitialHealth(target, now)
	healthWindowSamples(t, &h, target, now, 8, 8, 2000)
	h.AdvanceHealthWindow(now.Add(time.Minute), policy, 1000)
	before, err := json.Marshal(h)
	require.NoError(t, err)
	h.AdvanceHealthWindow(now, policy, 1000)
	after, err := json.Marshal(h)
	require.NoError(t, err)
	require.Equal(t, before, after)
	report := h.HealthWindowReport(now, *policy, 1000)
	require.True(t, report.ClockBehind)
	require.Equal(t, h.WindowFactor(now.Add(time.Minute), *policy, 1000), report.Hot.Factor)
	h.State = Probing
	h.ProbeSuccesses = 1
	o := testObservation(target, now, Success)
	require.NoError(t, h.ApplyWithWindow(target, o, now, true, false, policy, 1000))
	require.Equal(t, Probing, h.State)
	require.Equal(t, 1, h.ProbeSuccesses)
	require.Equal(t, now.Add(time.Minute), h.UpdatedAt)
	require.Equal(t, 21, h.HealthWindowReport(now, *policy, 1000).Baseline.Samples, "rollback must retain confirmed facts")
	encoded, err := json.Marshal(h)
	require.NoError(t, err)
	var restarted Health
	require.NoError(t, json.Unmarshal(encoded, &restarted))
	require.Equal(t, h.HealthWindowReport(now, *policy, 1000), restarted.HealthWindowReport(now, *policy, 1000))
	restarted.AdvanceHealthWindow(now.Add(10*time.Minute), policy, 1000)
	require.Equal(t, Probing, restarted.State)
	require.Empty(t, restarted.Rolling.Buckets)
}

func TestDispatchHealthWindowHardTripsAndProbeRecovery(t *testing.T) {
	now := healthWindowStart()
	target, policy := testTarget("a", 1), &HealthWindowConfig{MinSamples: 20}
	for _, outcome := range []Outcome{UpstreamAuth, UpstreamRateLimit} {
		h := InitialHealth(target, now)
		o := testObservation(target, now, outcome)
		o.RetryAfterSeconds = 120
		require.NoError(t, h.ApplyWithWindow(target, o, now, false, true, policy, 1000))
		require.Equal(t, Tripped, h.State)
		require.Equal(t, 1, h.HealthWindowReport(now, *policy, 1000).Hot.Failures)
		if outcome == UpstreamRateLimit {
			require.Equal(t, now.Add(120*time.Second), h.CoolUntil)
		} else {
			require.True(t, h.AuthBlocked)
		}
	}
	h := InitialHealth(target, now)
	for i := range 5 {
		applyWindowSample(t, &h, target, now.Add(time.Duration(i)*time.Second), UpstreamFailure, true)
	}
	require.Equal(t, Tripped, h.State)
	firstProbe := now.Add(time.Minute)
	healthWindowSamples(t, &h, target, firstProbe, 0, 0, 1000)
	healthWindowSamples(t, &h, target, firstProbe.Add(time.Minute), 0, 0, 1000)
	h.AdvanceHealthWindow(firstProbe.Add(2*time.Minute), policy, 1000)
	require.Equal(t, Tripped, h.State, "passive success and H cannot revive a tripped target")
	for i := range 2 {
		at := firstProbe.Add(2*time.Minute + time.Duration(i)*30*time.Second)
		h.State = Probing
		o := testObservation(target, at, Success)
		require.NoError(t, h.ApplyWithWindow(target, o, at, true, false, policy, 1000))
	}
	require.Equal(t, Degraded, h.State)
	require.Equal(t, 1, h.RecoveryStep)
	require.Equal(t, int64(10), h.Candidate(target).EffectiveWeight)
	start := firstProbe.Add(3 * time.Minute)
	for i := range 2 {
		healthWindowSamples(t, &h, target, start.Add(time.Duration(i)*time.Minute), 0, 0, 1000)
		h.AdvanceHealthWindow(start.Add(time.Duration(i+1)*time.Minute), policy, 1000)
	}
	require.Equal(t, Degraded, h.State)
	require.Equal(t, 2, h.RecoveryStep)
	// 比例熔断不依赖连续失败，也不等待完整分钟结束。
	h = InitialHealth(target, now)
	healthWindowSamples(t, &h, target, now, 10, 0, 0)
	require.Equal(t, Tripped, h.State)
	require.Equal(t, "failure_window", h.Reason)
	// 两个新探针只恢复 10%；旧热窗失败不能在成功分支重新熔断。
	h = InitialHealth(target, now)
	for range 20 {
		applyWindowSample(t, &h, target, now, UpstreamFailure, true)
	}
	for i := range 2 {
		at := now.Add(time.Duration(i+1) * time.Second)
		h.State = Probing
		o := testObservation(target, at, Success)
		require.NoError(t, h.ApplyWithWindow(target, o, at, true, false, policy, 1000))
	}
	require.Equal(t, Degraded, h.State)
	require.Equal(t, 1, h.RecoveryStep)
	require.Equal(t, int64(10), h.Candidate(target).EffectiveWeight)
	applyWindowSample(t, &h, target, now.Add(3*time.Second), Success, true)
	require.Equal(t, Degraded, h.State)
}

func TestDispatchHealthWindowCooldownAnchorsIgnorePassiveSuccess(t *testing.T) {
	now := healthWindowStart()
	target, policy := testTarget("a", 1), &HealthWindowConfig{MinSamples: 20}
	h := InitialHealth(target, now)
	o := testObservation(target, now, UpstreamRateLimit)
	o.RetryAfterSeconds = 120
	require.NoError(t, h.ApplyWithWindow(target, o, now, false, true, policy, 1000))
	require.Equal(t, now, h.Rolling.CooldownStartedAt)
	require.Equal(t, now.Add(120*time.Second), h.Rolling.CooldownUntil)
	for i := range 5 {
		applyWindowSample(t, &h, target, now.Add(time.Duration(i+1)*time.Second), Success, true)
	}
	require.Equal(t, now.Add(5*time.Second), h.UpdatedAt)
	require.Equal(t, now, h.Rolling.CooldownStartedAt)
	require.Equal(t, now.Add(120*time.Second), h.Rolling.CooldownUntil)
	at := now.Add(130 * time.Second)
	h.State = Probing
	o = testObservation(target, at, Success)
	require.NoError(t, h.ApplyWithWindow(target, o, at, true, false, policy, 1000))
	require.Equal(t, Tripped, h.State)
	require.Equal(t, at, h.Rolling.CooldownStartedAt)
	require.Equal(t, at.Add(30*time.Second), h.Rolling.CooldownUntil)
	require.Equal(t, h.CoolUntil, h.Rolling.CooldownUntil)
	applyWindowSample(t, &h, target, at.Add(time.Second), Success, true)
	require.Equal(t, at, h.Rolling.CooldownStartedAt)
	require.Equal(t, at.Add(30*time.Second), h.Rolling.CooldownUntil)
}

func TestDispatchHealthWindowActivationRespectsNativeClockFence(t *testing.T) {
	now := healthWindowStart()
	target, policy := testTarget("a", 1), &HealthWindowConfig{MinSamples: 20}
	h := InitialHealth(target, now.Add(2*time.Second))
	h.State, h.ProbeSuccesses = Probing, 1
	o := testObservation(target, now, Success)
	require.NoError(t, h.ApplyWithWindow(target, o, now, true, false, policy, 1000))
	require.Equal(t, Probing, h.State, "rebuilding a window cannot discard the retained confirmation clock")
	require.Equal(t, 1, h.ProbeSuccesses)
	require.Equal(t, now.Add(2*time.Second), h.UpdatedAt)
	require.True(t, h.HealthWindowReport(now, *policy, 1000).ClockBehind)
	at := now.Add(3 * time.Second)
	o.At, o.EventID = at, "after-clock-fence"
	require.NoError(t, h.ApplyWithWindow(target, o, at, true, false, policy, 1000))
	require.Equal(t, Degraded, h.State, "confirmed time permits the next independent recovery success")
	require.Equal(t, 1, h.RecoveryStep)
}
