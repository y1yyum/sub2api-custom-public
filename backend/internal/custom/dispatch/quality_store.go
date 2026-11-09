package dispatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/quality"
	"github.com/lib/pq"
)

var ErrQualityConflict = errors.New("dispatch quality result conflict")

type qualityEvaluation struct {
	Result         quality.Result    `json:"result"`
	Reference      quality.Reference `json:"reference"`
	BaselineDigest string            `json:"baseline_digest"`
}

type scoredQualityResult struct {
	source     LegacyQualityResult
	evaluation qualityEvaluation
}

func qualitySourceDigest(r LegacyQualityResult) (string, error) {
	r.StartedAt, r.FinishedAt = r.StartedAt.UTC(), r.FinishedAt.UTC()
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func knownQualityDigests(ctx context.Context, tx *sql.Tx, cfg Config, ids []int64) (map[int64]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT result_id,source_digest FROM dispatch.quality_results
	 WHERE core_instance=$1 AND config_revision=$2 AND result_id=ANY($3)`, cfg.CoreInstance, cfg.ConfigRevision, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	known := make(map[int64]string, len(ids))
	for rows.Next() {
		var id int64
		var digest string
		if err := rows.Scan(&id, &digest); err != nil {
			return nil, err
		}
		known[id] = digest
	}
	return known, rows.Err()
}

func (s *Store) knownQuality(ctx context.Context, cfg Config, results []LegacyQualityResult) (map[int64]string, error) {
	tx, _, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := qualityActivationAt(ctx, tx, cfg); err != nil {
		return nil, err
	}
	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.ResultID
	}
	known, err := knownQualityDigests(ctx, tx, cfg, ids)
	if err != nil {
		return nil, err
	}
	return known, tx.Commit()
}

type QualityReport struct {
	TargetRef        string `json:"target_ref"`
	Kind             string `json:"kind"`
	Samples          int    `json:"samples"`
	QualifiedSamples int    `json:"qualified_samples"`
	Plans            int    `json:"plans"`
	Questions        int    `json:"questions"`
	DriftQuestions   int    `json:"drift_questions"`
	DriftPlans       int    `json:"drift_plans"`
	FactorMilli      int    `json:"factor_milli"`
	Reason           string `json:"reason"`
	ScorerRevision   string `json:"scorer_revision"`
	// Backlog fields are present only when quality.deferral is enabled.
	PendingSamples       *int   `json:"pending_samples,omitempty"`
	OldestPendingSeconds *int64 `json:"oldest_pending_seconds,omitempty"`
	BacklogExceeded      *bool  `json:"backlog_exceeded,omitempty"`
}

func qualityActivationAt(ctx context.Context, tx *sql.Tx, cfg Config) (time.Time, error) {
	if cfg.Quality == nil || len(cfg.Quality.baselineDigest) != 64 {
		return time.Time{}, errors.New("dispatch quality is not configured")
	}
	if err := checkConfigRevision(ctx, tx, cfg.CoreInstance, cfg.ConfigRevision); err != nil {
		return time.Time{}, err
	}
	var at time.Time
	var revision int64
	var digest string
	err := tx.QueryRowContext(ctx, `SELECT config_revision,baseline_digest,activated_at FROM dispatch.quality_configs WHERE core_instance=$1`, cfg.CoreInstance).Scan(&revision, &digest, &at)
	if err != nil {
		return time.Time{}, err
	}
	if revision != cfg.ConfigRevision || digest != cfg.Quality.baselineDigest {
		return time.Time{}, ErrConfigRevision
	}
	return at.UTC(), nil
}

func (s *Store) QualityActivation(ctx context.Context, cfg Config) (time.Time, error) {
	tx, _, err := s.begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback() }()
	at, err := qualityActivationAt(ctx, tx, cfg)
	if err != nil {
		return time.Time{}, err
	}
	return at, tx.Commit()
}

func (s *Store) recordQuality(ctx context.Context, cfg Config, results []scoredQualityResult) (int, error) {
	if cfg.Quality == nil || len(results) > cfg.Quality.MaxResults {
		return 0, errors.New("invalid dispatch quality batch")
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	at, err := qualityActivationAt(ctx, tx, cfg)
	if err != nil {
		return 0, err
	}
	bindings, err := validateQualityBindings(cfg.Quality.Bindings, cfg.Targets)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.source.ResultID
	}
	known, err := knownQualityDigests(ctx, tx, cfg, ids)
	if err != nil {
		return 0, err
	}
	inserted := 0
	for _, scored := range results {
		r := scored.source
		binding, exists := bindings[r.PlanID]
		if !exists || binding.Target.Ref != r.TargetRef || binding.Target.AccountID != r.AccountID || binding.Target.Model != r.Model || binding.Binding.Kind != r.Kind ||
			r.ResultID < 1 || r.StartedAt.Before(at) || r.StartedAt.Before(now.Add(-7*24*time.Hour)) || r.FinishedAt.Before(r.StartedAt) || r.FinishedAt.After(now) {
			return 0, errors.New("dispatch quality result identity or time mismatch")
		}
		r.StartedAt, r.FinishedAt = r.StartedAt.UTC(), r.FinishedAt.UTC()
		digest, err := qualitySourceDigest(r)
		if err != nil {
			return 0, err
		}
		evaluation, err := json.Marshal(scored.evaluation)
		if err != nil {
			return 0, err
		}
		result := scored.evaluation.Result
		if scored.evaluation.BaselineDigest != cfg.Quality.baselineDigest {
			return 0, ErrConfigRevision
		}
		if result.SourceResultID != r.ResultID || result.Kind != r.Kind || result.ScorerRevision != quality.ScorerRevision || len(result.QuestionRef) != 64 || result.Score < 0 || result.Score > 100 || result.Confidence < 0 || result.Confidence > 100 {
			return 0, errors.New("invalid dispatch quality score")
		}
		if previous, exists := known[r.ResultID]; exists {
			if previous != digest {
				return 0, ErrQualityConflict
			}
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO dispatch.quality_results
		 (core_instance,config_revision,result_id,plan_id,target_ref,account_id,credential_revision,model,kind,question_ref,source_digest,score,started_at,finished_at,recorded_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, cfg.CoreInstance, cfg.ConfigRevision, r.ResultID, r.PlanID, r.TargetRef, r.AccountID, binding.Target.CredentialRevision, r.Model, r.Kind, result.QuestionRef, digest, string(evaluation), r.StartedAt, r.FinishedAt, now)
		if err != nil {
			return 0, err
		}
		inserted++
		known[r.ResultID] = digest
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dispatch.quality_results WHERE core_instance=$1 AND finished_at<$2`, cfg.CoreInstance, now.Add(-7*24*time.Hour)); err != nil {
		return 0, err
	}
	return inserted, tx.Commit()
}

