package dispatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var invalidationRoute = Route{"pool", "policy-1", "gpt-fixture", "region-a"}

func credentialInvalidation(credential, lineage string, floor, sequence, holdRevision int64) Invalidation {
	scope := HealthHoldScope{Kind: "credential", ProviderRef: "provider", CredentialRef: credential}
	return Invalidation{HoldID: healthHoldID("core-fixture", FailureCredentialBanned, scope), HoldRevision: holdRevision,
		Class: FailureCredentialBanned, Scope: scope, PublishLineage: lineage, FloorRevision: floor, Sequence: sequence}
}

func selectedRefs(selector *Selector, now time.Time, rounds int) map[string]int {
	refs := map[string]int{}
	for range rounds {
		refs[selector.Select(invalidationRoute, now).CandidateRef]++
	}
	return refs
}

func TestDispatchInvalidationRemovesOnlyScopedTargetsUntilSnapshotReflectsHold(t *testing.T) {
	now := time.Now()
	selector := NewSelector("core-fixture")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 10), now))
	applied, err := selector.ApplyInvalidations("lineage-a", []Invalidation{credentialInvalidation("credential-a", "lineage-a", 10, 1, 1)})
	require.NoError(t, err)
	require.Equal(t, 1, applied)
	require.Equal(t, map[string]int{"b": 6}, selectedRefs(selector, now, 6), "only the held credential is removed")
	entries, candidates := selector.InvalidationCounts()
	require.Equal(t, [2]int{1, 1}, [2]int{entries, candidates})

	decision, mismatch := selector.SelectShadow(invalidationRoute, 1, now)
	require.Equal(t, int64(2), decision.AccountID)
	require.Equal(t, MismatchHealth, mismatch, "shadow comparison classifies the removal as health")

	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 10), now), "a repeated floor snapshot does not revive")
	require.Equal(t, map[string]int{"b": 4}, selectedRefs(selector, now, 4))
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 11), now))
	require.Equal(t, map[string]int{"a": 2, "b": 2}, selectedRefs(selector, now, 4), "a snapshot past the floor already carries the hold")
	entries, candidates = selector.InvalidationCounts()
	require.Equal(t, [2]int{0, 0}, [2]int{entries, candidates})
}

func TestDispatchInvalidationAccountModelRegionScopeHasNoCollateral(t *testing.T) {
	now := time.Now()
	snapshot := epochSnapshot(now, 5)
	other := snapshot.Candidates[0]
	other.Ref, other.Region = "a-other-region", "region-b"
	snapshot.Candidates = append(snapshot.Candidates, other)
	selector := NewSelector("core-fixture")
	require.NoError(t, selector.UpdateEpoch("lineage-a", snapshot, now))
	scope := HealthHoldScope{Kind: "account_model_region", AccountID: 1, Model: "gpt-fixture", Region: "region-a"}
	entry := Invalidation{HoldID: healthHoldID("core-fixture", FailureCapabilityDenied, scope), HoldRevision: 1,
		Class: FailureCapabilityDenied, Scope: scope, PublishLineage: "lineage-a", FloorRevision: 5, Sequence: 1}
	_, err := selector.ApplyInvalidations("lineage-a", []Invalidation{entry})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"b": 3}, selectedRefs(selector, now, 3))
	decision := selector.Select(Route{"pool", "policy-1", "gpt-fixture", "region-b"}, now)
	require.Equal(t, "a-other-region", decision.CandidateRef, "the same account in another region stays eligible")
}

func TestDispatchInvalidationOutOfOrderDuplicateAndStaleOnlyTighten(t *testing.T) {
	now := time.Now()
	selector := NewSelector("core-fixture")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 10), now))
	apply := func(entry Invalidation) int {
		applied, err := selector.ApplyInvalidations("lineage-a", []Invalidation{entry})
		require.NoError(t, err)
		return applied
	}
	require.Equal(t, 1, apply(credentialInvalidation("credential-a", "lineage-a", 20, 2, 2)))
	require.Equal(t, 0, apply(credentialInvalidation("credential-a", "lineage-a", 20, 2, 2)), "duplicate")
	require.Equal(t, 0, apply(credentialInvalidation("credential-a", "lineage-a", 12, 1, 1)), "older hold revision and floor")
	require.Equal(t, 1, apply(credentialInvalidation("credential-a", "lineage-a", 15, 3, 3)), "newer hold revision keeps the higher floor")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 16), now))
	require.Equal(t, map[string]int{"b": 2}, selectedRefs(selector, now, 2), "the floor never moves backwards")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 21), now))
	require.Equal(t, map[string]int{"a": 1, "b": 1}, selectedRefs(selector, now, 2))
}

