package dispatch

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDispatchShadowMismatchReasons(t *testing.T) {
	now := time.Now().UTC()
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	for _, test := range []struct {
		name   string
		change func(*Snapshot)
		reason MismatchReason
	}{
		{"absent", func(s *Snapshot) { s.Candidates = s.Candidates[:1] }, MismatchExpectedAccountAbsent},
		{"pool", func(s *Snapshot) { s.Candidates[1].PoolRef = "other" }, MismatchRoute},
		{"policy", func(s *Snapshot) { s.Candidates[1].PolicyRevision = "other" }, MismatchRoute},
		{"model", func(s *Snapshot) { s.Candidates[1].Model = "other" }, MismatchRoute},
		{"region", func(s *Snapshot) { s.Candidates[1].Region = "other" }, MismatchRoute},
		{"disabled", func(s *Snapshot) {
			s.Candidates[1].Enabled, s.Candidates[1].EffectiveWeight = false, 0
		}, MismatchDisabled},
		{"disabled_before_health", func(s *Snapshot) {
			s.Candidates[1].Enabled, s.Candidates[1].EffectiveWeight, s.Candidates[1].State = false, 0, Tripped
		}, MismatchDisabled},
		{"tripped", func(s *Snapshot) {
			s.Candidates[1].State, s.Candidates[1].EffectiveWeight = Tripped, 0
		}, MismatchHealth},
		{"probing", func(s *Snapshot) {
			s.Candidates[1].State, s.Candidates[1].EffectiveWeight = Probing, 0
		}, MismatchHealth},
		{"zero_weight_before_capacity", func(s *Snapshot) {
			s.Candidates[1].EffectiveWeight, s.Candidates[1].Active = 0, s.Candidates[1].MaxInflight
		}, MismatchZeroWeight},
		{"degraded_zero_weight", func(s *Snapshot) {
			s.Candidates[1].State, s.Candidates[1].EffectiveWeight = Degraded, 0
		}, MismatchZeroWeight},
		{"capacity", func(s *Snapshot) { s.Candidates[1].Active = s.Candidates[1].MaxInflight }, MismatchCapacity},
		{"above_capacity", func(s *Snapshot) { s.Candidates[1].Active = s.Candidates[1].MaxInflight + 1 }, MismatchCapacity},
		{"swrr_choice", func(*Snapshot) {}, MismatchSWRRChoice},
		{"degraded_choice", func(s *Snapshot) { s.Candidates[1].State = Degraded }, MismatchSWRRChoice},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := testSnapshot(now)
			snapshot.Candidates[1].EffectiveWeight = snapshot.Candidates[0].EffectiveWeight
			snapshot.Candidates[1].Reason = "opaque-candidate-reason"
			test.change(&snapshot)
			selector := NewSelector(snapshot.CoreInstance)
			require.NoError(t, selector.Update(snapshot, now))
			decision, reason := selector.SelectShadow(route, 2, now)
			require.Equal(t, Decision{AccountID: 1, CandidateRef: "a", Revision: snapshot.Revision, Reason: "selected"}, decision)
			require.Equal(t, test.reason, reason)
		})
	}
}

