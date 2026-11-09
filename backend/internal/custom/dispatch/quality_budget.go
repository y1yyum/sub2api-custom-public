package dispatch

import (
	"context"
	"errors"
	"time"
)

// QualityDeferralConfig bounds quality scoring inside a round so probe
// settlement and snapshot publication never wait for it. Every value is
// operator supplied; there are no implicit production defaults.
type QualityDeferralConfig struct {
	BudgetMilliseconds    int `json:"budget_milliseconds"`
	TTLMarginMilliseconds int `json:"ttl_margin_milliseconds"`
	MaxPending            int `json:"max_pending"`
}

const (
	QualityDeferredTTLMargin = "ttl_margin"
	QualityDeferredBudget    = "budget"
)

// QualityIngest describes one ingestion pass. Pending results stay unrecorded
// and are rescanned by the next round; recording remains deduplicated.
type QualityIngest struct {
	Recorded        int
	Pending         int
	Deferral        string
	BacklogExceeded bool
}

func validateQualityDeferral(cfg Config) error {
	if cfg.Quality == nil || cfg.Quality.Deferral == nil {
		return nil
	}
	d := cfg.Quality.Deferral
	window := cfg.SnapshotTTLSeconds*1000 - cfg.PollIntervalMilliseconds
	if d.TTLMarginMilliseconds < 1 || d.TTLMarginMilliseconds >= window ||
		d.BudgetMilliseconds < 1 || d.BudgetMilliseconds > window-d.TTLMarginMilliseconds ||
		d.MaxPending < 1 || d.MaxPending > cfg.Quality.MaxResults {
		return errors.New("invalid dispatch quality deferral limits")
	}
	return nil
}

type qualityScoringWindow struct {
	enabled     bool
	ttl, budget time.Time
}

// The previous snapshot was published at the end of the previous round, one
// poll interval before roundStart, so it expires near roundStart+TTL-poll.
func newQualityScoringWindow(cfg Config, roundStart, scoringStart time.Time) qualityScoringWindow {
	d := cfg.Quality.Deferral
	if d == nil {
		return qualityScoringWindow{}
	}
	reserve := time.Duration(cfg.PollIntervalMilliseconds+d.TTLMarginMilliseconds) * time.Millisecond
	return qualityScoringWindow{
		enabled: true,
		ttl:     roundStart.Add(time.Duration(cfg.SnapshotTTLSeconds)*time.Second - reserve),
		budget:  scoringStart.Add(time.Duration(d.BudgetMilliseconds) * time.Millisecond),
	}
}

// deferral names why the next pending result must wait. The snapshot margin
// always wins; the budget still admits the oldest result so rounds progress.
func (w qualityScoringWindow) deferral(scored int, now time.Time) string {
	switch {
	case !w.enabled:
		return ""
	case !now.Before(w.ttl):
		return QualityDeferredTTLMargin
	case scored > 0 && !now.Before(w.budget):
		return QualityDeferredBudget
	}
	return ""
}

// QualityReportsWithBacklog adds the unscored backlog to the read-only quality
// report. It reads the bound source but never scores or records results.
func (s *Store) QualityReportsWithBacklog(ctx context.Context, cfg Config, source QualityResultSource) ([]QualityReport, error) {
	if cfg.Quality == nil || cfg.Quality.Deferral == nil || source == nil {
		return nil, errors.New("dispatch quality deferral and source are required")
	}
	reports, err := s.QualityReports(ctx, cfg)
	if err != nil {
		return nil, err
	}
	at, err := s.QualityActivation(ctx, cfg)
	if err != nil {
		return nil, err
	}
	results, err := source.Read(ctx, cfg.Quality.Bindings, cfg.Targets, at, cfg.Quality.MaxResults)
	if err != nil {
		return nil, err
	}
	if len(results) > cfg.Quality.MaxResults {
		return nil, errors.New("dispatch quality result window exceeds limit")
	}
	known, err := s.knownQuality(ctx, cfg, results)
	if err != nil {
		return nil, err
	}
	return reports, attachQualityBacklog(reports, results, known, cfg.Quality.Deferral.MaxPending, time.Now())
}

func attachQualityBacklog(reports []QualityReport, results []LegacyQualityResult, known map[int64]string, maxPending int, now time.Time) error {
	type key struct{ ref, kind string }
	pending := make(map[key]int)
	oldest := make(map[key]time.Time)
	total := 0
	for _, r := range results {
		digest, err := qualitySourceDigest(r)
		if err != nil {
			return err
		}
		if previous, exists := known[r.ResultID]; exists {
			if previous != digest {
				return ErrQualityConflict
			}
			continue
		}
		k := key{r.TargetRef, r.Kind}
		pending[k]++
		total++
		if at, seen := oldest[k]; !seen || r.FinishedAt.Before(at) {
			oldest[k] = r.FinishedAt
		}
	}
	exceeded := total > maxPending
	for i := range reports {
		k := key{reports[i].TargetRef, reports[i].Kind}
		count := pending[k]
		reports[i].PendingSamples, reports[i].BacklogExceeded = &count, &exceeded
		if count > 0 {
			age := max(0, int64(now.Sub(oldest[k])/time.Second))
			reports[i].OldestPendingSeconds = &age
		}
	}
	return nil
}
