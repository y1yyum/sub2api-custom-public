package dispatch

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func selectionSeedRoute() Route {
	return Route{"pool", "policy-1", "gpt-fixture", "region-a"}
}

func selectionSeedPermits(t *testing.T, snapshot Snapshot) *PermitSet {
	t.Helper()
	permits := make([]Permit, 0, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		permits = append(permits, permitFor(candidate))
	}
	set, err := NewPermitSet(permits)
	require.NoError(t, err)
	return set
}

func selectionSeedSequence(t *testing.T, selector *Selector, snapshot Snapshot, now time.Time, count int, acquire bool) []int64 {
	t.Helper()
	permits := selectionSeedPermits(t, snapshot)
	sequence := make([]int64, 0, count)
	for range count {
		var decision Decision
		if acquire {
			var release func()
			decision, release = selector.Acquire(selectionSeedRoute(), permits, now)
			require.NotNil(t, release)
			release()
		} else {
			decision = selector.Select(selectionSeedRoute(), now)
		}
		require.Equal(t, "selected", decision.Reason)
		sequence = append(sequence, decision.AccountID)
	}
	return sequence
}

func TestDispatchSelectionSeedValidate(t *testing.T) {
	seed := SelectionSeed{GatewayInstance: "gateway-A_1:zone.2", ShardRef: "shard-01"}
	require.NoError(t, seed.Validate())
	body, err := json.Marshal(seed)
	require.NoError(t, err)
	require.JSONEq(t, `{"gateway_instance":"gateway-A_1:zone.2","shard_ref":"shard-01"}`, string(body))
	for _, value := range []string{"", "two words", "with/slash", "换行", "newline\n", strings.Repeat("a", 129)} {
		for _, field := range []string{"gateway", "shard"} {
			t.Run(field+"/"+fmt.Sprintf("%q", value), func(t *testing.T) {
				invalid := seed
				if field == "gateway" {
					invalid.GatewayInstance = value
				} else {
					invalid.ShardRef = value
				}
				require.Error(t, invalid.Validate())
			})
		}
	}
	require.NoError(t, (SelectionSeed{strings.Repeat("a", 128), strings.Repeat("b", 128)}).Validate())
}

func TestDispatchSelectionSeedLegacyTrace(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 30, 70
	for _, acquire := range []bool{false, true} {
		t.Run(fmt.Sprintf("acquire=%t", acquire), func(t *testing.T) {
			selector := NewSelector(snapshot.CoreInstance)
			require.NoError(t, selector.Update(snapshot, now))
			require.Nil(t, selector.seed)
			require.Nil(t, selector.seedTotals)
			require.Equal(t, []int64{2, 1, 2, 2, 1, 2, 2, 2, 1, 2}, selectionSeedSequence(t, selector, snapshot, now, 10, acquire))
		})
	}
}

func TestDispatchSelectionSeedExactWeightedPhase(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 1, 3
	third := Candidate{Target: testTarget("c", 3), State: Healthy, EffectiveWeight: 3, Reason: "healthy"}
	snapshot.Candidates = append(snapshot.Candidates, third)
	seed := SelectionSeed{ShardRef: "shard-1"}
	found := false
	for i := range 1000 {
		seed.GatewayInstance = fmt.Sprintf("gw-%d", i)
		if NewSelectorWithSeed(snapshot.CoreInstance, seed).selectionPhase(selectionSeedRoute(), 7) == 4 {
			found = true
			break
		}
	}
	require.True(t, found)
	legacy := NewSelector(snapshot.CoreInstance)
	require.NoError(t, legacy.Update(snapshot, now))
	want := selectionSeedSequence(t, legacy, snapshot, now, 14, false)
	for _, acquire := range []bool{false, true} {
		t.Run(fmt.Sprintf("acquire=%t", acquire), func(t *testing.T) {
			selector := NewSelectorWithSeed(snapshot.CoreInstance, seed)
			require.NoError(t, selector.Update(snapshot, now))
			got := selectionSeedSequence(t, selector, snapshot, now, 7, acquire)
			counts := map[int64]int{}
			for _, account := range got {
				counts[account]++
			}
			require.Equal(t, map[int64]int{1: 1, 2: 3, 3: 3}, counts)
			require.Equal(t, want[4:11], got, "the initial state must be an actual SWRR phase")
		})
	}
}

