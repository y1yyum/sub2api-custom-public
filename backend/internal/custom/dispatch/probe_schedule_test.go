package dispatch

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDispatchProbeScheduleIdleAndNativeIdentity(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	target := testTarget("a", 1)
	policy := ProbeScheduleConfig{30, 300, 20}
	health := InitialHealth(target, now)
	require.False(t, probeDueAt("core", target, health, policy, time.Time{}, now).After(now), "first claim is immediately eligible")
	last := now.Add(-20 * time.Second)
	due := probeDueAt("core", target, health, policy, last, now)
	require.False(t, due.Before(last.Add(30*time.Second)))
	require.False(t, due.After(last.Add(33*time.Second)))
	for _, shift := range []time.Duration{0, time.Millisecond, 5 * time.Second} {
		require.Equal(t, due, probeDueAt("core", target, health, policy, last, now.Add(shift)), "polling never redraws a due time")
	}
	alias := target
	alias.Ref, alias.PoolRef, alias.CredentialRef, alias.CredentialRevision = "alias", "other-pool", "new-credential", 2
	alias.ProviderRef, alias.QuotaRef, alias.PolicyRevision = "other-provider", "quota", "policy-2"
	require.True(t, due.Equal(probeDueAt("core", alias, health, policy, last.In(time.FixedZone("offset", 3600)), now)))
	health.CoolUntil = now.Add(2 * time.Minute)
	require.Equal(t, health.CoolUntil, probeDueAt("core", target, health, policy, last, now))
	future := now.Add(time.Minute)
	require.True(t, probeDueAt("core", target, InitialHealth(target, now), policy, future, now).After(future))
}

func TestDispatchProbeScheduleBusyRequiresRecentTrustedPassive(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	target := testTarget("a", 1)
	policy := ProbeScheduleConfig{30, 300, 20}
	window := &HealthWindowConfig{MinSamples: 20}
	last := now.Add(-100 * time.Second)
	for name, origin := range map[string]struct {
		probe, passive, evidence bool
		outcome                  Outcome
		count                    int
	}{
		"passive":  {false, true, true, Success, 20},
		"probe":    {true, false, true, Success, 20},
		"import":   {false, false, true, Success, 20},
		"unknown":  {false, true, true, Unknown, 20},
		"local":    {false, true, true, LocalRejection, 20},
		"unproven": {false, true, false, Success, 20},
		"few":      {false, true, true, Success, 19},
	} {
		t.Run(name, func(t *testing.T) {
			health := InitialHealth(target, now)
			for range origin.count {
				o := testObservation(target, now, origin.outcome)
				o.ProviderEvidence = origin.evidence
				require.NoError(t, health.ApplyWithWindow(target, o, now, origin.probe, origin.passive, window, 1000))
			}
			due := probeDueAt("core", target, health, policy, last, now)
			if name == "passive" {
				require.False(t, due.Before(last.Add(300*time.Second)))
				require.False(t, due.After(last.Add(330*time.Second)))
				for _, state := range []State{Healthy, Degraded} {
					health.State = state
					require.Equal(t, due, probeDueAt("core", target, health, policy, last, now))
				}
				later := now.Add(time.Minute)
				require.False(t, probeDueAt("core", target, health, policy, last, later).After(later), "expired traffic cannot postpone idle probes")
				health.State, health.Backoff, health.CoolUntil = Tripped, 0, now
				require.Equal(t, now, probeDueAt("core", target, health, policy, last, now), "recovery ignores busy traffic")
			} else {
				require.False(t, due.Before(last.Add(30*time.Second)))
				require.False(t, due.After(last.Add(33*time.Second)))
			}
		})
	}
}

func TestDispatchProbeScheduleRecoveryJitterBounds(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	target := testTarget("a", 1)
	policy := ProbeScheduleConfig{30, 3600, 20}
	for _, state := range []State{Tripped, Probing} {
		for _, backoff := range []int{1, 2, 3, 4, 5, 6, 1000000} {
			base := min(30*time.Second*time.Duration(1<<min(backoff-1, 5)), 10*time.Minute)
			for _, retryAfter := range []time.Duration{0, 75 * time.Second, 599 * time.Second, 600 * time.Second} {
				health := InitialHealth(target, now)
				health.State, health.Backoff = state, backoff
				health.CoolUntil = now.Add(max(base, retryAfter))
				health.Rolling = &RollingHealth{CooldownStartedAt: now, CooldownUntil: health.CoolUntil}
				due := probeDueAt("core", target, health, policy, now, now)
				require.False(t, due.Before(health.CoolUntil))
				require.False(t, due.After(health.CoolUntil.Add(max(base, retryAfter)/10)))
				require.False(t, due.After(now.Add(10*time.Minute)))
				require.Equal(t, due, probeDueAt("core", target, health, policy, now, due.Add(time.Second)))
				if health.CoolUntil.Equal(now.Add(10 * time.Minute)) {
					require.Equal(t, health.CoolUntil, due, "no jitter can extend an already capped cooldown")
				}
				alias := target
				alias.Ref, alias.CredentialRevision = "alias", 2
				require.Equal(t, due, probeDueAt("core", alias, health, policy, now, now))
			}
		}
	}
	// A retained Retry-After beyond the normal cap must never be shortened.
	health := InitialHealth(target, now)
	health.State, health.Backoff, health.CoolUntil = Tripped, 6, now.Add(11*time.Minute)
	health.Rolling = &RollingHealth{CooldownStartedAt: now, CooldownUntil: health.CoolUntil}
	require.Equal(t, health.CoolUntil, probeDueAt("core", target, health, policy, now, now))
}

