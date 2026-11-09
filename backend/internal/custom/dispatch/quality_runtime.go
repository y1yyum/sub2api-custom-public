package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/quality"
)

type QualityResultSource interface {
	Read(context.Context, []QualityBinding, []Target, time.Time, int) ([]LegacyQualityResult, error)
}

func (s *Store) IngestQuality(ctx context.Context, cfg Config, source QualityResultSource) (int, error) {
	ingest, err := s.IngestQualityRound(ctx, cfg, source, time.Now())
	return ingest.Recorded, err
}

// IngestQualityRound scores new results; with quality.deferral it stops at the
// round's budget or snapshot margin and leaves the rest pending.
func (s *Store) IngestQualityRound(ctx context.Context, cfg Config, source QualityResultSource, roundStart time.Time) (QualityIngest, error) {
	if cfg.Quality == nil || cfg.Quality.baseline == nil || source == nil {
		return QualityIngest{}, errors.New("dispatch quality source and baseline are required")
	}
	at, err := s.QualityActivation(ctx, cfg)
	if err != nil {
		return QualityIngest{}, err
	}
	results, err := source.Read(ctx, cfg.Quality.Bindings, cfg.Targets, at, cfg.Quality.MaxResults)
	if err != nil {
		return QualityIngest{}, err
	}
	if len(results) > cfg.Quality.MaxResults {
		return QualityIngest{}, errors.New("dispatch quality result window exceeds limit")
	}
	known, err := s.knownQuality(ctx, cfg, results)
	if err != nil {
		return QualityIngest{}, err
	}
	scored, ingest, err := scoreQualityResults(ctx, cfg, results, known, newQualityScoringWindow(cfg, roundStart, time.Now()), time.Now)
	if err != nil {
		return QualityIngest{}, err
	}
	ingest.Recorded, err = s.recordQuality(ctx, cfg, scored)
	if err != nil {
		return QualityIngest{}, err
	}
	return ingest, nil
}

// Results arrive oldest first, so deferral always resumes from the oldest
// pending result and every deferred result is rescanned, never dropped.
func scoreQualityResults(ctx context.Context, cfg Config, results []LegacyQualityResult, known map[int64]string, window qualityScoringWindow, clock func() time.Time) ([]scoredQualityResult, QualityIngest, error) {
	var ingest QualityIngest
	scorer := quality.NewScorer(cfg.Quality.baseline)
	scored := make([]scoredQualityResult, 0, len(results))
	for _, r := range results {
		if err := ctx.Err(); err != nil {
			return nil, QualityIngest{}, err
		}
		digest, err := qualitySourceDigest(r)
		if err != nil {
			return nil, QualityIngest{}, err
		}
		if previous, exists := known[r.ResultID]; exists {
			if previous != digest {
				return nil, QualityIngest{}, ErrQualityConflict
			}
			continue
		}
		if ingest.Deferral == "" {
			ingest.Deferral = window.deferral(len(scored), clock())
		}
		if ingest.Deferral != "" {
			ingest.Pending++
			continue
		}
		result := scorer.Score(quality.Sample{ResultID: r.ResultID, TaskID: r.PlanID, Kind: r.Kind, Model: r.Model, QuestionID: r.QuestionID, Prompt: r.Prompt, Response: r.Response, Expected: r.ExpectedAnswer, Verdict: r.Verdict, Status: r.Status})
		// The source has a larger, independently bounded text envelope. Preserve
		// its question identity when the scorer declines oversized metadata.
		if result.QuestionRef == "" && result.Reason == "input_limit" && len(r.Prompt) <= qualitySourceTextLimit && len(r.ExpectedAnswer) <= qualitySourceTextLimit && len(r.QuestionID) <= 256 {
			result.QuestionRef = quality.QuestionRef(r.Kind, r.QuestionID, r.Prompt, r.ExpectedAnswer)
		}
		scored = append(scored, scoredQualityResult{source: r, evaluation: qualityEvaluation{Result: result, Reference: cfg.Quality.baseline.Reference(r.Kind, result.QuestionRef, r.Model), BaselineDigest: cfg.Quality.baselineDigest}})
	}
	if cfg.Quality.Deferral != nil {
		ingest.BacklogExceeded = ingest.Pending > cfg.Quality.Deferral.MaxPending
	}
	return scored, ingest, nil
}