func TestDispatchSelectionSeedPreservesStablePeriodScores(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates = nil
	wantCounts := map[int64]int{}
	for i, weight := range []int64{50, 1, 100, 10, 1, 1} {
		account := int64(i + 1)
		snapshot.Candidates = append(snapshot.Candidates, Candidate{
			Target: testTarget(fmt.Sprintf("r%02d", i), account),
			State:  Healthy, EffectiveWeight: weight, Reason: "healthy",
		})
		wantCounts[account] = int(weight)
	}
	seed := SelectionSeed{GatewayInstance: "gw-135", ShardRef: "shard-1"}
	const phase, period = 156, 163
	require.Equal(t, int64(phase), NewSelectorWithSeed(snapshot.CoreInstance, seed).selectionPhase(selectionSeedRoute(), period))
	legacy := NewSelector(snapshot.CoreInstance)
	require.NoError(t, legacy.Update(snapshot, now))
	want := selectionSeedSequence(t, legacy, snapshot, now, phase+period, false)[phase:]
	for _, acquire := range []bool{false, true} {
		t.Run(fmt.Sprintf("acquire=%t", acquire), func(t *testing.T) {
			selector := NewSelectorWithSeed(snapshot.CoreInstance, seed)
			require.NoError(t, selector.Update(snapshot, now))
			got := selectionSeedSequence(t, selector, snapshot, now, period, acquire)
			counts := map[int64]int{}
			for _, account := range got {
				counts[account]++
			}
			require.Equal(t, wantCounts, counts, "stable SWRR scores must not be truncated")
			require.Equal(t, want, got, "stable weights must preserve the original SWRR phase")
		})
	}
}

func TestDispatchSelectionSeedReproducibleAcrossRestartAndOrder(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 2, 9
	seed := SelectionSeed{"gateway-fixture", "shard-a"}
	first := NewSelectorWithSeed(snapshot.CoreInstance, seed)
	require.NoError(t, first.Update(snapshot, now))
	want := selectionSeedSequence(t, first, snapshot, now, 110, false)
	for _, acquire := range []bool{false, true} {
		t.Run(fmt.Sprintf("acquire=%t", acquire), func(t *testing.T) {
			restarted := NewSelectorWithSeed(snapshot.CoreInstance, seed)
			refreshed := snapshot
			refreshed.Candidates = slices.Clone(snapshot.Candidates)
			slices.Reverse(refreshed.Candidates)
			refreshed.Revision, refreshed.GeneratedAt = 42, now.Add(time.Second)
			require.NoError(t, restarted.Update(refreshed, now))
			require.Equal(t, want, selectionSeedSequence(t, restarted, snapshot, now, 110, acquire))
		})
	}
	counts := map[int64]int{}
	for _, account := range want {
		counts[account]++
	}
	require.Equal(t, map[int64]int{1: 20, 2: 90}, counts)
	seed.GatewayInstance = "mutated-caller"
	require.Equal(t, "gateway-fixture", first.seed.GatewayInstance)
}

func TestDispatchSelectionSeedWeightedStagger(t *testing.T) {
	now := time.Now().UTC()
	for _, equal := range []bool{false, true} {
		for _, field := range []string{"gateway", "shard", "core"} {
			t.Run(fmt.Sprintf("equal=%t/%s", equal, field), func(t *testing.T) {
				starts := map[int64]bool{}
				for i := range 32 {
					snapshot := testSnapshot(now)
					if !equal {
						// 总权重为奇数，两个累计值不可能以 tie 决胜。
						snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 2, 9
					}
					seed := SelectionSeed{"gateway-fixture", "shard-a"}
					identity := fmt.Sprintf("identity-%d", i)
					switch field {
					case "gateway":
						seed.GatewayInstance = identity
					case "shard":
						seed.ShardRef = identity
					case "core":
						snapshot.CoreInstance = identity
					}
					selector := NewSelectorWithSeed(snapshot.CoreInstance, seed)
					require.NoError(t, selector.Update(snapshot, now))
					starts[selector.Select(selectionSeedRoute(), now).AccountID] = true
				}
				require.Len(t, starts, 2, "different identities must stagger weighted selections, not only ties")
			})
		}
	}
}

