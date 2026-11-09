package dispatch

import (
	"errors"
	"math"
	"time"
)

const healthBucketDuration = 10 * time.Second

type HealthBucket struct {
	Start          time.Time      `json:"start"`
	Samples        int            `json:"samples"`
	Failures       int            `json:"failures"`
	Timeouts       int            `json:"timeouts"`
	PassiveSamples int            `json:"passive_samples"`
	Facts          map[string]int `json:"facts,omitempty"`
}

type RollingHealth struct {
	ActivatedAt       time.Time      `json:"activated_at"`
	AdvancedAt        time.Time      `json:"advanced_at"`
	CompletedThrough  time.Time      `json:"completed_through"`
	Buckets           []HealthBucket `json:"buckets"`
	GoodWindows       int            `json:"good_windows"`
	BadWindows        int            `json:"bad_windows"`
	CooldownStartedAt time.Time      `json:"cooldown_started_at,omitzero"`
	CooldownUntil     time.Time      `json:"cooldown_until,omitzero"`
}

type HealthWindowStats struct {
	Samples        int   `json:"samples"`
	Failures       int   `json:"failures"`
	Timeouts       int   `json:"timeouts"`
	PassiveSamples int   `json:"passive_samples"`
	Factor         int64 `json:"factor"`
	// Facts 仅报告被动协议事实计数，不参与 Factor。
	Facts map[string]int `json:"facts,omitempty"`
}

type HealthWindowReport struct {
	Hot              HealthWindowStats `json:"hot"`
	Baseline         HealthWindowStats `json:"baseline"`
	ActivatedAt      time.Time         `json:"activated_at"`
	CompletedThrough time.Time         `json:"completed_through"`
	GoodWindows      int               `json:"good_windows"`
	BadWindows       int               `json:"bad_windows"`
	ClockBehind      bool              `json:"clock_behind"`
}

func (h Health) healthWindowClockFloor() time.Time {
	if h.Rolling == nil {
		return h.UpdatedAt.UTC()
	}
	return maxTime(h.UpdatedAt.UTC(), h.Rolling.AdvancedAt.UTC())
}

func validHealthWindow(policy HealthWindowConfig, ttftTargetMillis int64) bool {
	return policy.MinSamples >= 20 && policy.MinSamples <= 100000 && ttftTargetMillis > 0 && ttftTargetMillis <= 900000
}

func nextFullHealthMinute(now time.Time) time.Time {
	start := now.UTC().Truncate(time.Minute)
	if start.Before(now) {
		start = start.Add(time.Minute)
	}
	return start
}

func (h Health) healthWindowStats(start, end, now time.Time, policy HealthWindowConfig, ttftTargetMillis int64) HealthWindowStats {
	stats := HealthWindowStats{Factor: 1000}
	if h.Rolling == nil {
		return stats
	}
	for _, bucket := range h.Rolling.Buckets {
		if !bucket.Start.Before(start) && bucket.Start.Before(end) {
			stats.Samples += bucket.Samples
			stats.Failures += bucket.Failures
			stats.Timeouts += bucket.Timeouts
			stats.PassiveSamples += bucket.PassiveSamples
			for key, count := range bucket.Facts {
				if stats.Facts == nil {
					stats.Facts = make(map[string]int, len(bucket.Facts))
				}
				stats.Facts[key] += count
			}
		}
	}
	if stats.Samples < policy.MinSamples || !validHealthWindow(policy, ttftTargetMillis) {
		return stats
	}
	penalty := (550*float64(stats.Failures) + 250*float64(stats.Timeouts)) / float64(stats.Samples)
	if h.hasFreshTTFT(now) {
		penalty += 200 * float64(min(ttftTargetMillis, max(0, h.TTFTMillis-ttftTargetMillis))) / float64(ttftTargetMillis)
	}
	stats.Factor = int64(math.Floor(max(0, 1000-penalty) + 1e-9))
	return stats
}

func (h Health) HealthWindowReport(now time.Time, policy HealthWindowConfig, ttftTargetMillis int64) HealthWindowReport {
	report := HealthWindowReport{Hot: HealthWindowStats{Factor: 1000}, Baseline: HealthWindowStats{Factor: 1000},
		ClockBehind: now.Before(h.healthWindowClockFloor())}
	if h.Rolling == nil {
		return report
	}
	r := h.Rolling
	report.ActivatedAt, report.CompletedThrough = r.ActivatedAt, r.CompletedThrough
	report.GoodWindows, report.BadWindows = r.GoodWindows, r.BadWindows
	now = maxTime(now.UTC(), h.healthWindowClockFloor())
	end := now.Truncate(healthBucketDuration).Add(healthBucketDuration)
	report.Hot = h.healthWindowStats(end.Add(-time.Minute), end, now, policy, ttftTargetMillis)
	report.Baseline = h.healthWindowStats(end.Add(-5*time.Minute), end, now, policy, ttftTargetMillis)
	return report
}