func TestDispatchProbeScheduleStableCooldownMetadata(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	target := testTarget("a", 1)
	policy := ProbeScheduleConfig{30, 300, 20}
	window := &HealthWindowConfig{MinSamples: 20}
	for _, cooldown := range []time.Duration{30 * time.Second, 120 * time.Second, 599 * time.Second, 600 * time.Second} {
		health := InitialHealth(target, now)
		health.State, health.Backoff, health.CoolUntil = Tripped, 3, now.Add(cooldown)
		health.Rolling = &RollingHealth{ActivatedAt: now, AdvancedAt: now, CompletedThrough: now,
			CooldownStartedAt: now, CooldownUntil: health.CoolUntil}
		due := probeDueAt("core", target, health, policy, now.Add(-time.Minute), now)
		for _, shift := range []time.Duration{time.Second, 10 * time.Second, 20 * time.Second} {
			o := testObservation(target, now.Add(shift), Success)
			require.NoError(t, health.ApplyWithWindow(target, o, o.At, false, true, window, 1000))
			require.Equal(t, due, probeDueAt("core", target, health, policy, now.Add(-time.Minute), o.At), "passive success must not move the trip anchor or redraw jitter")
		}
		if cooldown == 600*time.Second {
			require.Equal(t, health.CoolUntil, due)
		}
		health.Rolling.CooldownUntil = health.CoolUntil.Add(time.Second)
		require.Equal(t, health.CoolUntil, probeDueAt("core", target, health, policy, now, now), "stale metadata cannot jitter a different cooldown")
		health.Rolling.CooldownUntil, health.Rolling.CooldownStartedAt = health.CoolUntil, time.Time{}
		require.Equal(t, health.CoolUntil, probeDueAt("core", target, health, policy, now, now))
		health.Rolling = nil
		require.Equal(t, health.CoolUntil, probeDueAt("core", target, health, policy, now, now), "legacy cooldowns preserve their original floor")
	}
}

func TestDispatchProbeScheduleRecoveryResetAndClockRollback(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	target := testTarget("a", 1)
	policy := ProbeScheduleConfig{30, 3600, 20}
	health := InitialHealth(target, now)
	health.State, health.Backoff, health.CoolUntil = Tripped, 0, now
	for _, last := range []time.Time{{}, now.Add(-time.Second), now} {
		require.Equal(t, now, probeDueAt("core", target, health, policy, last, now), "release/config reset cannot wait for a busy interval")
	}
	future := now.Add(time.Minute)
	require.Equal(t, future.Add(30*time.Second), probeDueAt("core", target, health, policy, future, now))
	health.Backoff, health.CoolUntil = 2, now.Add(60*time.Second)
	require.False(t, probeDueAt("core", target, health, policy, future.Add(time.Minute), now).Before(future.Add(90*time.Second)))
}

func TestDispatchProbeScheduleCanonicalFairOrder(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	targets := []Target{testTarget("c", 3), testTarget("a", 1), testTarget("b", 2)}
	original := slices.Clone(targets)
	first := ProbeOrder(targets, "core", now)
	reversed := slices.Clone(targets)
	slices.Reverse(reversed)
	require.Equal(t, first, ProbeOrder(reversed, "core", now))
	require.Equal(t, first, ProbeOrder(targets, "core", now.Add(29*time.Second)))
	require.Equal(t, first, ProbeOrder(targets, "core", now.In(time.FixedZone("offset", 3600))))
	seen := map[string]bool{}
	for slot := range len(targets) {
		order := ProbeOrder(targets, "core", now.Add(time.Duration(slot)*30*time.Second))
		seen[order[0].Ref] = true
		require.ElementsMatch(t, targets, order)
	}
	require.Len(t, seen, len(targets), "each candidate leads within one bounded rotation")
	require.Equal(t, original, targets)
	first[0].Ref = "modified"
	require.Equal(t, original, targets, "ordering owns its returned copy")
	require.Nil(t, ProbeOrder(nil, "core", now))
	require.Equal(t, targets[:1], ProbeOrder(targets[:1], "core", now))
	for _, at := range []time.Time{time.Unix(-31, 0), time.Unix(-1, 0), {}} {
		require.ElementsMatch(t, targets, ProbeOrder(targets, "core", at))
	}
}

