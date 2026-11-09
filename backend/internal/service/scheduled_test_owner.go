package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/scheduledowner"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/robfig/cron/v3"
)

const scheduledOwnerCleanupTimeout = 10 * time.Second

type scheduledOwnerRepository interface {
	ScheduledOwnerStore() *scheduledowner.Store
}

type scheduledOwnerWorkerPool interface {
	ScheduledOwnerMaxWorkers() int
}

type scheduledOwnerRunInfo struct {
	PlanID  int64
	Epoch   int64
	Owner   string
	Slot    time.Time
	Managed bool
}

type scheduledOwnerRun interface {
	Validate(context.Context) error
	Finish(context.Context, int64, error) error
	Close() error
	info() scheduledOwnerRunInfo
}

type scheduledOwnerProtocol interface {
	CheckSchema(context.Context) error
	ListQueued(context.Context, int) ([]int64, error)
	acquire(context.Context, int64, string) (scheduledOwnerRun, error)
}

type scheduledOwnerStoreAdapter struct{ *scheduledowner.Store }

func (s scheduledOwnerStoreAdapter) acquire(ctx context.Context, planID int64, owner string) (scheduledOwnerRun, error) {
	run, err := s.Acquire(ctx, planID, owner)
	if err != nil {
		return nil, err
	}
	return scheduledOwnerRunAdapter{run}, nil
}

type scheduledOwnerRunAdapter struct{ *scheduledowner.Run }

func (r scheduledOwnerRunAdapter) info() scheduledOwnerRunInfo {
	return scheduledOwnerRunInfo{PlanID: r.PlanID, Epoch: r.Epoch, Owner: r.Owner, Slot: r.Slot, Managed: r.Managed}
}

type scheduledOwnerRuntime struct {
	poll     time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	workers  chan struct{}
	mu       sync.Mutex
	stopped  bool
	wg       sync.WaitGroup
	initOnce sync.Once
	initErr  error
	store    scheduledOwnerProtocol
	cron     *cron.Cron
}

func newScheduledOwnerRuntime() (*scheduledOwnerRuntime, error) {
	enabled, present := os.LookupEnv("SUB2API_SCHEDULED_OWNER_ENABLED")
	if !present {
		return nil, nil
	}
	on, err := strconv.ParseBool(enabled)
	if err != nil {
		return nil, errors.New("invalid SUB2API_SCHEDULED_OWNER_ENABLED")
	}
	if !on {
		return nil, nil
	}
	poll := time.Second
	if value, present := os.LookupEnv("SUB2API_SCHEDULED_OWNER_POLL_INTERVAL"); present {
		poll, err = time.ParseDuration(value)
		if err != nil || poll < 100*time.Millisecond || poll > time.Minute {
			return nil, errors.New("invalid SUB2API_SCHEDULED_OWNER_POLL_INTERVAL")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &scheduledOwnerRuntime{poll: poll, ctx: ctx, cancel: cancel, workers: make(chan struct{}, scheduledTestDefaultMaxWorkers)}, nil
}

func (s *ScheduledTestRunnerService) configureScheduledOwnerWorkers() error {
	if pool, ok := s.planRepo.(scheduledOwnerWorkerPool); ok {
		workers := min(pool.ScheduledOwnerMaxWorkers(), scheduledTestDefaultMaxWorkers)
		if workers < 1 {
			return errors.New("scheduled owner needs a database execution connection")
		}
		s.owner.workers = make(chan struct{}, workers)
	}
	return nil
}

func (s *ScheduledTestRunnerService) ensureScheduledOwner() error {
	rt := s.owner
	rt.initOnce.Do(func() {
		if rt.store == nil {
			provider, ok := s.planRepo.(scheduledOwnerRepository)
			if !ok {
				rt.initErr = errors.New("scheduled owner repository unavailable")
				return
			}
			store := provider.ScheduledOwnerStore()
			if store == nil {
				rt.initErr = errors.New("scheduled owner store unavailable")
				return
			}
			rt.store = scheduledOwnerStoreAdapter{store}
		}
		ctx, cancel := context.WithTimeout(rt.ctx, scheduledOwnerCleanupTimeout)
		defer cancel()
		rt.initErr = rt.store.CheckSchema(ctx)
	})
	return rt.initErr
}

func (s *ScheduledTestRunnerService) startOwnedRunner() {
	if err := s.ensureScheduledOwner(); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] not started (owner coordination unavailable)")
		return
	}
	rt := s.owner
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.stopped {
		return
	}
	loc := time.Local
	if s.cfg != nil {
		if parsed, err := time.LoadLocation(s.cfg.Timezone); err == nil && parsed != nil {
			loc = parsed
		}
	}
	c := cron.New(cron.WithParser(scheduledTestCronParser), cron.WithLocation(loc))
	if _, err := c.AddFunc("* * * * *", s.runOwnedScheduled); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] not started (invalid schedule)")
		return
	}
	rt.cron = c
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		s.pollScheduledOwner()
	}()
	c.Start()
	logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] owner-ready poll_ms=%d workers=%d", rt.poll.Milliseconds(), cap(rt.workers))
}