func (h Health) RecentPassiveSamples(now time.Time) int {
	return h.HealthWindowReport(now, HealthWindowConfig{MinSamples: 20}, 1000).Hot.PassiveSamples
}

func (h Health) WindowFactor(now time.Time, policy HealthWindowConfig, ttftTargetMillis int64) int64 {
	return h.HealthWindowReport(now, policy, ttftTargetMillis).Hot.Factor
}

func (h *Health) AdvanceHealthWindow(now time.Time, policy *HealthWindowConfig, ttftTargetMillis int64) {
	if policy == nil || !validHealthWindow(*policy, ttftTargetMillis) {
		return
	}
	now = now.UTC()
	if h.Rolling == nil {
		anchor := maxTime(now, h.healthWindowClockFloor())
		h.Rolling = &RollingHealth{ActivatedAt: anchor, AdvancedAt: anchor, CompletedThrough: nextFullHealthMinute(anchor), Buckets: []HealthBucket{}}
		return
	}
	r := h.Rolling
	if now.Before(h.healthWindowClockFloor()) {
		return
	}
	through := now.Truncate(time.Minute)
	start := r.CompletedThrough
	// 只评估保留范围内的完整分钟；更早的空洞必须打断连续计数。
	if earliest := through.Add(-4 * time.Minute); start.Before(earliest) {
		start = earliest
		r.GoodWindows, r.BadWindows = 0, 0
	}
	for start.Before(through) {
		end := start.Add(time.Minute)
		stats := h.healthWindowStats(start, end, end, *policy, ttftTargetMillis)
		h.completeHealthMinute(stats, now, *policy)
		start = end
	}
	if through.After(r.CompletedThrough) {
		r.CompletedThrough = through
	}
	r.AdvancedAt = now
	h.trimHealthWindowBuckets(now)
}

func (h *Health) trimHealthWindowBuckets(at time.Time) {
	cutoff := at.Truncate(healthBucketDuration).Add(-29 * healthBucketDuration)
	kept := h.Rolling.Buckets[:0]
	for _, bucket := range h.Rolling.Buckets {
		if !bucket.Start.Before(cutoff) {
			kept = append(kept, bucket)
		}
	}
	h.Rolling.Buckets = kept
}

func (h *Health) completeHealthMinute(stats HealthWindowStats, now time.Time, policy HealthWindowConfig) {
	r := h.Rolling
	if stats.Samples < policy.MinSamples || h.AuthBlocked || h.State == Tripped || h.State == Probing {
		r.GoodWindows, r.BadWindows = 0, 0
		return
	}
	if stats.Failures*2 >= stats.Samples {
		h.trip(now, "failure_window", 0)
		r.GoodWindows, r.BadWindows = 0, 0
		return
	}
	switch {
	case stats.Factor < 750:
		r.BadWindows, r.GoodWindows = min(2, r.BadWindows+1), 0
		if h.State == Healthy && r.BadWindows >= 2 {
			h.State, h.Reason = Degraded, "degraded_window"
		}
	case stats.Factor >= 900:
		r.GoodWindows, r.BadWindows = min(2, r.GoodWindows+1), 0
		if h.State == Degraded && r.GoodWindows >= 2 {
			if h.RecoveryStep > 0 && h.RecoveryStep < 4 {
				h.RecoveryStep++
			} else {
				h.State, h.Reason, h.Backoff = Healthy, "healthy", 0
			}
			r.GoodWindows = 0
		}
	default:
		r.GoodWindows, r.BadWindows = 0, 0
	}
}

func (h *Health) currentHealthBucket(now time.Time) *HealthBucket {
	r := h.Rolling
	start := maxTime(now.UTC(), h.healthWindowClockFloor()).Truncate(healthBucketDuration)
	h.trimHealthWindowBuckets(start)
	if len(r.Buckets) == 0 || !r.Buckets[len(r.Buckets)-1].Start.Equal(start) {
		r.Buckets = append(r.Buckets, HealthBucket{Start: start})
	}
	return &r.Buckets[len(r.Buckets)-1]
}

