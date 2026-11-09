//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custom/quality"
	"github.com/stretchr/testify/require"
)

type qualityFixtureSource struct {
	results []dispatch.LegacyQualityResult
	err     error
}

func (s *qualityFixtureSource) Read(context.Context, []dispatch.QualityBinding, []dispatch.Target, time.Time, int) ([]dispatch.LegacyQualityResult, error) {
	return s.results, s.err
}

// These labels exercise the operator-attestation boundary. They are synthetic
// fixtures, never calibration evidence for real model quality.
func qualityStoreBaseline(t *testing.T, path, revision string) {
	t.Helper()
	file := quality.BaselineFile{SchemaVersion: 1, Revision: revision, Calibration: &quality.CalibrationSet{Options: quality.CalibrationOptions{DatasetOrigin: "human"}}}
	var id int64
	for _, group := range []struct{ split, model, question string }{{"train", "train-model", "train"}, {"holdout", "model", "1"}, {"holdout", "model", "2"}, {"holdout", "second-model", "3"}} {
		for i := 0; i < 10; i++ {
			id++
			human := 0
			verdict := "fail"
			if i < 5 {
				human = 100
				verdict = "pass"
			}
			file.Calibration.Samples = append(file.Calibration.Samples, quality.LabeledSample{
				Sample: quality.Sample{ResultID: id, TaskID: id, Kind: "quiz", Model: group.model, QuestionID: group.question, Prompt: "fixture question " + group.question, Expected: "fixture expected " + group.question, Response: fmt.Sprintf("fixture %s %d", group.question, i), Verdict: verdict, Status: "success"},
				Split:  group.split, LabelSource: "human", Labeler: "synthetic-test-attestation", HumanScore: &human,
			})
		}
	}
	body, err := json.Marshal(file)
	require.NoError(t, err)
	baseline, err := quality.ParseBaseline(body)
	require.NoError(t, err)
	require.True(t, baseline.Reference("quiz", quality.QuestionRef("quiz", "1", "fixture question 1", "fixture expected 1"), "model").CalibratedCoverage)
	require.NoError(t, os.WriteFile(path, body, 0600))
}

func qualityStoreConfig(t *testing.T, revision int64) (dispatch.Config, string) {
	t.Helper()
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	qualityStoreBaseline(t, baseline, "fixture-baseline-1")
	a := dispatchTarget("a", 1)
	alias := a
	alias.Ref = "alias"
	alias.PoolRef = "another-pool"
	otherModel := a
	otherModel.Ref = "other-model"
	otherModel.Model = "other-model"
	cfg := dispatch.Config{Version: 1, CoreInstance: "core", ConfigRevision: revision, SnapshotFile: filepath.Join(dir, "snapshot.json"), Targets: []dispatch.Target{a, alias, otherModel}, Quality: &dispatch.QualityConfig{BaselineFile: baseline, ApplyWeight: true, Bindings: []dispatch.QualityBinding{{PlanID: 1, TargetRef: "a", Kind: "quiz"}, {PlanID: 2, TargetRef: "a", Kind: "quiz"}}}}
	path := filepath.Join(dir, "config.json")
	return writeQualityStoreConfig(t, path, cfg), path
}

func writeQualityStoreConfig(t *testing.T, path string, cfg dispatch.Config) dispatch.Config {
	t.Helper()
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0600))
	loaded, err := dispatch.LoadConfig(path)
	require.NoError(t, err)
	return loaded
}

func qualityStoreSource(at time.Time) *qualityFixtureSource {
	source := &qualityFixtureSource{}
	for q := 1; q <= 2; q++ {
		for i := 0; i < 5; i++ {
			source.results = append(source.results, dispatch.LegacyQualityResult{ResultID: int64(q*100 + i), PlanID: int64(q), AccountID: 1, TargetRef: "a", Model: "model", Kind: "quiz", QuestionID: fmt.Sprint(q), Prompt: fmt.Sprint("fixture question ", q), ExpectedAnswer: fmt.Sprint("fixture expected ", q), Response: "wrong", Verdict: "fail", Status: "success", StartedAt: at, FinishedAt: at})
		}
	}
	return source
}