type qualityTrendSample struct {
	Target     Target
	PlanID     int64
	ResultID   int64
	FinishedAt time.Time
	Evaluation qualityEvaluation
}

func readQualitySamples(ctx context.Context, tx *sql.Tx, cfg Config, now time.Time) ([]qualityTrendSample, error) {
	if _, err := qualityActivationAt(ctx, tx, cfg); err != nil {
		return nil, err
	}
	byRef := make(map[string]Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		byRef[target.Ref] = target
	}
	rows, err := tx.QueryContext(ctx, `SELECT target_ref,plan_id,result_id,finished_at,score FROM dispatch.quality_results
	 WHERE core_instance=$1 AND config_revision=$2 AND finished_at>=$3 ORDER BY finished_at DESC,result_id DESC LIMIT $4`, cfg.CoreInstance, cfg.ConfigRevision, now.Add(-7*24*time.Hour), cfg.Quality.MaxResults+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]qualityTrendSample, 0)
	for rows.Next() {
		var ref string
		var raw []byte
		var sample qualityTrendSample
		if err := rows.Scan(&ref, &sample.PlanID, &sample.ResultID, &sample.FinishedAt, &raw); err != nil {
			return nil, err
		}
		if len(results) == cfg.Quality.MaxResults {
			return nil, errors.New("dispatch quality score window exceeds limit")
		}
		target, exists := byRef[ref]
		if !exists || !target.Enabled {
			continue
		}
		sample.Target = target
		if err := json.Unmarshal(raw, &sample.Evaluation); err != nil {
			return nil, err
		}
		if sample.Evaluation.Result.ScorerRevision != quality.ScorerRevision || sample.Evaluation.BaselineDigest != cfg.Quality.baselineDigest {
			continue
		}
		results = append(results, sample)
	}
	return results, rows.Err()
}