func TestDispatchInvalidationCrossLineage(t *testing.T) {
	now := time.Now()
	t.Run("new server lineage blocks until the edge reaches it", func(t *testing.T) {
		selector := NewSelector("core-fixture")
		require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 100), now))
		_, err := selector.ApplyInvalidations("lineage-b", []Invalidation{credentialInvalidation("credential-a", "lineage-b", 3, 1, 1)})
		require.NoError(t, err)
		require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 101), now))
		require.Equal(t, map[string]int{"b": 2}, selectedRefs(selector, now, 2), "an older lineage revision cannot clear it")
		require.NoError(t, selector.UpdateEpoch("lineage-b", epochSnapshot(now, 3), now))
		require.Equal(t, map[string]int{"b": 2}, selectedRefs(selector, now, 2))
		require.NoError(t, selector.UpdateEpoch("lineage-b", epochSnapshot(now, 4), now))
		require.Equal(t, map[string]int{"a": 1, "b": 1}, selectedRefs(selector, now, 2))
	})
	t.Run("old lineage entries are absorbed and do not block", func(t *testing.T) {
		selector := NewSelector("core-fixture")
		require.NoError(t, selector.UpdateEpoch("lineage-b", epochSnapshot(now, 5), now))
		_, err := selector.ApplyInvalidations("lineage-b", []Invalidation{credentialInvalidation("credential-a", "lineage-a", 500, 1, 1)})
		require.NoError(t, err)
		require.Equal(t, map[string]int{"a": 1, "b": 1}, selectedRefs(selector, now, 2))
	})
	t.Run("a third lineage entry stays while the edge lags the server", func(t *testing.T) {
		selector := NewSelector("core-fixture")
		require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 5), now))
		_, err := selector.ApplyInvalidations("lineage-c", []Invalidation{credentialInvalidation("credential-a", "lineage-b", 9, 1, 1)})
		require.NoError(t, err)
		require.Equal(t, map[string]int{"b": 2}, selectedRefs(selector, now, 2), "unknown ordering against the edge lineage blocks")
		require.NoError(t, selector.UpdateEpoch("lineage-c", epochSnapshot(now, 1), now))
		require.Equal(t, map[string]int{"a": 1, "b": 1}, selectedRefs(selector, now, 2), "the server lineage snapshot absorbs it")
	})
	t.Run("an old lineage entry cannot replace the current one", func(t *testing.T) {
		selector := NewSelector("core-fixture")
		require.NoError(t, selector.UpdateEpoch("lineage-b", epochSnapshot(now, 5), now))
		_, err := selector.ApplyInvalidations("lineage-b", []Invalidation{credentialInvalidation("credential-a", "lineage-b", 5, 2, 1)})
		require.NoError(t, err)
		applied, err := selector.ApplyInvalidations("lineage-b", []Invalidation{credentialInvalidation("credential-a", "lineage-a", 900, 3, 7)})
		require.NoError(t, err)
		require.Zero(t, applied)
		require.Equal(t, map[string]int{"b": 2}, selectedRefs(selector, now, 2))
	})
}