func TestDispatchSelectionSeedSnapshotContract(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	original, err := json.Marshal(snapshot)
	require.NoError(t, err)
	legacy := NewSelector(snapshot.CoreInstance)
	seeded := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{"gateway-fixture", "shard-a"})
	require.Equal(t, "snapshot_unavailable", seeded.Select(selectionSeedRoute(), now).Reason)
	require.NoError(t, legacy.Update(snapshot, now))
	require.NoError(t, seeded.Update(snapshot, now))
	require.Equal(t, legacy.current.Load().digest, seeded.current.Load().digest)
	after, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.Equal(t, original, after, "the local seed must not affect snapshot encoding or caller slices")
	_ = seeded.Select(selectionSeedRoute(), now)
	weights, totals := maps.Clone(seeded.weights), maps.Clone(seeded.seedTotals)
	slices.Reverse(snapshot.Candidates)
	require.NoError(t, seeded.Update(snapshot, now), "digest remains canonical across candidate ordering")
	require.Equal(t, weights, seeded.weights)
	require.Equal(t, totals, seeded.seedTotals)
	snapshot.Candidates[0].EffectiveWeight--
	require.ErrorContains(t, seeded.Update(snapshot, now), "revision conflict")
	snapshot.Revision++
	require.NoError(t, seeded.Update(snapshot, now))
	snapshot.Revision--
	require.ErrorContains(t, seeded.Update(snapshot, now), "revision rollback")
	snapshot.Revision += 2
	snapshot.CoreInstance = "another-core"
	require.ErrorContains(t, seeded.Update(snapshot, now), "identity mismatch")
	for _, expired := range []time.Time{now.Add(30 * time.Second), now.Add(-6 * time.Second)} {
		require.Equal(t, "snapshot_expired", seeded.Select(selectionSeedRoute(), expired).Reason)
		decision, release := seeded.Acquire(selectionSeedRoute(), selectionSeedPermits(t, testSnapshot(now)), expired)
		require.Equal(t, "snapshot_expired", decision.Reason)
		require.Nil(t, release)
	}
}

func TestDispatchSelectionSeedRecoveryInitializesMissingWeights(t *testing.T) {
	now := time.Now().UTC()
	for name, modify := range map[string]func(*Candidate){
		"tripped": func(c *Candidate) { c.State, c.EffectiveWeight = Tripped, 0 },
		"probing": func(c *Candidate) { c.State, c.EffectiveWeight = Probing, 0 },
		"zero":    func(c *Candidate) { c.EffectiveWeight = 0 },
		"full":    func(c *Candidate) { c.Active = c.MaxInflight },
	} {
		for _, acquire := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/acquire=%t", name, acquire), func(t *testing.T) {
				seed := SelectionSeed{"gateway-fixture", "shard-a"}
				snapshot := testSnapshot(now)
				snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 2, 9
				unavailable := snapshot
				unavailable.Candidates = slices.Clone(snapshot.Candidates)
				for i := range unavailable.Candidates {
					modify(&unavailable.Candidates[i])
				}
				selector := NewSelectorWithSeed(snapshot.CoreInstance, seed)
				require.NoError(t, selector.Update(unavailable, now))
				require.Equal(t, "no_eligible_route", selector.Select(selectionSeedRoute(), now).Reason)
				decision, release := selector.Acquire(selectionSeedRoute(), selectionSeedPermits(t, snapshot), now)
				require.Equal(t, "no_eligible_route", decision.Reason)
				require.Nil(t, release)
				require.Empty(t, selector.weights)
				require.Empty(t, selector.seedTotals)
				snapshot.Revision++
				require.NoError(t, selector.Update(snapshot, now))
				fresh := NewSelectorWithSeed(snapshot.CoreInstance, seed)
				require.NoError(t, fresh.Update(snapshot, now))
				require.Equal(t, selectionSeedSequence(t, fresh, snapshot, now, 33, acquire),
					selectionSeedSequence(t, selector, snapshot, now, 33, acquire))
			})
		}
	}
}