func sameQualityDomain(a, b Target) bool {
	return sameDomain(a, b) && sameCredential(a, b) && a.ProviderRef == b.ProviderRef
}

func qualityReports(targets []Target, samples []qualityTrendSample) []QualityReport {
	reports := make([]QualityReport, 0, len(targets)*2)
	for _, target := range targets {
		for _, kind := range []string{quality.KindCharArt, quality.KindQuiz} {
			report := QualityReport{TargetRef: target.Ref, Kind: kind, FactorMilli: 1000, Reason: "insufficient_evidence", ScorerRevision: quality.ScorerRevision}
			questions := make(map[string][]qualityTrendSample)
			for _, sample := range samples {
				result := sample.Evaluation.Result
				if !sameQualityDomain(sample.Target, target) || result.Kind != kind {
					continue
				}
				report.Samples++
				if result.Confidence >= 80 && sample.Evaluation.Reference.CalibratedCoverage {
					report.QualifiedSamples++
				}
				questions[result.QuestionRef] = append(questions[result.QuestionRef], sample)
			}
			plans := make(map[int64]bool)
			driftPlans := make(map[int64]bool)
			for _, series := range questions {
				sort.Slice(series, func(i, j int) bool {
					if series[i].FinishedAt.Equal(series[j].FinishedAt) {
						return series[i].ResultID > series[j].ResultID
					}
					return series[i].FinishedAt.After(series[j].FinishedAt)
				})
				if len(series) < 5 {
					continue
				}
				series = series[:5]
				scores := make([]float64, 0, 5)
				qualified := true
				for _, sample := range series {
					if sample.Evaluation.Result.Confidence < 80 || !sample.Evaluation.Reference.CalibratedCoverage {
						qualified = false
						break
					}
					scores = append(scores, float64(sample.Evaluation.Result.Score))
				}
				if !qualified {
					continue
				}
				for _, sample := range series {
					plans[sample.PlanID] = true
				}
				sort.Float64s(scores)
				reference := series[0].Evaluation.Reference
				threshold := reference.Median - max(10, 3*reference.MAD)
				report.Questions++
				if scores[2] < threshold {
					report.DriftQuestions++
					for _, sample := range series {
						if float64(sample.Evaluation.Result.Score) < threshold {
							driftPlans[sample.PlanID] = true
						}
					}
				}
			}
			report.Plans = len(plans)
			report.DriftPlans = len(driftPlans)
			if report.Questions >= 2 && report.Plans >= 2 {
				report.Reason = "stable"
				if report.DriftQuestions >= 2 && report.DriftPlans >= 2 {
					report.FactorMilli = 850
					report.Reason = "quality_drift"
				} else if report.DriftQuestions >= 2 {
					report.Reason = "insufficient_independent_plans"
				}
			}
			reports = append(reports, report)
		}
	}
	return reports
}

func (s *Store) QualityReports(ctx context.Context, cfg Config) ([]QualityReport, error) {
	tx, now, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	samples, err := readQualitySamples(ctx, tx, cfg, now)
	if err != nil {
		return nil, err
	}
	reports := qualityReports(cfg.Targets, samples)
	return reports, tx.Commit()
}

func applyQualityWeights(snapshot *Snapshot, reports []QualityReport) {
	factors := make(map[string]int, len(reports))
	for _, report := range reports {
		previous, exists := factors[report.TargetRef]
		if !exists || report.FactorMilli < previous {
			factors[report.TargetRef] = report.FactorMilli
		}
	}
	for i := range snapshot.Candidates {
		candidate := &snapshot.Candidates[i]
		if factor, exists := factors[candidate.Ref]; exists && factor >= 850 && factor <= 1000 && candidate.EffectiveWeight > 0 {
			candidate.EffectiveWeight = (candidate.EffectiveWeight*int64(factor) + 999) / 1000
		}
	}
}