func TestDispatchInvalidationKeepsInflightAndStrictPermits(t *testing.T) {
	now := time.Now()
	snapshot := epochSnapshot(now, 10)
	selector := NewSelector("core-fixture")
	require.NoError(t, selector.UpdateEpoch("lineage-a", snapshot, now))
	permits, err := NewPermitSet([]Permit{permitFor(snapshot.Candidates[0]), permitFor(snapshot.Candidates[1])})
	require.NoError(t, err)
	var releases []func()
	for range 2 {
		decision, release := selector.Acquire(invalidationRoute, permits, now)
		require.NotNil(t, release, decision.Reason)
		releases = append(releases, release)
	}
	_, err = selector.ApplyInvalidations("lineage-a", []Invalidation{credentialInvalidation("credential-a", "lineage-a", 10, 1, 1)})
	require.NoError(t, err)
	for range 4 {
		decision, release := selector.Acquire(invalidationRoute, permits, now)
		require.Equal(t, "b", decision.CandidateRef, "a permitted but invalidated target is not reselected")
		release()
	}
	only, err := NewPermitSet([]Permit{permitFor(snapshot.Candidates[0])})
	require.NoError(t, err)
	decision, release := selector.Acquire(invalidationRoute, only, now)
	require.Nil(t, release)
	require.Equal(t, "no_eligible_route", decision.Reason)
	for _, release := range releases {
		release()
	}
	require.Empty(t, selector.active, "in-flight placeholders release normally after invalidation")
}

func TestDispatchInvalidationSeededSelectionExcludesTargets(t *testing.T) {
	now := time.Now()
	selector := NewSelectorWithSeed("core-fixture", SelectionSeed{GatewayInstance: "gateway-a", ShardRef: "shard-a"})
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 10), now))
	_, err := selector.ApplyInvalidations("lineage-a", []Invalidation{credentialInvalidation("credential-b", "lineage-a", 10, 1, 1)})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"a": 4}, selectedRefs(selector, now, 4))
}

func TestDispatchInvalidationRejectsInvalidUpdates(t *testing.T) {
	now := time.Now()
	valid := credentialInvalidation("credential-a", "lineage-a", 1, 1, 1)
	for name, mutate := range map[string]func(*Invalidation){
		"hold id":       func(i *Invalidation) { i.HoldID = "hold:other" },
		"class scope":   func(i *Invalidation) { i.Class = FailureQuotaExhausted },
		"hold revision": func(i *Invalidation) { i.HoldRevision = 0 },
		"lineage":       func(i *Invalidation) { i.PublishLineage = "" },
		"floor":         func(i *Invalidation) { i.FloorRevision = -1 },
		"sequence":      func(i *Invalidation) { i.Sequence = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			entry := valid
			mutate(&entry)
			_, err := NewSelector("core-fixture").ApplyInvalidations("lineage-a", []Invalidation{entry})
			require.Error(t, err)
		})
	}
	_, err := NewSelector("core-other").ApplyInvalidations("lineage-a", []Invalidation{valid})
	require.Error(t, err, "hold ids bind the core instance")
	_, err = NewSelector("core-fixture").ApplyInvalidations("", nil)
	require.Error(t, err)
	file := NewSelector("core-fixture")
	require.NoError(t, file.Update(testSnapshot(now), now))
	_, err = file.ApplyInvalidations("lineage-a", []Invalidation{valid})
	require.ErrorContains(t, err, "epoch snapshots")
	require.Equal(t, "selected", file.Select(invalidationRoute, now).Reason)
}

func TestDispatchInvalidationLimitRejectsWholeUpdate(t *testing.T) {
	now := time.Now()
	selector := NewSelector("core-fixture")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 1), now))
	entries := make([]Invalidation, 0, MaxInvalidations)
	for i := range MaxInvalidations {
		scope := HealthHoldScope{Kind: "account_model_region", AccountID: int64(i + 100), Model: "gpt-fixture", Region: "region-a"}
		entries = append(entries, Invalidation{HoldID: healthHoldID("core-fixture", FailureCapabilityDenied, scope), HoldRevision: 1,
			Class: FailureCapabilityDenied, Scope: scope, PublishLineage: "lineage-a", FloorRevision: 1, Sequence: int64(i + 1)})
	}
	_, err := selector.ApplyInvalidations("lineage-a", entries)
	require.NoError(t, err)
	_, err = selector.ApplyInvalidations("lineage-a", []Invalidation{credentialInvalidation("credential-a", "lineage-a", 1, 9999, 1)})
	require.ErrorContains(t, err, "limit")
	require.Equal(t, map[string]int{"a": 1, "b": 1}, selectedRefs(selector, now, 2), "a rejected update leaves the overlay unchanged")
	_, err = selector.ApplyInvalidations("lineage-a", append(entries, entries[0]))
	require.Error(t, err)
}
