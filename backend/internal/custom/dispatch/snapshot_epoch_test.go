package dispatch

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func epochSnapshot(now time.Time, revision int64) Snapshot {
	snapshot := testSnapshot(now)
	snapshot.Revision = revision
	return snapshot
}

func TestDispatchSelectorEpochSameLineageKeepsMonotonicRules(t *testing.T) {
	now := time.Now()
	selector := NewSelector("core-fixture")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 10), now))
	require.ErrorContains(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 9), now), "rollback")
	conflict := epochSnapshot(now, 10)
	conflict.Candidates[0].EffectiveWeight = 50
	require.ErrorContains(t, selector.UpdateEpoch("lineage-a", conflict, now), "conflict")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 10), now), "same epoch and digest is idempotent")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 11), now))
	require.Equal(t, int64(11), selector.Select(Route{"pool", "policy-1", "gpt-fixture", "region-a"}, now).Revision)
}

func TestDispatchSelectorEpochNewLineageResetsAndRemembersWatermarks(t *testing.T) {
	now := time.Now()
	route := Route{"pool", "policy-1", "gpt-fixture", "region-a"}
	selector := NewSelector("core-fixture")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 100), now))
	first := selector.Select(route, now)
	require.Equal(t, "selected", first.Reason)
	require.NoError(t, selector.UpdateEpoch("lineage-b", epochSnapshot(now, 3), now), "a new lineage may restart revisions")
	fresh := NewSelector("core-fixture")
	require.NoError(t, fresh.UpdateEpoch("lineage-b", epochSnapshot(now, 3), now))
	require.Equal(t, fresh.Select(route, now).CandidateRef, selector.Select(route, now).CandidateRef, "SWRR state resets on a new lineage")
	require.ErrorContains(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 100), now), "epoch rollback")
	require.ErrorContains(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 50), now), "epoch rollback")
	require.Equal(t, int64(3), selector.Select(route, now).Revision, "rejected epochs keep the last accepted snapshot")
	require.NoError(t, selector.UpdateEpoch("lineage-a", epochSnapshot(now, 101), now))
	require.ErrorContains(t, selector.UpdateEpoch("lineage-b", epochSnapshot(now, 3), now), "epoch rollback")
}

func TestDispatchSelectorEpochLineageLimitAndSourceMixing(t *testing.T) {
	now := time.Now()
	selector := NewSelector("core-fixture")
	for i := range MaxSnapshotLineages {
		require.NoError(t, selector.UpdateEpoch(fmt.Sprintf("lineage-%d", i), epochSnapshot(now, int64(i+1)), now))
	}
	require.ErrorContains(t, selector.UpdateEpoch("lineage-overflow", epochSnapshot(now, 99), now), "limit")
	require.NoError(t, selector.UpdateEpoch("lineage-0", epochSnapshot(now, 99), now), "known lineages still advance")
	require.ErrorContains(t, selector.Update(epochSnapshot(now, 200), now), "mixes")
	require.ErrorContains(t, selector.UpdateEpoch("bad lineage", epochSnapshot(now, 300), now), "lineage")

	file := NewSelector("core-fixture")
	require.NoError(t, file.Update(epochSnapshot(now, 5), now))
	require.ErrorContains(t, file.UpdateEpoch("lineage-a", epochSnapshot(now, 6), now), "mixes")
	require.ErrorContains(t, file.Update(epochSnapshot(now, 4), now), "rollback", "file mode keeps its revision rule")
}