func (s *ScheduledTestRunnerService) stopOwnedRunner() {
	rt := s.owner
	rt.mu.Lock()
	rt.stopped = true
	rt.cancel()
	c := rt.cron
	rt.mu.Unlock()
	if c != nil {
		<-c.Stop().Done()
	}
	rt.wg.Wait()
	logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] owner-stopped drained=true")
}

func (rt *scheduledOwnerRuntime) begin() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.stopped {
		return false
	}
	rt.wg.Add(1)
	return true
}

func (rt *scheduledOwnerRuntime) submit(ctx context.Context, work func()) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case rt.workers <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	if !rt.begin() {
		<-rt.workers
		return false
	}
	go func() {
		defer rt.wg.Done()
		defer func() { <-rt.workers }()
		work()
	}()
	return true
}

func (s *ScheduledTestRunnerService) runOwnedScheduled() {
	rt := s.owner
	if !rt.begin() {
		return
	}
	defer rt.wg.Done()
	select {
	case <-time.After(10 * time.Second):
	case <-rt.ctx.Done():
		return
	}
	ctx, cancel := context.WithTimeout(rt.ctx, 5*time.Minute)
	defer cancel()
	plans, err := s.planRepo.ListDue(ctx, time.Now())
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] owner-list-due failed")
		return
	}
	var pending sync.WaitGroup
	for _, plan := range plans {
		planID := plan.ID
		pending.Add(1)
		if !rt.submit(ctx, func() {
			defer pending.Done()
			s.runOwnedPlan(ctx, planID, "legacy")
		}) {
			pending.Done()
			break
		}
	}
	pending.Wait()
}

func (s *ScheduledTestRunnerService) pollScheduledOwner() {
	ticker := time.NewTicker(s.owner.poll)
	defer ticker.Stop()
	for {
		if s.owner.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(s.owner.ctx, scheduledOwnerCleanupTimeout)
		plans, err := s.owner.store.ListQueued(ctx, cap(s.owner.workers))
		cancel()
		if err != nil {
			plans = nil
			if s.owner.ctx.Err() == nil {
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] owner-list-queued failed")
			}
		}
		for _, planID := range plans {
			if !s.owner.submit(s.owner.ctx, func() {
				ctx, cancel := context.WithTimeout(s.owner.ctx, 5*time.Minute)
				defer cancel()
				s.runOwnedPlan(ctx, planID, "dispatch")
			}) {
				return
			}
		}
		select {
		case <-ticker.C:
		case <-s.owner.ctx.Done():
			return
		}
	}
}