func TestDispatchProbeScheduleDailyDomainFairness(t *testing.T) {
	for _, count := range []int{4, 43, 67} {
		t.Run(fmt.Sprintf("domains-%d", count), func(t *testing.T) {
			targets := make([]Target, count)
			for i := range targets {
				targets[i] = testTarget(fmt.Sprintf("target-%03d", i), int64(i+1))
			}
			original := slices.Clone(targets)
			for _, hour := range []int{0, 6, 23} {
				start := time.Date(2026, 11, 7, hour, 17, 0, 0, time.UTC)
				seen := map[int64]int{}
				for day := range count {
					at := start.AddDate(0, 0, day)
					order := ProbeOrder(targets, "daily-core", at)
					seen[order[0].AccountID]++
					restarted := slices.Clone(targets)
					slices.Reverse(restarted)
					require.Equal(t, order, ProbeOrder(restarted, "daily-core", at.In(time.FixedZone("offset", -7*3600))), "restart and local timezone cannot change UTC priority")
					require.Equal(t, order, ProbeOrder(targets, "daily-core", at.Add(29*time.Second)), "polling inside one slot cannot change priority")
				}
				require.Len(t, seen, count, "a daily request budget of one must eventually serve every native domain")
				for _, times := range seen {
					require.Equal(t, 1, times)
				}
			}
			require.Equal(t, original, targets)
		})
	}
}

func TestDispatchProbeScheduleAliasesCannotBuyDailyPriority(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	base := []Target{testTarget("a", 1), testTarget("b", 2), testTarget("c", 3), testTarget("d", 4)}
	withAliases := slices.Clone(base)
	for i := range 19 {
		alias := base[0]
		alias.Ref, alias.PoolRef = fmt.Sprintf("alias-%02d", i), fmt.Sprintf("pool-%02d", i)
		alias.CredentialRef, alias.CredentialRevision = fmt.Sprintf("credential-%02d", i), int64(i+1)
		alias.ProviderRef = fmt.Sprintf("provider-%02d", i)
		withAliases = append(withAliases, alias)
	}
	original := slices.Clone(withAliases)
	for _, daily := range []bool{false, true} {
		seen := map[int64]int{}
		for step := range len(base) {
			at := now.Add(time.Duration(step) * 30 * time.Second)
			if daily {
				at = now.AddDate(0, 0, step)
			}
			order := ProbeOrder(withAliases, "core", at)
			require.Equal(t, ProbeOrder(base, "core", at)[0].AccountID, order[0].AccountID, "adding aliases must not alter native-domain priority")
			seen[order[0].AccountID]++
			refs := []string{}
			closed := map[int64]bool{}
			var previous int64
			for _, target := range order {
				if target.AccountID != previous {
					if previous != 0 {
						closed[previous] = true
					}
					require.False(t, closed[target.AccountID], "aliases must be contiguous within their native-domain group")
					previous = target.AccountID
				}
				if target.AccountID == base[0].AccountID {
					refs = append(refs, target.Ref)
				}
			}
			require.True(t, slices.IsSorted(refs), "aliases expand by stable ref order")
		}
		require.Len(t, seen, len(base))
		for _, times := range seen {
			require.Equal(t, 1, times, "alias count cannot buy extra leading positions")
		}
	}
	require.Equal(t, original, withAliases)
}

func TestDispatchProbeScheduleNativeGroupsIncludeModelAndRegion(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	a := testTarget("a", 1)
	model, region, other := a, a, testTarget("other", 2)
	model.Ref, model.Model = "model", "other-model"
	region.Ref, region.Region = "region", "other-region"
	alias := a
	alias.Ref, alias.PoolRef = "alias", "alias-pool"
	base := []Target{a, model, region, other}
	withAlias := append(slices.Clone(base), alias)
	seen := map[string]bool{}
	for day := range len(base) {
		at := now.AddDate(0, 0, day)
		first, aliased := ProbeOrder(base, "core", at)[0], ProbeOrder(withAlias, "core", at)[0]
		key := fmt.Sprintf("%d/%s/%s", first.AccountID, first.Model, first.Region)
		require.Equal(t, key, fmt.Sprintf("%d/%s/%s", aliased.AccountID, aliased.Model, aliased.Region))
		seen[key] = true
	}
	require.Len(t, seen, 4, "different model/region scopes of one account are distinct native probe domains")
}
