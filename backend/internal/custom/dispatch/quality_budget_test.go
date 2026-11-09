package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/quality"
	"github.com/stretchr/testify/require"
)

func qualityDeferralTestConfig(t *testing.T, deferral *QualityDeferralConfig) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "baseline.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"schema_version":1,"revision":"uncalibrated","templates":[]}`), 0600))
	target := qualityTestTarget()
	return Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, SnapshotFile: filepath.Join(t.TempDir(), "snapshot.json"), SnapshotTTLSeconds: 30, PollIntervalMilliseconds: 5000, Targets: []Target{target},
		Quality: &QualityConfig{BaselineFile: path, Bindings: []QualityBinding{{PlanID: 1, TargetRef: target.Ref, Kind: quality.KindQuiz}}, Deferral: deferral}}
}

func qualityDeferralResults(n int, at time.Time) []LegacyQualityResult {
	results := make([]LegacyQualityResult, n)
	for i := range results {
		results[i] = LegacyQualityResult{ResultID: int64(i + 1), PlanID: 1, AccountID: 1, TargetRef: "a", Model: "model", Kind: quality.KindQuiz, QuestionID: fmt.Sprint(i % 2),
			Prompt: "question", ExpectedAnswer: "expected", Response: "answer", Verdict: "fail", Status: "success", StartedAt: at.Add(time.Duration(i) * time.Second), FinishedAt: at.Add(time.Duration(i) * time.Second)}
	}
	return results
}

// steppingClock advances by step on every read, modelling slow scoring.
func steppingClock(start time.Time, step time.Duration) func() time.Time {
	now := start
	return func() time.Time {
		now = now.Add(step)
		return now
	}
}

func TestQualityDeferralConfigBoundsAndCompatibility(t *testing.T) {
	disabled, err := normalizeConfig(qualityDeferralTestConfig(t, nil))
	require.NoError(t, err)
	raw, err := json.Marshal(disabled.Quality.version())
	require.NoError(t, err)
	require.NotContains(t, string(raw), "Deferral", "disabled deferral keeps the config digest unchanged")
	raw, err = json.Marshal(disabled.Quality)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "deferral")

	valid := QualityDeferralConfig{BudgetMilliseconds: 2000, TTLMarginMilliseconds: 3000, MaxPending: 100}
	enabled, err := normalizeConfig(qualityDeferralTestConfig(t, &valid))
	require.NoError(t, err)
	raw, err = json.Marshal(enabled.Quality.version())
	require.NoError(t, err)
	require.Contains(t, string(raw), `"Deferral":{"budget_milliseconds":2000,"ttl_margin_milliseconds":3000,"max_pending":100}`)

	for name, change := range map[string]func(*QualityDeferralConfig){
		"zero_budget":        func(d *QualityDeferralConfig) { d.BudgetMilliseconds = 0 },
		"zero_margin":        func(d *QualityDeferralConfig) { d.TTLMarginMilliseconds = 0 },
		"zero_pending":       func(d *QualityDeferralConfig) { d.MaxPending = 0 },
		"margin_beyond_ttl":  func(d *QualityDeferralConfig) { d.TTLMarginMilliseconds = 25000 },
		"budget_beyond_ttl":  func(d *QualityDeferralConfig) { d.BudgetMilliseconds = 22001 },
		"pending_beyond_max": func(d *QualityDeferralConfig) { d.MaxPending = 1001 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := valid
			change(&changed)
			_, err := normalizeConfig(qualityDeferralTestConfig(t, &changed))
			require.ErrorContains(t, err, "deferral")
		})
	}
	edge := QualityDeferralConfig{BudgetMilliseconds: 22000, TTLMarginMilliseconds: 3000, MaxPending: 1000}
	_, err = normalizeConfig(qualityDeferralTestConfig(t, &edge))
	require.NoError(t, err, "budget may use the whole window left by poll and margin")

	var decoded Config
	require.Error(t, decodeJSON([]byte(`{"quality":{"deferral":{"budget_milliseconds":1,"ttl_margin_milliseconds":1,"max_pending":1,"extra":1}}}`), &decoded))
}

func TestQualityScoringWindowPriority(t *testing.T) {
	cfg := qualityDeferralTestConfig(t, &QualityDeferralConfig{BudgetMilliseconds: 100, TTLMarginMilliseconds: 3000, MaxPending: 10})
	start := time.Unix(1000, 0)
	window := newQualityScoringWindow(cfg, start, start.Add(time.Second))
	require.Equal(t, start.Add(22*time.Second), window.ttl, "TTL 30s minus poll 5s minus margin 3s")
	require.Empty(t, window.deferral(0, start.Add(5*time.Second)))
	require.Empty(t, window.deferral(0, start.Add(2*time.Second)), "the oldest result is admitted after the budget")
	require.Equal(t, QualityDeferredBudget, window.deferral(1, start.Add(1100*time.Millisecond)))
	require.Equal(t, QualityDeferredTTLMargin, window.deferral(0, start.Add(22*time.Second)), "the snapshot margin overrides the progress floor")
	require.Equal(t, QualityDeferredTTLMargin, window.deferral(1, start.Add(22*time.Second)), "the snapshot margin takes priority over the budget")
	require.Empty(t, qualityScoringWindow{}.deferral(1000, start.Add(time.Hour)), "disabled deferral never stops scoring")
}