func (s *ScheduledTestRunnerService) runOwnedPlan(ctx context.Context, planID int64, owner string) {
	if s.ensureScheduledOwner() != nil || ctx.Err() != nil {
		return
	}
	run, err := s.owner.store.acquire(ctx, planID, owner)
	if err != nil {
		if !errors.Is(err, scheduledowner.ErrNotAdmitted) && ctx.Err() == nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] owner-acquire failed owner=%s plan=%d", owner, planID)
		}
		return
	}
	info := run.info()
	logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] owner-claimed owner=%s plan=%d epoch=%d slot=%v", info.Owner, info.PlanID, info.Epoch, info.Slot)
	defer func() {
		if err := run.Close(); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] owner-close failed owner=%s plan=%d", info.Owner, info.PlanID)
		}
	}()
	resultID, runErr := s.executeOwnedPlan(ctx, planID, run, info.Managed)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), scheduledOwnerCleanupTimeout)
	defer cancel()
	finishErr := run.Finish(cleanup, resultID, runErr)
	outcome := "completed"
	if runErr != nil || finishErr != nil {
		outcome = "unknown"
	}
	logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] owner-finished owner=%s plan=%d epoch=%d slot=%v result=%d outcome=%s", info.Owner, info.PlanID, info.Epoch, info.Slot, resultID, outcome)
}

func (s *ScheduledTestRunnerService) executeOwnedPlan(ctx context.Context, planID int64, run scheduledOwnerRun, managed bool) (int64, error) {
	plan, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		return 0, fmt.Errorf("reload plan: %w", err)
	}
	if plan == nil || plan.ID != planID || !plan.Enabled || plan.NextRunAt == nil || plan.NextRunAt.After(time.Now()) {
		return 0, errors.New("fresh plan is not due")
	}
	if err := run.Validate(ctx); err != nil {
		return 0, fmt.Errorf("validate plan: %w", err)
	}
	result, err := s.runPlanTest(ctx, plan)
	var resultID int64
	switch {
	case errors.Is(err, errScheduledTestSkipped):
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d %v", plan.ID, err)
	case err != nil:
		return 0, fmt.Errorf("run test: %w", err)
	default:
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if result == nil {
			return 0, errors.New("missing test result")
		}
		if err := run.Validate(ctx); err != nil {
			return 0, fmt.Errorf("validate result: %w", err)
		}
		result.PlanID = plan.ID
		saved, err := s.scheduledSvc.resultRepo.Create(ctx, result)
		if err != nil {
			return 0, fmt.Errorf("save result: %w", err)
		}
		if saved == nil || saved.ID <= 0 {
			return 0, errors.New("missing persisted result identity")
		}
		resultID = saved.ID
		result = saved
		if !managed {
			if err := run.Validate(ctx); err != nil {
				return resultID, fmt.Errorf("validate pruning: %w", err)
			}
			if err := s.scheduledSvc.resultRepo.PruneOldResults(ctx, plan.ID, plan.MaxResults); err != nil {
				return resultID, fmt.Errorf("prune results: %w", err)
			}
		}
		if plan.Kind == ScheduledTestKindQuiz && plan.FailureAction != "" && plan.FailureAction != ScheduledTestFailureNone {
			if err := run.Validate(ctx); err != nil {
				return resultID, fmt.Errorf("validate quiz outcome: %w", err)
			}
			if err := s.planRepo.ApplyQuizOutcome(ctx, plan, result); err != nil {
				return resultID, fmt.Errorf("apply quiz outcome: %w", err)
			}
		}
		if (plan.Kind == "" || plan.Kind == ScheduledTestKindProbe) && result.Status == "success" && plan.AutoRecover {
			if err := run.Validate(ctx); err != nil {
				return resultID, fmt.Errorf("validate account recovery: %w", err)
			}
			if s.rateLimitSvc != nil {
				if _, err := s.rateLimitSvc.RecoverAccountAfterSuccessfulTest(ctx, plan.AccountID); err != nil {
					return resultID, fmt.Errorf("recover account: %w", err)
				}
			}
		}
	}
	if err := run.Validate(ctx); err != nil {
		return resultID, fmt.Errorf("validate progress: %w", err)
	}
	now := time.Now()
	nextRun, err := computeNextRun(plan.CronExpression, now)
	if err != nil {
		return resultID, fmt.Errorf("compute next run: %w", err)
	}
	if err := s.planRepo.UpdateAfterRun(ctx, plan.ID, now, nextRun); err != nil {
		return resultID, fmt.Errorf("update progress: %w", err)
	}
	return resultID, nil
}
