package dispatch

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDispatchLoadSnapshotBoundsAndAliases(t *testing.T) {
	now := time.Now().UTC()
	a := InitialHealth(testTarget("a", 1), now).Candidate(testTarget("a", 1))
	b := a
	b.Ref = "alias"
	snapshot := Snapshot{Version: 1, CoreInstance: "core", Revision: 1, GeneratedAt: now, ExpiresAt: now.Add(30 * time.Second), Candidates: []Candidate{a, b}}
	base := snapshot
	require.NoError(t, snapshot.applyLoad(LoadSample{ObservedAt: now, Accounts: map[int64]int{1: 7}}, 5*time.Second, now))
	require.Equal(t, now.Add(5*time.Second), snapshot.ExpiresAt)
	for _, candidate := range snapshot.Candidates {
		require.Equal(t, 7, candidate.Active)
	}
	selector := NewSelector("core")
	require.NoError(t, selector.Update(snapshot, now))
	route := Route{a.PoolRef, a.PolicyRevision, a.Model, a.Region}
	require.Zero(t, selector.Select(route, now.Add(6*time.Second)).AccountID)
	for name, sample := range map[string]LoadSample{
		"missing":  {ObservedAt: now, Accounts: map[int64]int{}},
		"extra":    {ObservedAt: now, Accounts: map[int64]int{1: 0, 2: 0}},
		"negative": {ObservedAt: now, Accounts: map[int64]int{1: -1}},
		"overflow": {ObservedAt: now, Accounts: map[int64]int{1: 100001}},
		"stale":    {ObservedAt: now.Add(-5 * time.Second), Accounts: map[int64]int{1: 0}},
		"future":   {ObservedAt: now.Add(6 * time.Second), Accounts: map[int64]int{1: 0}},
	} {
		t.Run(name, func(t *testing.T) { s := base; require.Error(t, s.applyLoad(sample, 5*time.Second, now)) })
	}
}

func TestDispatchLoadSnapshotCannotOutliveSample(t *testing.T) {
	now := time.Now().UTC()
	snapshot := Snapshot{Version: 1, CoreInstance: "core", Revision: 1, GeneratedAt: now, ExpiresAt: now.Add(5 * time.Second), LoadObservedAt: now, LoadExpiresAt: now.Add(5 * time.Second), Candidates: []Candidate{}}
	require.NoError(t, snapshot.Validate(now))
	snapshot.ExpiresAt = now.Add(6 * time.Second)
	require.Error(t, snapshot.Validate(now))
	snapshot.LoadExpiresAt = time.Time{}
	require.Error(t, snapshot.Validate(now))
	snapshot.LoadObservedAt = time.Time{}
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "load_observed_at")
	require.NotContains(t, string(raw), "load_expires_at")
}

func TestDispatchLoadSnapshotRemovedAccountDoesNotBlock(t *testing.T) {
	now := time.Now().UTC()
	a := testTarget("active", 1)
	b := testTarget("removed", 2)
	b.Enabled = false
	snapshot := Snapshot{Version: 1, CoreInstance: "core", Revision: 1, GeneratedAt: now, ExpiresAt: now.Add(30 * time.Second),
		Candidates: []Candidate{InitialHealth(a, now).Candidate(a), InitialHealth(b, now).Candidate(b)}}
	require.NoError(t, snapshot.applyLoad(LoadSample{ObservedAt: now, Accounts: map[int64]int{1: 3}}, 5*time.Second, now))
	require.Equal(t, 3, snapshot.Candidates[0].Active)
	require.Zero(t, snapshot.Candidates[1].EffectiveWeight)
	require.NoError(t, snapshot.Validate(now))
}
