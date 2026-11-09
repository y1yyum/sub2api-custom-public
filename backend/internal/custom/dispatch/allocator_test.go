package dispatch

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func permitFor(candidate Candidate) Permit {
	return Permit{CandidateRef: candidate.Ref, AccountID: candidate.AccountID, CredentialRef: candidate.CredentialRef,
		CredentialRevision: candidate.CredentialRevision, ProviderRef: candidate.ProviderRef, Model: candidate.Model, MaxInflight: candidate.MaxInflight}
}

func TestDispatchPermitsValidateAndFreeze(t *testing.T) {
	now := time.Now().UTC()
	candidate := testSnapshot(now).Candidates[0]
	permit := permitFor(candidate)
	for name, modify := range map[string]func(*Permit){
		"candidate":   func(p *Permit) { p.CandidateRef = "" },
		"account":     func(p *Permit) { p.AccountID = 0 },
		"credential":  func(p *Permit) { p.CredentialRef = "" },
		"revision":    func(p *Permit) { p.CredentialRevision = 0 },
		"provider":    func(p *Permit) { p.ProviderRef = "" },
		"model":       func(p *Permit) { p.Model = "invalid model" },
		"limit_zero":  func(p *Permit) { p.MaxInflight = 0 },
		"limit_large": func(p *Permit) { p.MaxInflight = 100001 },
	} {
		t.Run(name, func(t *testing.T) {
			value := permit
			modify(&value)
			_, err := NewPermitSet([]Permit{value})
			require.Error(t, err)
		})
	}
	_, err := NewPermitSet(nil)
	require.Error(t, err)
	_, err = NewPermitSet(make([]Permit, MaxCandidates+1))
	require.Error(t, err)
	_, err = NewPermitSet([]Permit{permit, permit})
	require.Error(t, err)
	alias := permit
	alias.CandidateRef, alias.MaxInflight = "alias", permit.MaxInflight+1
	_, err = NewPermitSet([]Permit{permit, alias})
	require.Error(t, err)
	input := []Permit{permit}
	set, err := NewPermitSet(input)
	require.NoError(t, err)
	input[0].AccountID = 999
	require.Equal(t, candidate.AccountID, set.byRef[candidate.Ref].AccountID)
}

func TestDispatchAcquireCannotWidenPermit(t *testing.T) {
	now := time.Now().UTC()
	baseline := testSnapshot(now)
	set, err := NewPermitSet([]Permit{permitFor(baseline.Candidates[0])})
	require.NoError(t, err)
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	for name, modify := range map[string]func(*Candidate){
		"candidate":  func(c *Candidate) { c.Ref = "other" },
		"account":    func(c *Candidate) { c.AccountID = 999 },
		"credential": func(c *Candidate) { c.CredentialRef = "other" },
		"revision":   func(c *Candidate) { c.CredentialRevision++ },
		"provider":   func(c *Candidate) { c.ProviderRef = "other" },
		"model":      func(c *Candidate) { c.Model = "other" },
		"pool":       func(c *Candidate) { c.PoolRef = "other" },
		"policy":     func(c *Candidate) { c.PolicyRevision = "other" },
		"region":     func(c *Candidate) { c.Region = "other" },
		"disabled":   func(c *Candidate) { c.Enabled = false; c.EffectiveWeight = 0 },
		"tripped":    func(c *Candidate) { c.State = Tripped; c.EffectiveWeight = 0 },
		"probing":    func(c *Candidate) { c.State = Probing; c.EffectiveWeight = 0 },
		"weight":     func(c *Candidate) { c.EffectiveWeight = 0 },
		"full":       func(c *Candidate) { c.Active = c.MaxInflight },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := testSnapshot(now)
			modify(&snapshot.Candidates[0])
			selector := NewSelector(snapshot.CoreInstance)
			require.NoError(t, selector.Update(snapshot, now))
			decision, release := selector.Acquire(route, set, now)
			require.Zero(t, decision.AccountID)
			require.Equal(t, "no_eligible_route", decision.Reason)
			require.Nil(t, release, "healthy unpermitted B must never become fallback")
		})
	}
	selector := NewSelector(baseline.CoreInstance)
	decision, release := selector.Acquire(route, set, now)
	require.Equal(t, "snapshot_unavailable", decision.Reason)
	require.Nil(t, release)
	require.NoError(t, selector.Update(baseline, now))
	decision, release = selector.Acquire(route, set, now.Add(30*time.Second))
	require.Equal(t, "snapshot_expired", decision.Reason)
	require.Nil(t, release)
	decision, release = selector.Acquire(route, nil, now)
	require.Zero(t, decision.AccountID)
	require.Nil(t, release)
}