func TestQualityDeferralRetriesPendingWithoutDuplicates(t *testing.T) {
	cfg, err := normalizeConfig(qualityDeferralTestConfig(t, &QualityDeferralConfig{BudgetMilliseconds: 3, TTLMarginMilliseconds: 3000, MaxPending: 4}))
	require.NoError(t, err)
	start := time.Unix(1000, 0)
	results := qualityDeferralResults(10, start.Add(-time.Hour))
	known := map[int64]string{}
	var order []int64
	for round := 0; round < 10 && len(known) < len(results); round++ {
		roundStart := start.Add(time.Duration(round) * time.Minute)
		window := newQualityScoringWindow(cfg, roundStart, roundStart)
		scored, ingest, err := scoreQualityResults(context.Background(), cfg, results, known, window, steppingClock(roundStart, time.Millisecond))
		require.NoError(t, err)
		require.NotEmpty(t, scored, "every round within the snapshot margin makes progress")
		require.Equal(t, len(results)-len(known)-len(scored), ingest.Pending)
		if ingest.Pending > 0 {
			require.Equal(t, QualityDeferredBudget, ingest.Deferral)
		}
		require.Equal(t, ingest.Pending > 4, ingest.BacklogExceeded)
		for _, s := range scored {
			_, duplicate := known[s.source.ResultID]
			require.False(t, duplicate, "retried rounds never rescore recorded results")
			known[s.source.ResultID], err = qualitySourceDigest(s.source)
			require.NoError(t, err)
			order = append(order, s.source.ResultID)
		}
	}
	require.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, order, "pending results are consumed oldest first")

	results[0].Response = "changed"
	_, _, err = scoreQualityResults(context.Background(), cfg, results, known, qualityScoringWindow{}, time.Now)
	require.ErrorIs(t, err, ErrQualityConflict, "deferral keeps the source conflict fence")
}

func TestQualityDeferralTTLMarginDefersEverything(t *testing.T) {
	cfg, err := normalizeConfig(qualityDeferralTestConfig(t, &QualityDeferralConfig{BudgetMilliseconds: 1000, TTLMarginMilliseconds: 3000, MaxPending: 4}))
	require.NoError(t, err)
	start := time.Unix(1000, 0)
	results := qualityDeferralResults(6, start.Add(-time.Hour))
	window := newQualityScoringWindow(cfg, start.Add(-22*time.Second), start)
	scored, ingest, err := scoreQualityResults(context.Background(), cfg, results, map[int64]string{}, window, func() time.Time { return start })
	require.NoError(t, err)
	require.Empty(t, scored)
	require.Equal(t, QualityIngest{Pending: 6, Deferral: QualityDeferredTTLMargin, BacklogExceeded: true}, ingest)
}

func TestQualityDisabledDeferralScoresEverything(t *testing.T) {
	cfg, err := normalizeConfig(qualityDeferralTestConfig(t, nil))
	require.NoError(t, err)
	start := time.Unix(1000, 0)
	results := qualityDeferralResults(20, start.Add(-time.Hour))
	window := newQualityScoringWindow(cfg, start.Add(-time.Hour), start)
	scored, ingest, err := scoreQualityResults(context.Background(), cfg, results, map[int64]string{}, window, steppingClock(start, time.Minute))
	require.NoError(t, err)
	require.Len(t, scored, 20)
	require.Equal(t, QualityIngest{}, ingest)
	raw, err := json.Marshal(RoundResult{Revision: 1, QualityResults: 20})
	require.NoError(t, err)
	require.JSONEq(t, `{"revision":1,"candidates":0,"probes":0,"unknown":0,"quality_results":20}`, string(raw))
}

func TestQualityBacklogReportKeepsFactorNeutral(t *testing.T) {
	target := qualityTestTarget()
	other := target
	other.Ref = "b"
	now := time.Unix(10000, 0)
	recorded := qualityTestTrend(target, 0)
	reports := qualityReports([]Target{target, other}, recorded[:4])
	results := qualityDeferralResults(3, now.Add(-time.Minute))
	results[2].Kind = quality.KindCharArt
	digest, err := qualitySourceDigest(results[0])
	require.NoError(t, err)
	require.NoError(t, attachQualityBacklog(reports, results, map[int64]string{1: digest}, 1, now))
	for _, report := range reports {
		require.Equal(t, 1000, report.FactorMilli, "unscored backlog is never low-quality evidence")
		require.NotNil(t, report.PendingSamples)
		require.True(t, *report.BacklogExceeded)
	}
	require.Equal(t, quality.KindCharArt, reports[0].Kind)
	require.Equal(t, 1, *reports[0].PendingSamples)
	require.Equal(t, int64(58), *reports[0].OldestPendingSeconds)
	require.Equal(t, 1, *reports[1].PendingSamples)
	require.Equal(t, int64(59), *reports[1].OldestPendingSeconds)
	require.Zero(t, *reports[2].PendingSamples)
	require.Nil(t, reports[2].OldestPendingSeconds)

	digest, err = qualitySourceDigest(results[1])
	require.NoError(t, err)
	require.ErrorIs(t, attachQualityBacklog(reports, results, map[int64]string{1: digest}, 1, now), ErrQualityConflict)

	raw, err := json.Marshal(qualityReports([]Target{target}, nil)[0])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "pending", "reports without deferral keep their legacy fields")
}
