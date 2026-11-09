package dispatch

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

type RoundResult struct {
	Revision       int64 `json:"revision"`
	Candidates     int   `json:"candidates"`
	Probes         int   `json:"probes"`
	Unknown        int   `json:"unknown"`
	Observations   int   `json:"observations,omitempty"`
	QualityResults int   `json:"quality_results,omitempty"`
	ScheduledPlans int   `json:"scheduled_plans,omitempty"`
	// Quality deferral fields stay zero, and omitted, unless quality.deferral is enabled.
	QualityPending         int    `json:"quality_pending,omitempty"`
	QualityDeferral        string `json:"quality_deferral,omitempty"`
	QualityBacklogExceeded bool   `json:"quality_backlog_exceeded,omitempty"`
	// published is set only for rounds that committed a snapshot.
	published *PublishedSnapshot
}

// PublishedSnapshot 是本轮已提交的快照及其发布 lineage；未配置 snapshot_server 时 lineage 为空。
type PublishedSnapshot struct {
	Lineage  string
	Snapshot Snapshot
}

func (r RoundResult) Published() (PublishedSnapshot, bool) {
	if r.published == nil {
		return PublishedSnapshot{}, false
	}
	return *r.published, true
}

// RunRound 没有请求重试；未知 attempt 继续由 Core 回执与审计记录核对。
func RunRound(ctx context.Context, cfg Config, store *Store, runner *ProbeRunner, sources ...*TelemetryReader) (RoundResult, error) {
	var result RoundResult
	start := time.Now()
	if err := store.SyncRuntimeConfig(ctx, cfg); err != nil {
		return result, err
	}
	var telemetry *TelemetryReader
	if cfg.Telemetry != nil {
		if len(sources) != 1 || sources[0] == nil || sources[0].Passive == nil || sources[0].Load == nil {
			return result, errors.New("dispatch telemetry sources are required")
		}
		telemetry = sources[0]
		if err := ingestPassive(ctx, cfg, store, telemetry, &result); err != nil {
			return result, err
		}
	}
	if runner != nil && cfg.Probe != nil {
		owner := uuid.NewString()
		var workers sync.WaitGroup
		var mu sync.Mutex
		var roundErr error
		semaphore := make(chan struct{}, cfg.Probe.GlobalConcurrent)
		targets := cfg.Targets
		if cfg.ProbeSchedule != nil {
			targets = ProbeOrder(cfg.Targets, cfg.CoreInstance, time.Now())
		}
		for _, target := range targets {
			if err := ctx.Err(); err != nil {
				workers.Wait()
				return result, err
			}
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				workers.Wait()
				return result, ctx.Err()
			}
			lease, err := store.ClaimProbe(ctx, cfg.CoreInstance, target.Ref, owner, cfg.ProbeLimits())
			if err != nil {
				<-semaphore
				workers.Wait()
				return result, err
			}
			if lease == nil {
				<-semaphore
				continue
			}
			workers.Go(func() {
				defer func() { <-semaphore }()
				observation := runner.Execute(ctx, *lease)
				finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				err := store.FinishProbe(finishCtx, *lease, observation)
				cancel()
				mu.Lock()
				defer mu.Unlock()
				result.Probes++
				if observation.Outcome == Unknown {
					result.Unknown++
				}
				if err != nil && !errors.Is(err, ErrProbeLease) {
					roundErr = err
				}
			})
		}
		workers.Wait()
		if roundErr != nil {
			return result, roundErr
		}
	}
	var snapshot Snapshot
	var lineage string
	var err error
	if cfg.Quality != nil {
		if len(sources) != 1 || sources[0] == nil || sources[0].Quality == nil {
			return result, errors.New("dispatch quality source is required")
		}
		ingest, err := store.IngestQualityRound(ctx, cfg, sources[0].Quality, start)
		if err != nil {
			return result, err
		}
		result.QualityResults, result.QualityPending, result.QualityDeferral, result.QualityBacklogExceeded = ingest.Recorded, ingest.Pending, ingest.Deferral, ingest.BacklogExceeded
	}
	if cfg.Scheduled != nil {
		if len(sources) != 1 || sources[0] == nil || sources[0].Scheduled == nil {
			return result, errors.New("dispatch scheduled source is required")
		}
		result.ScheduledPlans, err = sources[0].Scheduled.Schedule(ctx, *cfg.Scheduled)
		if err != nil {
			return result, err
		}
	}
	if telemetry != nil {
		// Recheck the producer generation after probes before refreshing admission.
		if err = ingestPassive(ctx, cfg, store, telemetry, &result); err != nil {
			return result, err
		}
		ids := make([]int64, 0, len(cfg.Targets))
		seen := make(map[int64]bool)
		for _, target := range cfg.Targets {
			if !seen[target.AccountID] {
				ids = append(ids, target.AccountID)
				seen[target.AccountID] = true
			}
		}
		var load LoadSample
		load, err = telemetry.Load.Read(ctx, ids)
		if err == nil {
			snapshot, lineage, err = store.publishRuntime(ctx, cfg, &load)
		}
	} else {
		snapshot, lineage, err = store.publishRuntime(ctx, cfg, nil)
	}
	if err != nil {
		return result, err
	}
	result.Revision, result.Candidates = snapshot.Revision, len(snapshot.Candidates)
	result.published = &PublishedSnapshot{Lineage: lineage, Snapshot: snapshot}
	return result, nil
}

func ingestPassive(ctx context.Context, cfg Config, store *Store, source *TelemetryReader, result *RoundResult) error {
	events, err := source.Passive.Read(ctx, cfg)
	if err != nil {
		return err
	}
	count, err := store.ObservePassive(ctx, cfg.CoreInstance, cfg.ConfigRevision, events)
	result.Observations += count
	return err
}