func TestDispatchQualitySchemaLegacyCompatibility(t *testing.T) {
	db := dispatchDB(t)
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM pg_tables WHERE schemaname='dispatch' AND tablename='quality_results'`))
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncConfig(t.Context(), "legacy", 1, []dispatch.Target{dispatchTarget("a", 1)}, dispatch.ProbeLimits{ConfigRevision: 1}))
	_, err := store.Publish(t.Context(), "legacy", 1, time.Second)
	require.NoError(t, err)
	accountOID := dispatchCount(t, db, `SELECT 'public.accounts'::regclass::oid::bigint`)
	require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
	require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
	require.NoError(t, dispatch.ApplySchema(t.Context(), db), "legacy schema ledger remains usable")
	require.Equal(t, accountOID, dispatchCount(t, db, `SELECT 'public.accounts'::regclass::oid::bigint`))
	dispatchExec(t, db, `UPDATE dispatch.quality_schema_migrations SET checksum='changed'`)
	require.ErrorContains(t, dispatch.ApplyQualitySchema(t.Context(), db), "checksum")
}

func TestDispatchQualityStoreDeduplicationAndHealthPriority(t *testing.T) {
	db := dispatchDB(t)
	require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
	cfg, _ := qualityStoreConfig(t, 1)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	before := dispatchHealth(t, db, "core", "a")
	source := qualityStoreSource(dispatchNow(t, db))
	count, err := store.IngestQuality(t.Context(), cfg, source)
	require.NoError(t, err)
	require.Equal(t, 10, count)
	count, err = dispatch.NewStore(db).IngestQuality(t.Context(), cfg, source)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, before, dispatchHealth(t, db, "core", "a"))
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
	snapshot, err := store.PublishRuntime(t.Context(), cfg, nil)
	require.NoError(t, err)
	require.Equal(t, int64(85), snapshot.Candidates[0].EffectiveWeight)
	require.Equal(t, int64(85), snapshot.Candidates[1].EffectiveWeight)
	require.Equal(t, int64(100), snapshot.Candidates[2].EffectiveWeight)
	reports, err := store.QualityReports(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, reports, 6)
	require.Equal(t, "insufficient_evidence", reports[0].Reason, "glyph and quiz remain separate")
	require.Equal(t, 850, reports[1].FactorMilli)
	source.results[0].Response = "mutated"
	_, err = store.IngestQuality(t.Context(), cfg, source)
	require.ErrorIs(t, err, dispatch.ErrQualityConflict)
	require.Equal(t, 10, dispatchCount(t, db, `SELECT count(*) FROM dispatch.quality_results`))
	_, err = store.Observe(t.Context(), "core", dispatchObservation(t, db, cfg.Targets[0], "auth", dispatch.UpstreamAuth))
	require.NoError(t, err)
	snapshot, err = store.PublishRuntime(t.Context(), cfg, nil)
	require.NoError(t, err)
	require.Zero(t, snapshot.Candidates[0].EffectiveWeight)
	require.Equal(t, dispatch.Tripped, snapshot.Candidates[0].State)
	var leaked bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM dispatch.quality_results WHERE score::text LIKE '%mutated%' OR score::text LIKE '%fixture expected%')`).Scan(&leaked))
	require.False(t, leaked)
}