func TestDispatchSelectionSeedEligibilityAndPermits(t *testing.T) {
	now := time.Now().UTC()
	for name, modify := range map[string]func(*Candidate){
		"pool":     func(c *Candidate) { c.PoolRef = "another" },
		"policy":   func(c *Candidate) { c.PolicyRevision = "another" },
		"model":    func(c *Candidate) { c.Model = "another" },
		"region":   func(c *Candidate) { c.Region = "another" },
		"disabled": func(c *Candidate) { c.Enabled, c.EffectiveWeight = false, 0 },
		"tripped":  func(c *Candidate) { c.State, c.EffectiveWeight = Tripped, 0 },
		"probing":  func(c *Candidate) { c.State, c.EffectiveWeight = Probing, 0 },
		"zero":     func(c *Candidate) { c.EffectiveWeight = 0 },
		"full":     func(c *Candidate) { c.Active = c.MaxInflight },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := testSnapshot(now)
			modify(&snapshot.Candidates[0])
			for _, acquire := range []bool{false, true} {
				selector := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{"gateway-fixture", "shard-a"})
				require.NoError(t, selector.Update(snapshot, now))
				require.Equal(t, slices.Repeat([]int64{2}, 20), selectionSeedSequence(t, selector, snapshot, now, 20, acquire))
			}
		})
	}
	for name, modify := range map[string]func(*Candidate){
		"candidate":  func(c *Candidate) { c.Ref = "another" },
		"account":    func(c *Candidate) { c.AccountID = 999 },
		"credential": func(c *Candidate) { c.CredentialRef = "another" },
		"revision":   func(c *Candidate) { c.CredentialRevision++ },
		"provider":   func(c *Candidate) { c.ProviderRef = "another" },
		"model":      func(c *Candidate) { c.Model = "another" },
	} {
		t.Run("permit/"+name, func(t *testing.T) {
			snapshot := testSnapshot(now)
			permits, err := NewPermitSet([]Permit{permitFor(snapshot.Candidates[0])})
			require.NoError(t, err)
			modify(&snapshot.Candidates[0])
			selector := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{"gateway-fixture", "shard-a"})
			require.NoError(t, selector.Update(snapshot, now))
			for range 20 {
				decision, release := selector.Acquire(selectionSeedRoute(), permits, now)
				require.Equal(t, "no_eligible_route", decision.Reason)
				require.Nil(t, release)
			}
			decision, release := selector.Acquire(selectionSeedRoute(), nil, now)
			require.Equal(t, "no_eligible_route", decision.Reason)
			require.Nil(t, release)
		})
	}
}

func TestDispatchSelectionSeedRefreshPreservesProgressAndReservations(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 2, 9
	selector := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{"gateway-fixture", "shard-a"})
	require.NoError(t, selector.Update(snapshot, now))
	_ = selector.Select(selectionSeedRoute(), now)
	decision, release := selector.Acquire(selectionSeedRoute(), selectionSeedPermits(t, snapshot), now)
	require.NotNil(t, release)
	defer release()
	weights, totals := maps.Clone(selector.weights), maps.Clone(selector.seedTotals)
	snapshot.Revision++
	snapshot.GeneratedAt = now.Add(time.Millisecond)
	for i := range snapshot.Candidates {
		snapshot.Candidates[i].Reason = "updated"
		snapshot.Candidates[i].State = Degraded
		snapshot.Candidates[i].Active = 1
	}
	require.NoError(t, selector.Update(snapshot, now))
	require.Equal(t, weights, selector.weights)
	require.Equal(t, totals, selector.seedTotals)
	require.Equal(t, map[int64]int{decision.AccountID: 1}, selector.active)
	snapshot.Revision++
	snapshot.Candidates = nil
	require.NoError(t, selector.Update(snapshot, now))
	require.Empty(t, selector.weights)
	require.Empty(t, selector.seedTotals)
	require.Equal(t, map[int64]int{decision.AccountID: 1}, selector.active)
	alias := Candidate{Target: testTarget("new-alias", decision.AccountID), State: Healthy, EffectiveWeight: 100, Reason: "healthy"}
	alias.MaxInflight = 1
	snapshot.Revision++
	snapshot.Candidates = []Candidate{alias}
	require.NoError(t, selector.Update(snapshot, now))
	permits := selectionSeedPermits(t, snapshot)
	denied, rejectedRelease := selector.Acquire(selectionSeedRoute(), permits, now)
	require.Zero(t, denied.AccountID)
	require.Nil(t, rejectedRelease)
	release()
	release()
	_, acceptedRelease := selector.Acquire(selectionSeedRoute(), permits, now)
	require.NotNil(t, acceptedRelease)
	acceptedRelease()
	require.Empty(t, selector.active)
}