func TestDispatchShadowMismatchFiltersAliasesInStages(t *testing.T) {
	now := time.Now().UTC()
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	for _, test := range []struct {
		name          string
		first, second func(*Candidate)
		reason        MismatchReason
	}{
		{"route_then_disabled", func(c *Candidate) { c.Region = "other" }, func(c *Candidate) {
			c.Enabled, c.EffectiveWeight = false, 0
		}, MismatchDisabled},
		{"disabled_then_health", func(c *Candidate) { c.Enabled, c.EffectiveWeight = false, 0 }, func(c *Candidate) {
			c.State, c.EffectiveWeight = Tripped, 0
		}, MismatchHealth},
		{"health_then_weight", func(c *Candidate) { c.State, c.EffectiveWeight = Probing, 0 }, func(c *Candidate) {
			c.EffectiveWeight = 0
		}, MismatchZeroWeight},
		{"weight_then_capacity", func(c *Candidate) { c.EffectiveWeight = 0 }, func(c *Candidate) {
			c.Active = c.MaxInflight
		}, MismatchCapacity},
		{"capacity_then_eligible", func(c *Candidate) { c.Active = c.MaxInflight }, func(*Candidate) {}, MismatchSWRRChoice},
		{"disabled_then_eligible", func(c *Candidate) { c.Enabled, c.EffectiveWeight = false, 0 }, func(*Candidate) {}, MismatchSWRRChoice},
		{"hold_then_eligible", func(c *Candidate) { c.Reason, c.EffectiveWeight = "manual_hold", 0 }, func(*Candidate) {}, MismatchSWRRChoice},
		{"route_eligible_then_health", func(c *Candidate) { c.Model = "other" }, func(c *Candidate) {
			c.State, c.EffectiveWeight = Tripped, 0
		}, MismatchHealth},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				snapshot := testSnapshot(now)
				first := snapshot.Candidates[1]
				first.EffectiveWeight = 100
				second := first
				second.Ref = "c"
				test.first(&first)
				test.second(&second)
				if reverse {
					first.Ref, second.Ref = second.Ref, first.Ref
				}
				snapshot.Candidates = []Candidate{snapshot.Candidates[0], first, second}
				selector := NewSelector(snapshot.CoreInstance)
				require.NoError(t, selector.Update(snapshot, now))
				decision, reason := selector.SelectShadow(route, 2, now)
				require.Equal(t, int64(1), decision.AccountID)
				require.Equal(t, test.reason, reason, "reverse=%v", reverse)
			}
		})
	}
}

func TestDispatchShadowPreservesDecisionAndSelectsOnce(t *testing.T) {
	now := time.Now().UTC()
	snapshot := testSnapshot(now)
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	ordinary, shadow := NewSelector(snapshot.CoreInstance), NewSelector(snapshot.CoreInstance)
	for _, selector := range []*Selector{ordinary, shadow} {
		require.NoError(t, selector.Update(snapshot, now))
	}
	for i := range 40 {
		decision := ordinary.Select(route, now)
		expected := int64(1)
		if i%2 == 0 {
			expected = decision.AccountID
		}
		actual, reason := shadow.SelectShadow(route, expected, now)
		require.Equal(t, decision, actual)
		if actual.AccountID == expected {
			require.Empty(t, reason)
		} else {
			require.Equal(t, MismatchSWRRChoice, reason)
		}
		require.Equal(t, ordinary.weights, shadow.weights)
	}
	for _, test := range []struct {
		name     string
		selector *Selector
		route    Route
		now      time.Time
		reason   string
	}{
		{"missing", NewSelector(snapshot.CoreInstance), route, now, "snapshot_unavailable"},
		{"expired", shadow, route, snapshot.ExpiresAt, "snapshot_expired"},
		{"future", shadow, route, now.Add(-6 * time.Second), "snapshot_expired"},
		{"no_route", shadow, Route{PoolRef: "other"}, now, "no_eligible_route"},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, reason := test.selector.SelectShadow(test.route, 2, test.now)
			require.Zero(t, decision.AccountID)
			require.Equal(t, test.reason, decision.Reason)
			require.Empty(t, reason)
		})
	}
}

func TestDispatchShadowComparisonUsesSelectedSnapshotConcurrently(t *testing.T) {
	now := time.Now().UTC()
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	selector := NewSelector("core-fixture")
	publish := func(revision int64) {
		snapshot := testSnapshot(now)
		snapshot.Revision = revision
		if revision%2 == 1 {
			snapshot.Candidates = snapshot.Candidates[:1]
		} else {
			snapshot.Candidates[1].Enabled, snapshot.Candidates[1].EffectiveWeight = false, 0
		}
		require.NoError(t, selector.Update(snapshot, now))
	}
	publish(1)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 1000 {
				decision, reason := selector.SelectShadow(route, 2, now)
				want := MismatchDisabled
				if decision.Revision%2 == 1 {
					want = MismatchExpectedAccountAbsent
				}
				if decision.AccountID != 1 || decision.Reason != "selected" || reason != want {
					t.Errorf("decision and mismatch span snapshots: %+v, reason=%s, want=%s", decision, reason, want)
					return
				}
			}
		})
	}
	for revision := int64(2); revision <= 100; revision++ {
		publish(revision)
	}
	workers.Wait()
}
