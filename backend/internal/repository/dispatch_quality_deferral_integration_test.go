//go:build integration

package repository

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

func TestDispatchQualityDeferralKeepsBacklogPendingAndNeutral(t *testing.T) {
	db := dispatchDB(t)
	require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
	cfg, path := qualityStoreConfig(t, 1)
	cfg.Quality.Deferral = &dispatch.QualityDeferralConfig{BudgetMilliseconds: 1000, TTLMarginMilliseconds: 1000, MaxPending: 5}
	cfg = writeQualityStoreConfig(t, path, cfg)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	source := qualityStoreSource(dispatchNow(t, db))

	// A round that started a TTL ago has no publication margin left.
	ingest, err := store.IngestQualityRound(t.Context(), cfg, source, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, dispatch.QualityIngest{Pending: 10, Deferral: dispatch.QualityDeferredTTLMargin, BacklogExceeded: true}, ingest)
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.quality_results`))
	snapshot, err := store.PublishRuntime(t.Context(), cfg, nil)
	require.NoError(t, err)
	for _, candidate := range snapshot.Candidates {
		require.Equal(t, candidate.BaseWeight, candidate.EffectiveWeight, "pending results are not low-quality evidence")
	}
	reports, err := store.QualityReportsWithBacklog(t.Context(), cfg, source)
	require.NoError(t, err)
	require.Equal(t, "quiz", reports[1].Kind)
	require.Equal(t, 10, *reports[1].PendingSamples)
	require.GreaterOrEqual(t, *reports[1].OldestPendingSeconds, int64(0))
	require.True(t, *reports[1].BacklogExceeded)
	require.Equal(t, 1000, reports[1].FactorMilli)
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.quality_results`), "the backlog report never records scores")

	result, err := dispatch.RunRound(t.Context(), cfg, store, nil, &dispatch.RuntimeSources{Quality: source})
	require.NoError(t, err)
	require.Equal(t, 10, result.QualityResults)
	require.Zero(t, result.QualityPending)
	require.Empty(t, result.QualityDeferral)
	result, err = dispatch.RunRound(t.Context(), cfg, store, nil, &dispatch.RuntimeSources{Quality: source})
	require.NoError(t, err)
	require.Zero(t, result.QualityResults, "retried rounds do not record a result twice")
	require.Equal(t, 10, dispatchCount(t, db, `SELECT count(*) FROM dispatch.quality_results`))
	reports, err = store.QualityReportsWithBacklog(t.Context(), cfg, source)
	require.NoError(t, err)
	require.Zero(t, *reports[1].PendingSamples)
	require.Nil(t, reports[1].OldestPendingSeconds)
	require.False(t, *reports[1].BacklogExceeded)
	require.Equal(t, 850, reports[1].FactorMilli)

	_, err = store.Observe(t.Context(), "core", dispatchObservation(t, db, cfg.Targets[0], "auth", dispatch.UpstreamAuth))
	require.NoError(t, err)
	late := source.results[0]
	late.ResultID, late.Response = 300, "late"
	source.results = append(source.results, late)
	ingest, err = store.IngestQualityRound(t.Context(), cfg, source, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, ingest.Pending)
	snapshot, err = store.PublishRuntime(t.Context(), cfg, nil)
	require.NoError(t, err)
	require.Equal(t, dispatch.Tripped, snapshot.Candidates[0].State, "deferral never releases a health hold")
	require.Zero(t, snapshot.Candidates[0].EffectiveWeight)
}

// dispatchStoreLock mirrors the unexported dispatch.storeLock advisory key.
const dispatchStoreLock int64 = 813028810208

func TestDispatchQualityDeferralRoundReportsFromRoundStart(t *testing.T) {
	db := dispatchDB(t)
	require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
	cfg, path := qualityStoreConfig(t, 1)
	// The publication margin ends 500ms after the round starts.
	cfg.Quality.Deferral = &dispatch.QualityDeferralConfig{BudgetMilliseconds: 500, TTLMarginMilliseconds: 24500, MaxPending: 5}
	cfg = writeQualityStoreConfig(t, path, cfg)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	source := qualityStoreSource(dispatchNow(t, db))

	// Holding the store lock delays the round's config sync past the margin.
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock($1)`, dispatchStoreLock)
	require.NoError(t, err)
	released := make(chan error, 1)
	go func() {
		time.Sleep(time.Second)
		released <- tx.Commit()
	}()
	result, err := dispatch.RunRound(t.Context(), cfg, store, nil, &dispatch.RuntimeSources{Quality: source})
	require.NoError(t, <-released)
	require.NoError(t, err)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.NotContains(t, fields, "quality_results")
	require.Equal(t, float64(10), fields["quality_pending"])
	require.Equal(t, dispatch.QualityDeferredTTLMargin, fields["quality_deferral"])
	require.Equal(t, true, fields["quality_backlog_exceeded"])
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.quality_results`))
}