func (h *Health) recordHealthBucket(o Observation, now time.Time, passive bool) {
	bucket := h.currentHealthBucket(now)
	bucket.Samples++
	if o.Outcome != Success {
		bucket.Failures++
	}
	if o.TimedOut {
		bucket.Timeouts++
	}
	if passive {
		bucket.PassiveSamples++
	}
}

func (h *Health) ApplyWithWindow(target Target, o Observation, now time.Time, probe, passive bool, policy *HealthWindowConfig, ttftTargetMillis int64) error {
	if policy == nil {
		return h.Apply(target, o, now, probe)
	}
	if err := validateObservation(o, target, now); err != nil {
		return err
	}
	if o.FailureClass != "" {
		return errors.New("classified dispatch failure requires persistent hold handling")
	}
	if !validHealthWindow(*policy, ttftTargetMillis) || probe && passive {
		return errors.New("invalid dispatch health window policy or observation origin")
	}
	if h.CredentialRevision != target.CredentialRevision {
		return errors.New("dispatch credential revision mismatch")
	}
	clockBehind := now.Before(h.healthWindowClockFloor())
	h.AdvanceHealthWindow(now, policy, ttftTargetMillis)
	confirmedAt := maxTime(now.UTC(), h.healthWindowClockFloor())
	if passive && policy.ProtocolFacts {
		h.recordProtocolFacts(o, now)
	}
	if o.Outcome == Unknown || o.Outcome == LocalRejection || o.Outcome == ClientCanceled || !o.ProviderEvidence {
		if probe && h.State == Probing {
			h.trip(confirmedAt, "probe_unattributed", 0)
			h.Rolling.GoodWindows, h.Rolling.BadWindows = 0, 0
			h.UpdatedAt = maxTime(h.UpdatedAt, confirmedAt)
		}
		return nil
	}
	h.recordHealthBucket(o, now, passive)
	h.UpdatedAt = maxTime(h.UpdatedAt, confirmedAt)
	if h.AuthBlocked {
		return nil
	}
	switch o.Outcome {
	case UpstreamAuth:
		h.AuthBlocked = true
		h.trip(confirmedAt, "credential_rejected", 0)
	case UpstreamRateLimit:
		h.trip(confirmedAt, "rate_limited", time.Duration(o.RetryAfterSeconds)*time.Second)
	case UpstreamFailure:
		h.ConsecutiveFailures++
		h.ProbeSuccesses = 0
		if h.ConsecutiveFailures >= 5 || h.State == Probing {
			h.trip(confirmedAt, "upstream_failure", 0)
		}
	case Success:
		h.ConsecutiveFailures = 0
		if !clockBehind {
			if o.LatencyMillis > 0 {
				if h.LatencyMillis == 0 {
					h.LatencyMillis = o.LatencyMillis
				} else {
					h.LatencyMillis = (h.LatencyMillis*4 + o.LatencyMillis) / 5
				}
			}
			if o.TTFTMillis > 0 {
				if h.hasFreshTTFT(now) {
					h.TTFTMillis = (h.TTFTMillis*4 + o.TTFTMillis) / 5
				} else {
					h.TTFTMillis = o.TTFTMillis
				}
				h.TTFTObservedAt = now.UTC()
			}
			if probe && h.State == Probing {
				h.ProbeSuccesses++
				if h.ProbeSuccesses >= 2 {
					h.State, h.Reason, h.RecoveryStep = Degraded, "probe_recovered", 1
					h.Rolling.CompletedThrough = nextFullHealthMinute(now)
				} else {
					h.State, h.Reason, h.CoolUntil = Tripped, "probe_confirm_required", confirmedAt.Add(30*time.Second)
					h.Rolling.CooldownStartedAt, h.Rolling.CooldownUntil = confirmedAt, h.CoolUntil
				}
				h.Rolling.GoodWindows, h.Rolling.BadWindows = 0, 0
			}
		}
	}
	stats := h.HealthWindowReport(now, *policy, ttftTargetMillis).Hot
	// 探针恢复后的成功不能用旧失败重新熔断；新失败仍立即执行比例门禁。
	checkFailureRatio := o.Outcome == UpstreamFailure || h.State == Healthy || h.RecoveryStep == 0
	if checkFailureRatio && (h.State == Healthy || h.State == Degraded) && stats.Samples >= policy.MinSamples && stats.Failures*2 >= stats.Samples {
		h.trip(confirmedAt, "failure_window", 0)
	}
	if h.State == Tripped || h.State == Probing {
		h.Rolling.GoodWindows, h.Rolling.BadWindows = 0, 0
	}
	return nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