func TestDispatchQualityVersionCutoffAndSourceFailure(t *testing.T) {
	db := dispatchDB(t)
	require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
	cfg, path := qualityStoreConfig(t, 1)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	source := qualityStoreSource(dispatchNow(t, db))
	_, err := dispatch.RunRound(t.Context(), cfg, store, nil, &dispatch.RuntimeSources{Quality: source})
	require.NoError(t, err)
	before, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	source.err = errors.New("synthetic unavailable")
	_, err = dispatch.RunRound(t.Context(), cfg, store, nil, &dispatch.RuntimeSources{Quality: source})
	require.Error(t, err)
	after, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	require.Equal(t, before, after)
	old := cfg
	cfg.ConfigRevision++
	for i := range cfg.Targets {
		cfg.Targets[i].CredentialRevision++
	}
	cfg = writeQualityStoreConfig(t, path, cfg)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	source.err = nil
	_, err = store.IngestQuality(t.Context(), old, source)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	_, err = store.IngestQuality(t.Context(), cfg, source)
	require.ErrorContains(t, err, "time mismatch")
	source.results = nil
	_, err = store.IngestQuality(t.Context(), cfg, source)
	require.NoError(t, err)
	reports, err := store.QualityReports(t.Context(), cfg)
	require.NoError(t, err)
	for _, report := range reports {
		require.Equal(t, 1000, report.FactorMilli)
		require.Zero(t, report.Samples)
	}
	// Reusing a baseline name for different bytes is rejected even with a new
	// runtime revision, preserving the meaning of every retained score.
	body, err := os.ReadFile(cfg.Quality.BaselineFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfg.Quality.BaselineFile, append(body, '\n'), 0600))
	cfg.ConfigRevision++
	cfg = writeQualityStoreConfig(t, path, cfg)
	require.ErrorContains(t, store.SyncRuntimeConfig(t.Context(), cfg), "baseline revision conflict")
	qualityStoreBaseline(t, cfg.Quality.BaselineFile, "fixture-baseline-2")
	cfg = writeQualityStoreConfig(t, path, cfg)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
}

func TestDispatchQualityOldScorerCannotAffectWeights(t *testing.T) {
	db := dispatchDB(t)
	require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
	cfg, _ := qualityStoreConfig(t, 1)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	_, err := store.IngestQuality(t.Context(), cfg, qualityStoreSource(dispatchNow(t, db)))
	require.NoError(t, err)
	dispatchExec(t, db, `UPDATE dispatch.quality_results SET score=jsonb_set(score,'{result,scorer_revision}','"old-scorer"')`)
	snapshot, err := store.PublishRuntime(t.Context(), cfg, nil)
	require.NoError(t, err)
	for _, candidate := range snapshot.Candidates {
		require.Equal(t, candidate.BaseWeight, candidate.EffectiveWeight)
	}
	reports, err := store.QualityReports(t.Context(), cfg)
	require.NoError(t, err)
	for _, report := range reports {
		require.Zero(t, report.Samples)
		require.Equal(t, 1000, report.FactorMilli)
	}
}

func TestDispatchQualityOversizedSourceIsUnknown(t *testing.T) {
	for _, field := range []string{"response", "prompt", "expected"} {
		t.Run(field, func(t *testing.T) {
			db := dispatchDB(t)
			require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
			cfg, _ := qualityStoreConfig(t, 1)
			store := dispatch.NewStore(db)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			source := qualityStoreSource(dispatchNow(t, db))
			source.results = source.results[:1]
			oversized := strings.Repeat("x", quality.MaxResponseBytes+1)
			switch field {
			case "response":
				source.results[0].Response = oversized
			case "prompt":
				source.results[0].Prompt = oversized
			case "expected":
				source.results[0].ExpectedAnswer = oversized
			}
			count, err := store.IngestQuality(t.Context(), cfg, source)
			require.NoError(t, err, "source envelope accepts <=64 KiB but scoring remains bounded at 16 KiB")
			require.Equal(t, 1, count)
			var reason, hash, question string
			var confidence int
			var encoded []byte
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT score->'result'->>'reason',score->'result'->>'source_sha256',
			 score->'result'->>'question_ref',(score->'result'->>'confidence')::int,score FROM dispatch.quality_results`).Scan(&reason, &hash, &question, &confidence, &encoded))
			require.Equal(t, "input_limit", reason)
			require.Empty(t, hash)
			require.Len(t, question, 64)
			require.Zero(t, confidence)
			require.Equal(t, quality.QuestionRef("quiz", source.results[0].QuestionID, source.results[0].Prompt, source.results[0].ExpectedAnswer), question)
			require.Less(t, len(encoded), 4096, "only bounded diagnostics and hashes are stored")
			snapshot, err := store.PublishRuntime(t.Context(), cfg, nil)
			require.NoError(t, err)
			for _, candidate := range snapshot.Candidates {
				require.Equal(t, candidate.BaseWeight, candidate.EffectiveWeight)
				require.Equal(t, dispatch.Healthy, candidate.State)
			}
		})
	}
}