func TestDispatchAcquireConcurrentAccountCapAcrossAliases(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates = snapshot.Candidates[:1]
	snapshot.Candidates[0].MaxInflight = 5
	alias := snapshot.Candidates[0]
	alias.Ref, alias.Model = "alias", "another-model"
	snapshot.Candidates = append(snapshot.Candidates, alias)
	set, err := NewPermitSet([]Permit{permitFor(snapshot.Candidates[0]), permitFor(alias)})
	require.NoError(t, err)
	selector := NewSelector(snapshot.CoreInstance)
	require.NoError(t, selector.Update(snapshot, now))
	start := make(chan struct{})
	releases := make(chan func(), 200)
	var workers sync.WaitGroup
	var acquired atomic.Int64
	for i := range 200 {
		workers.Go(func() {
			<-start
			model := "gpt-fixture"
			if i%2 == 0 {
				model = "another-model"
			}
			decision, release := selector.Acquire(Route{"pool", "policy-1", model, "region-a"}, set, now)
			if decision.AccountID != 0 {
				acquired.Add(1)
				releases <- release
			}
		})
	}
	close(start)
	workers.Wait()
	require.Equal(t, int64(5), acquired.Load())
	close(releases)
	for release := range releases {
		workers.Go(func() {
			release()
			release()
		})
	}
	workers.Wait()
	require.Empty(t, selector.active)
	decision, release := selector.Acquire(Route{"pool", "policy-1", "gpt-fixture", "region-a"}, set, now)
	require.Equal(t, int64(1), decision.AccountID)
	release()
	require.Empty(t, selector.active)
}

func TestDispatchAcquirePreservesReservationsAcrossUpdates(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates = snapshot.Candidates[:1]
	snapshot.Candidates[0].MaxInflight = 1
	set, err := NewPermitSet([]Permit{permitFor(snapshot.Candidates[0])})
	require.NoError(t, err)
	selector := NewSelector(snapshot.CoreInstance)
	require.NoError(t, selector.Update(snapshot, now))
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	decision, release := selector.Acquire(route, set, now)
	require.Equal(t, int64(1), decision.AccountID)
	snapshot.Revision++
	snapshot.Candidates = nil
	require.NoError(t, selector.Update(snapshot, now))
	snapshot.Revision++
	alias := Candidate{Target: testTarget("new-alias", 1), State: Healthy, EffectiveWeight: 100, Reason: "healthy"}
	alias.MaxInflight = 1
	snapshot.Candidates = []Candidate{alias}
	require.NoError(t, selector.Update(snapshot, now))
	newSet, err := NewPermitSet([]Permit{permitFor(alias)})
	require.NoError(t, err)
	decision, denied := selector.Acquire(route, newSet, now)
	require.Zero(t, decision.AccountID, "snapshot removal must not erase an active account reservation")
	require.Nil(t, denied)
	release()
	release()
	decision, release = selector.Acquire(route, newSet, now)
	require.Equal(t, int64(1), decision.AccountID)
	release()
	require.Empty(t, selector.active)
}

