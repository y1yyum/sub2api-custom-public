package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testTarget(ref string, account int64) Target {
	return Target{Ref: ref, AccountID: account, CredentialRef: "credential-" + ref, CredentialRevision: 1, ProviderRef: "provider", PoolRef: "pool", PolicyRevision: "policy-1", Model: "gpt-fixture", Region: "region-a", BaseWeight: 100, MaxInflight: 10, Enabled: true}
}

func testSnapshot(now time.Time) Snapshot {
	return Snapshot{Version: 1, CoreInstance: "core-fixture", Revision: 1, GeneratedAt: now, ExpiresAt: now.Add(30 * time.Second), Candidates: []Candidate{
		{Target: testTarget("a", 1), State: Healthy, EffectiveWeight: 100, Reason: "healthy"},
		{Target: testTarget("b", 2), State: Healthy, EffectiveWeight: 100, Reason: "healthy"},
	}}
}

func TestDispatchSelectorIsolationAndEligibility(t *testing.T) {
	now := time.Now().UTC()
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	tests := map[string]func(*Candidate){
		"pool":        func(c *Candidate) { c.PoolRef = "another-pool" },
		"policy":      func(c *Candidate) { c.PolicyRevision = "policy-2" },
		"model":       func(c *Candidate) { c.Model = "different-model" },
		"region":      func(c *Candidate) { c.Region = "region-b" },
		"disabled":    func(c *Candidate) { c.Enabled = false; c.EffectiveWeight = 0 },
		"tripped":     func(c *Candidate) { c.State = Tripped; c.EffectiveWeight = 0 },
		"probing":     func(c *Candidate) { c.State = Probing; c.EffectiveWeight = 0 },
		"zero_weight": func(c *Candidate) { c.EffectiveWeight = 0 },
		"full":        func(c *Candidate) { c.Active = c.MaxInflight },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := testSnapshot(now)
			change(&snapshot.Candidates[0])
			selector := NewSelector("core-fixture")
			require.NoError(t, selector.Update(snapshot, now))
			for range 10 {
				decision := selector.Select(route, now)
				require.Equal(t, int64(2), decision.AccountID)
				require.Equal(t, "selected", decision.Reason)
			}
			snapshot.Revision++
			change(&snapshot.Candidates[1])
			require.NoError(t, selector.Update(snapshot, now))
			require.Equal(t, "no_eligible_route", selector.Select(route, now).Reason)
		})
	}
}

func TestDispatchSelectorWeightedDistributionAndBoundedState(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	snapshot.Candidates[0].EffectiveWeight = 30
	snapshot.Candidates[1].EffectiveWeight = 70
	selector := NewSelector("core-fixture")
	require.NoError(t, selector.Update(snapshot, now))
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	counts := map[int64]int{}
	for range 100 {
		counts[selector.Select(route, now).AccountID]++
	}
	require.Equal(t, 30, counts[1])
	require.Equal(t, 70, counts[2])
	for i := range 1000 {
		selector.Select(Route{PoolRef: strings.Repeat("x", i%128)}, now)
	}
	require.LessOrEqual(t, len(selector.weights), len(snapshot.Candidates))
}

func TestDispatchSnapshotRefreshPreservesWeightedProgress(t *testing.T) {
	now := time.Now().UTC()
	selector := NewSelector("core-fixture")
	counts := map[int64]int{}
	for revision := int64(1); revision <= 10; revision++ {
		snapshot := testSnapshot(now)
		snapshot.Revision = revision
		snapshot.Candidates[0].EffectiveWeight = 30
		snapshot.Candidates[1].EffectiveWeight = 70
		require.NoError(t, selector.Update(snapshot, now))
		counts[selector.Select(Route{"pool", "policy-1", "gpt-fixture", "region-a"}, now).AccountID]++
	}
	require.Equal(t, 3, counts[1])
	require.Equal(t, 7, counts[2])
}