func TestDispatchSelectionSeedRemovalAndIneligibilityRescaleDebt(t *testing.T) {
	now := time.Now().UTC()
	for name, modify := range map[string]func(*Snapshot){
		"removed": func(s *Snapshot) { s.Candidates = s.Candidates[:2] },
		"zero":    func(s *Snapshot) { s.Candidates[2].EffectiveWeight = 0 },
		"full":    func(s *Snapshot) { s.Candidates[2].Active = s.Candidates[2].MaxInflight },
		"tripped": func(s *Snapshot) { s.Candidates[2].State, s.Candidates[2].EffectiveWeight = Tripped, 0 },
	} {
		for _, acquire := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/acquire=%t", name, acquire), func(t *testing.T) {
				snapshot := testSnapshot(now)
				snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 1, 1
				snapshot.Candidates = append(snapshot.Candidates, Candidate{Target: testTarget("c", 3), State: Healthy, EffectiveWeight: 100, Reason: "healthy"})
				selector := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{"gw-371", "shard-1"})
				require.NoError(t, selector.Update(snapshot, now))
				snapshot.Revision++
				modify(&snapshot)
				require.NoError(t, selector.Update(snapshot, now))
				counts := map[int64]int{}
				for _, account := range selectionSeedSequence(t, selector, snapshot, now, 20, acquire) {
					counts[account]++
				}
				require.Equal(t, map[int64]int{1: 10, 2: 10}, counts)
			})
		}
	}
}

func TestDispatchSelectionSeedChurnRetainsBoundedDistribution(t *testing.T) {
	now := time.Now().UTC()
	for _, acquire := range []bool{false, true} {
		t.Run(fmt.Sprintf("acquire=%t", acquire), func(t *testing.T) {
			snapshot := testSnapshot(now)
			snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 30, 70
			selector := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{"gateway-fixture", "shard-a"})
			require.NoError(t, selector.Update(snapshot, now))
			for revision := int64(2); revision <= 80; revision++ {
				snapshot.Revision = revision
				snapshot.Candidates = snapshot.Candidates[:2]
				if revision%2 == 0 {
					alias := Candidate{Target: testTarget(fmt.Sprintf("alias-%d", revision), revision+2), State: Healthy, EffectiveWeight: 43, Reason: "healthy"}
					snapshot.Candidates = append(snapshot.Candidates, alias)
				}
				prior := maps.Clone(selector.weights)
				require.NoError(t, selector.Update(snapshot, now))
				require.Equal(t, prior["a"], selector.weights["a"])
				require.Equal(t, prior["b"], selector.weights["b"])
				_ = selectionSeedSequence(t, selector, snapshot, now, 17, acquire)
				require.Len(t, selector.weights, len(snapshot.Candidates))
				require.Len(t, selector.seedTotals, len(snapshot.Candidates))
				for ref, weight := range selector.weights {
					require.LessOrEqual(t, weight, 2*selector.seedTotals[ref])
					require.GreaterOrEqual(t, weight, -2*selector.seedTotals[ref])
				}
			}
			counts := map[int64]int{}
			for _, account := range selectionSeedSequence(t, selector, snapshot, now, 1430, acquire) {
				counts[account]++
			}
			require.InDelta(t, 300, counts[1], 2)
			require.InDelta(t, 700, counts[2], 2)
			require.InDelta(t, 430, counts[82], 2)
		})
	}
}

func TestDispatchSelectionSeedMaximumSnapshotAndBoundedRequests(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates = nil
	var total int64
	for i := range MaxCandidates {
		candidate := Candidate{Target: testTarget(fmt.Sprintf("candidate-%04d", i), int64(i+1)), State: Healthy, Reason: "healthy"}
		candidate.BaseWeight, candidate.EffectiveWeight = 10000, 10000-int64(i%3)
		total += candidate.EffectiveWeight
		snapshot.Candidates = append(snapshot.Candidates, candidate)
	}
	selector := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{"gateway-fixture", "shard-a"})
	require.Less(t, selector.selectionPhase(selectionSeedRoute(), total), int64(MaxCandidates))
	require.NoError(t, selector.Update(snapshot, now))
	require.Len(t, selector.weights, MaxCandidates)
	require.Len(t, selector.seedTotals, MaxCandidates)
	var sum int64
	for _, weight := range selector.weights {
		sum += weight
		require.Less(t, weight, total)
		require.Greater(t, weight, -total)
	}
	require.Zero(t, sum)
	weights, totals := maps.Clone(selector.weights), maps.Clone(selector.seedTotals)
	for i := range 1000 {
		route := selectionSeedRoute()
		route.Model = fmt.Sprintf("absent-%d", i)
		require.Equal(t, "no_eligible_route", selector.Select(route, now).Reason)
	}
	require.Equal(t, weights, selector.weights)
	require.Equal(t, totals, selector.seedTotals)
}