func TestDispatchAcquireConservativeExternalLoadAndLimits(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates = snapshot.Candidates[:1]
	snapshot.Candidates[0].MaxInflight, snapshot.Candidates[0].Active = 10, 2
	alias := snapshot.Candidates[0]
	alias.Ref, alias.Active, alias.MaxInflight = "alias", 0, 3
	snapshot.Candidates = append(snapshot.Candidates, alias)
	p1, p2 := permitFor(snapshot.Candidates[0]), permitFor(alias)
	p1.MaxInflight, p2.MaxInflight = 10, 10
	set, err := NewPermitSet([]Permit{p1, p2})
	require.NoError(t, err)
	selector := NewSelector(snapshot.CoreInstance)
	require.NoError(t, selector.Update(snapshot, now))
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	decision, release := selector.Acquire(route, set, now)
	require.Equal(t, int64(1), decision.AccountID)
	decision, denied := selector.Acquire(route, set, now)
	require.Zero(t, decision.AccountID, "aliases share the maximum external load and minimum matching capacity")
	require.Nil(t, denied)
	release()
	p1.MaxInflight, p2.MaxInflight = 2, 2
	set, err = NewPermitSet([]Permit{p1, p2})
	require.NoError(t, err)
	decision, denied = selector.Acquire(route, set, now)
	require.Zero(t, decision.AccountID, "a larger snapshot capacity cannot widen the permit cap")
	require.Nil(t, denied)
}

func TestDispatchAcquireSnapshotAccountCapAcrossGrants(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates = snapshot.Candidates[:1]
	snapshot.Candidates[0].MaxInflight = 1
	alias := snapshot.Candidates[0]
	alias.Ref, alias.MaxInflight = "other-grant-alias", 10
	snapshot.Candidates = append(snapshot.Candidates, alias)
	a, b := permitFor(snapshot.Candidates[0]), permitFor(alias)
	a.MaxInflight = 10
	setA, err := NewPermitSet([]Permit{a})
	require.NoError(t, err)
	setB, err := NewPermitSet([]Permit{b})
	require.NoError(t, err)
	selector := NewSelector(snapshot.CoreInstance)
	require.NoError(t, selector.Update(snapshot, now))
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	decision, release := selector.Acquire(route, setA, now)
	require.Equal(t, int64(1), decision.AccountID)
	defer release()
	decision, denied := selector.Acquire(route, setB, now)
	if denied != nil {
		defer denied()
	}
	require.Zero(t, decision.AccountID, "another grant cannot widen the same account's lower snapshot cap")
	require.Nil(t, denied)
}

func TestDispatchAcquireWeightedDistribution(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates[0].EffectiveWeight, snapshot.Candidates[1].EffectiveWeight = 30, 70
	set, err := NewPermitSet([]Permit{permitFor(snapshot.Candidates[0]), permitFor(snapshot.Candidates[1])})
	require.NoError(t, err)
	selector := NewSelector(snapshot.CoreInstance)
	require.NoError(t, selector.Update(snapshot, now))
	counts := map[int64]int{}
	for range 100 {
		decision, release := selector.Acquire(Route{"pool", "policy-1", "gpt-fixture", "region-a"}, set, now)
		require.NotNil(t, release)
		counts[decision.AccountID]++
		release()
	}
	require.Equal(t, map[int64]int{1: 30, 2: 70}, counts)
}

func TestDispatchAcquireConcurrentSnapshotRefresh(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates = snapshot.Candidates[:1]
	snapshot.Candidates[0].MaxInflight = 1
	set, err := NewPermitSet([]Permit{permitFor(snapshot.Candidates[0])})
	require.NoError(t, err)
	selector := NewSelector(snapshot.CoreInstance)
	require.NoError(t, selector.Update(snapshot, now))
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	_, release := selector.Acquire(route, set, now)
	require.NotNil(t, release)
	var workers sync.WaitGroup
	var unexpected atomic.Int64
	for range 20 {
		workers.Go(func() {
			for range 100 {
				decision, denied := selector.Acquire(route, set, now)
				if decision.AccountID != 0 {
					unexpected.Add(1)
					denied()
				}
			}
		})
	}
	for revision := int64(2); revision <= 100; revision++ {
		snapshot.Revision = revision
		require.NoError(t, selector.Update(snapshot, now))
	}
	workers.Wait()
	require.Zero(t, unexpected.Load())
	release()
	require.Empty(t, selector.active)
}