func TestDispatchSnapshotVersionsExpiryAndCopy(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	selector := NewSelector("core-fixture")
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	require.Equal(t, "snapshot_unavailable", selector.Select(route, now).Reason)
	require.NoError(t, selector.Update(snapshot, now))
	require.NoError(t, selector.Update(snapshot, now))
	snapshot.Candidates[0].AccountID = 999
	require.Error(t, selector.Update(snapshot, now), "same revision must not replace a different payload")
	for range 4 {
		require.NotEqual(t, int64(999), selector.Select(route, now).AccountID)
	}
	snapshot.Revision = 2
	require.NoError(t, selector.Update(snapshot, now))
	snapshot.Revision = 1
	require.Error(t, selector.Update(snapshot, now))
	snapshot.Revision = 3
	snapshot.CoreInstance = "other-core"
	require.Error(t, selector.Update(snapshot, now))
	require.Equal(t, "snapshot_expired", selector.Select(route, now.Add(30*time.Second)).Reason)
	require.Equal(t, "snapshot_expired", selector.Select(route, now.Add(-6*time.Second)).Reason)
}

func TestDispatchSnapshotFileBoundary(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "snapshot.json")
	snapshot := testSnapshot(now)
	require.NoError(t, WriteSnapshot(path, snapshot, now))
	loaded, err := LoadSnapshot(path, now)
	require.NoError(t, err)
	require.Equal(t, snapshot, loaded)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	body, err := json.Marshal(snapshot)
	require.NoError(t, err)
	for name, raw := range map[string]string{
		"duplicate":  strings.Replace(string(body), `"version":1`, `"version":1,"version":1`, 1),
		"case_alias": strings.Replace(string(body), `"version":1`, `"Version":1`, 1),
		"unknown":    strings.Replace(string(body), `"version":1`, `"version":1,"secret":"placeholder"`, 1),
		"trailing":   string(body) + ` {}`,
		"null":       `null`,
		"too_large":  strings.Repeat(" ", MaxSnapshotBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(path, []byte(raw), 0600))
			_, err := LoadSnapshot(path, now)
			require.Error(t, err)
		})
	}
	require.NoError(t, os.Remove(path))
	_, err = LoadSnapshot(path, now)
	require.Error(t, err)
}

func TestDispatchSnapshotRejectsInvalidCandidates(t *testing.T) {
	now := time.Now().UTC()
	for name, change := range map[string]func(*Snapshot){
		"expired":          func(s *Snapshot) { s.ExpiresAt = now },
		"long_ttl":         func(s *Snapshot) { s.ExpiresAt = now.Add(31 * time.Second) },
		"future":           func(s *Snapshot) { s.GeneratedAt = now.Add(6 * time.Second) },
		"revision":         func(s *Snapshot) { s.Revision = 0 },
		"duplicate":        func(s *Snapshot) { s.Candidates[1].Ref = s.Candidates[0].Ref },
		"negative":         func(s *Snapshot) { s.Candidates[0].EffectiveWeight = -1 },
		"too_heavy":        func(s *Snapshot) { s.Candidates[0].EffectiveWeight = 101 },
		"tripped_weight":   func(s *Snapshot) { s.Candidates[0].State = Tripped },
		"unknown_state":    func(s *Snapshot) { s.Candidates[0].State = "unknown" },
		"missing_revision": func(s *Snapshot) { s.Candidates[0].CredentialRevision = 0 },
	} {
		t.Run(name, func(t *testing.T) { s := testSnapshot(now); change(&s); require.Error(t, s.Validate(now)) })
	}
}

func TestDispatchSelectorConcurrentPublishAndChoose(t *testing.T) {
	now := time.Now().UTC()
	selector := NewSelector("core-fixture")
	require.NoError(t, selector.Update(testSnapshot(now), now))
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 1000 {
				d := selector.Select(Route{"pool", "policy-1", "gpt-fixture", "region-a"}, now)
				if d.AccountID != 1 && d.AccountID != 2 {
					t.Errorf("unexpected decision: %+v", d)
					return
				}
			}
		})
	}
	for revision := int64(2); revision <= 20; revision++ {
		s := testSnapshot(now)
		s.Revision = revision
		require.NoError(t, selector.Update(s, now))
	}
	workers.Wait()
}