func TestDispatchSelectionSeedConcurrentRefreshAndAcquire(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 2, 9
	selector := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{"gateway-fixture", "shard-a"})
	require.NoError(t, selector.Update(snapshot, now))
	permits := selectionSeedPermits(t, snapshot)
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Go(func() {
			for range 200 {
				var decision Decision
				if i%2 == 0 {
					var release func()
					decision, release = selector.Acquire(selectionSeedRoute(), permits, now)
					if release == nil {
						t.Error("seeded acquire unexpectedly denied")
						return
					}
					release()
					release()
				} else {
					decision = selector.Select(selectionSeedRoute(), now)
				}
				if decision.AccountID != 1 && decision.AccountID != 2 {
					t.Errorf("unexpected decision: %+v", decision)
					return
				}
			}
		})
	}
	for revision := int64(2); revision <= 50; revision++ {
		snapshot.Revision = revision
		for i := range snapshot.Candidates {
			snapshot.Candidates[i].Active = int(revision % 2)
			snapshot.Candidates[i].EffectiveWeight = 1 + revision%9
		}
		require.NoError(t, selector.Update(snapshot, now))
	}
	workers.Wait()
	require.Empty(t, selector.active)
	require.Len(t, selector.weights, 2)
	require.Len(t, selector.seedTotals, 2)
}

func TestDispatchSelectionSeedWeightDrop(t *testing.T) {
	now := time.Now().UTC()
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	for i := range 8 {
		t.Run(fmt.Sprintf("gateway-%d", i), func(t *testing.T) {
			snapshot := testSnapshot(now)
			for j := range snapshot.Candidates {
				snapshot.Candidates[j].BaseWeight = 10000
				snapshot.Candidates[j].EffectiveWeight = 10000
			}
			selector := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{fmt.Sprintf("gateway-%d", i), "shard-a"})
			require.NoError(t, selector.Update(snapshot, now))
			snapshot.Revision++
			for j := range snapshot.Candidates {
				snapshot.Candidates[j].EffectiveWeight = 1
			}
			require.NoError(t, selector.Update(snapshot, now))
			counts := map[int64]int{}
			for range 20 {
				counts[selector.Select(route, now).AccountID]++
			}
			require.InDelta(t, 10, counts[1], 1, "weight units must follow the new eligible total")
			require.InDelta(t, 10, counts[2], 1)
		})
	}
}

func TestDispatchSelectionSeedPermitSubset(t *testing.T) {
	now := time.Now().UTC()
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	for i := range 8 {
		t.Run(fmt.Sprintf("gateway-%d", i), func(t *testing.T) {
			snapshot := testSnapshot(now)
			snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 1, 2
			heavy := Candidate{Target: testTarget("unpermitted", 3), State: Healthy, EffectiveWeight: 10000, Reason: "healthy"}
			heavy.BaseWeight = 10000
			snapshot.Candidates = append(snapshot.Candidates, heavy)
			permits, err := NewPermitSet([]Permit{permitFor(snapshot.Candidates[0]), permitFor(snapshot.Candidates[1])})
			require.NoError(t, err)
			selector := NewSelectorWithSeed(snapshot.CoreInstance, SelectionSeed{fmt.Sprintf("gateway-%d", i), "shard-a"})
			require.NoError(t, selector.Update(snapshot, now))
			counts := map[int64]int{}
			for range 30 {
				decision, release := selector.Acquire(route, permits, now)
				require.NotNil(t, release)
				counts[decision.AccountID]++
				release()
			}
			require.Zero(t, counts[3], "the heavy candidate must remain outside the permit")
			require.InDelta(t, 10, counts[1], 1, "a small permit must not inherit large route debts")
			require.InDelta(t, 20, counts[2], 1)
		})
	}
}
