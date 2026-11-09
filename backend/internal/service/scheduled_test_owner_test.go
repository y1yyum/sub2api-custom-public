//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/scheduledowner"
	"github.com/stretchr/testify/require"
)

type scheduledOwnerTestStore struct {
	check      func(context.Context) error
	queued     func(context.Context, int) ([]int64, error)
	acquireRun func(context.Context, int64, string) (scheduledOwnerRun, error)
}

func (s *scheduledOwnerTestStore) CheckSchema(ctx context.Context) error {
	if s.check != nil {
		return s.check(ctx)
	}
	return nil
}

func (s *scheduledOwnerTestStore) ListQueued(ctx context.Context, limit int) ([]int64, error) {
	if s.queued != nil {
		return s.queued(ctx, limit)
	}
	return nil, nil
}

func (s *scheduledOwnerTestStore) acquire(ctx context.Context, id int64, owner string) (scheduledOwnerRun, error) {
	return s.acquireRun(ctx, id, owner)
}

type scheduledOwnerTestRun struct {
	metadata scheduledOwnerRunInfo
	validate func(context.Context) error
	finish   func(context.Context, int64, error) error
	close    func() error
}

func (r *scheduledOwnerTestRun) info() scheduledOwnerRunInfo { return r.metadata }
func (r *scheduledOwnerTestRun) Validate(ctx context.Context) error {
	if r.validate != nil {
		return r.validate(ctx)
	}
	return ctx.Err()
}
func (r *scheduledOwnerTestRun) Finish(ctx context.Context, resultID int64, err error) error {
	if r.finish != nil {
		return r.finish(ctx, resultID, err)
	}
	return nil
}
func (r *scheduledOwnerTestRun) Close() error {
	if r.close != nil {
		return r.close()
	}
	return nil
}

type scheduledOwnerTestPlanRepo struct {
	ScheduledTestPlanRepository
	get     func(context.Context, int64) (*ScheduledTestPlan, error)
	update  func(context.Context, int64, time.Time, time.Time) error
	outcome func(context.Context, *ScheduledTestPlan, *ScheduledTestResult) error
}

func (r *scheduledOwnerTestPlanRepo) GetByID(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return r.get(ctx, id)
}
func (r *scheduledOwnerTestPlanRepo) UpdateAfterRun(ctx context.Context, id int64, last, next time.Time) error {
	if r.update != nil {
		return r.update(ctx, id, last, next)
	}
	return nil
}
func (r *scheduledOwnerTestPlanRepo) ApplyQuizOutcome(ctx context.Context, p *ScheduledTestPlan, result *ScheduledTestResult) error {
	if r.outcome != nil {
		return r.outcome(ctx, p, result)
	}
	return nil
}

type scheduledOwnerTestResultRepo struct {
	ScheduledTestResultRepository
	create func(context.Context, *ScheduledTestResult) (*ScheduledTestResult, error)
	prune  func(context.Context, int64, int) error
}

func (r *scheduledOwnerTestResultRepo) Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	return r.create(ctx, result)
}
func (r *scheduledOwnerTestResultRepo) PruneOldResults(ctx context.Context, planID int64, keep int) error {
	if r.prune != nil {
		return r.prune(ctx, planID, keep)
	}
	return nil
}

func newScheduledOwnerTestRunner(t *testing.T, plans ScheduledTestPlanRepository, svc *ScheduledTestService, probe *AccountTestService) *ScheduledTestRunnerService {
	t.Helper()
	t.Setenv("SUB2API_SCHEDULED_OWNER_ENABLED", "true")
	t.Setenv("SUB2API_SCHEDULED_OWNER_POLL_INTERVAL", "100ms")
	runner := NewScheduledTestRunnerService(plans, svc, probe, nil, nil)
	require.NoError(t, runner.ownerErr)
	require.NotNil(t, runner.owner)
	t.Cleanup(runner.Stop)
	return runner
}

func scheduledOwnerDuePlan() *ScheduledTestPlan {
	due := time.Now().Add(-time.Minute)
	return &ScheduledTestPlan{ID: 5, AccountID: 41, ModelID: "public-model", Kind: ScheduledTestKindCharArt,
		Prompt: "fresh prompt", Enabled: true, NextRunAt: &due, MaxResults: 20, CronExpression: "* * * * *"}
}

func TestScheduledOwnerEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, poll  string
		wantEnabled, wantErr bool
	}{
		{"off", "false", "invalid", false, false},
		{"on", "true", "1s", true, false},
		{"default poll", "true", "", true, false},
		{"minimum poll", "true", "100ms", true, false},
		{"maximum poll", "true", "1m", true, false},
		{"invalid bool", "invalid", "", false, true},
		{"empty bool", "", "", false, true},
		{"invalid poll", "true", "later", false, true},
		{"short poll", "true", "99ms", false, true},
		{"long poll", "true", "61s", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUB2API_SCHEDULED_OWNER_ENABLED", tc.enabled)
			if tc.poll != "" {
				t.Setenv("SUB2API_SCHEDULED_OWNER_POLL_INTERVAL", tc.poll)
			} else {
				t.Setenv("SUB2API_SCHEDULED_OWNER_POLL_INTERVAL", "unused")
				require.NoError(t, os.Unsetenv("SUB2API_SCHEDULED_OWNER_POLL_INTERVAL"))
			}
			rt, err := newScheduledOwnerRuntime()
			require.Equal(t, tc.wantErr, err != nil)
			require.Equal(t, tc.wantEnabled, rt != nil)
			if rt != nil {
				defer rt.cancel()
			}
		})
	}
	t.Setenv("SUB2API_SCHEDULED_OWNER_ENABLED", "unused")
	require.NoError(t, os.Unsetenv("SUB2API_SCHEDULED_OWNER_ENABLED"))
	rt, err := newScheduledOwnerRuntime()
	require.NoError(t, err)
	require.Nil(t, rt)
}

type scheduledOwnerForbiddenProvider struct{ *fakeScheduledPlanRepo }

func (*scheduledOwnerForbiddenProvider) ScheduledOwnerStore() *scheduledowner.Store {
	panic("default path accessed owner database")
}

func (*scheduledOwnerForbiddenProvider) ScheduledOwnerMaxWorkers() int {
	panic("default path accessed owner pool")
}

type scheduledOwnerSizedPlanRepo struct {
	ScheduledTestPlanRepository
	workers int
}

func (r scheduledOwnerSizedPlanRepo) ScheduledOwnerMaxWorkers() int { return r.workers }

func TestScheduledOwnerWorkerPoolHonorsDatabaseReserve(t *testing.T) {
	t.Setenv("SUB2API_SCHEDULED_OWNER_ENABLED", "true")
	for _, workers := range []int{0, 1, 4, 10, 50} {
		runner := NewScheduledTestRunnerService(scheduledOwnerSizedPlanRepo{workers: workers}, nil, nil, nil, nil)
		if workers == 0 {
			require.Error(t, runner.ownerErr)
		} else {
			require.NoError(t, runner.ownerErr)
			require.Equal(t, min(workers, scheduledTestDefaultMaxWorkers), cap(runner.owner.workers))
		}
		runner.Stop()
	}
}

func TestScheduledOwnerOffPreservesLegacyPath(t *testing.T) {
	t.Setenv("SUB2API_SCHEDULED_OWNER_ENABLED", "false")
	svc, oldPlans, results := newKindsTestService(&fakeQuestionRepo{})
	plans := &scheduledOwnerForbiddenProvider{oldPlans}
	probe, bodies := newClaudePromptFixture(t, "#")
	runner := NewScheduledTestRunnerService(plans, svc, probe, nil, nil)
	require.Nil(t, runner.owner)
	runner.runOnePlan(context.Background(), scheduledOwnerDuePlan())
	require.Len(t, *bodies, 1)
	require.Len(t, results.saved, 1)
	require.Equal(t, []int64{5}, oldPlans.afterRunPlan)
	runner.Start()
	runner.Stop()
}

func TestScheduledOwnerInvalidOrUnavailableFailsClosed(t *testing.T) {
	for _, mode := range []string{"invalid", "missing hook", "missing schema"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SUB2API_SCHEDULED_OWNER_ENABLED", "true")
			if mode == "invalid" {
				t.Setenv("SUB2API_SCHEDULED_OWNER_ENABLED", "nope")
			}
			svc, plans, results := newKindsTestService(&fakeQuestionRepo{})
			probe, bodies := newClaudePromptFixture(t, "#")
			runner := NewScheduledTestRunnerService(plans, svc, probe, nil, nil)
			if mode == "missing schema" {
				runner.owner.store = &scheduledOwnerTestStore{check: func(context.Context) error { return errors.New("not installed") }}
			}
			runner.Start()
			runner.runOnePlan(context.Background(), scheduledOwnerDuePlan())
			runner.Stop()
			require.Nil(t, runner.cron)
			require.Empty(t, *bodies)
			require.Empty(t, results.saved)
			require.Empty(t, plans.afterRunPlan)
		})
	}
}

func TestScheduledOwnerReloadsAndHoldsFenceThroughWrites(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmanaged", true: "managed"}[managed], func(t *testing.T) {
			var actions []string
			plan := scheduledOwnerDuePlan()
			plans := &scheduledOwnerTestPlanRepo{get: func(context.Context, int64) (*ScheduledTestPlan, error) {
				actions = append(actions, "reload")
				return plan, nil
			}, update: func(context.Context, int64, time.Time, time.Time) error {
				actions = append(actions, "progress")
				return nil
			}}
			results := &scheduledOwnerTestResultRepo{create: func(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
				actions = append(actions, "save")
				require.Equal(t, int64(5), result.PlanID)
				copy := *result
				copy.ID = 91
				return &copy, nil
			}, prune: func(context.Context, int64, int) error {
				actions = append(actions, "prune")
				return nil
			}}
			svc := NewScheduledTestService(plans, results)
			probe, bodies := newClaudePromptFixture(t, "#")
			runner := newScheduledOwnerTestRunner(t, plans, svc, probe)
			run := &scheduledOwnerTestRun{metadata: scheduledOwnerRunInfo{PlanID: 5, Owner: "legacy", Epoch: 7, Slot: time.Unix(0, 0).UTC(), Managed: managed},
				validate: func(ctx context.Context) error { actions = append(actions, "validate"); return ctx.Err() },
				finish: func(ctx context.Context, id int64, err error) error {
					require.NoError(t, ctx.Err())
					require.NoError(t, err)
					require.Equal(t, int64(91), id)
					actions = append(actions, "finish")
					return nil
				}, close: func() error { actions = append(actions, "close"); return nil },
			}
			runner.owner.store = &scheduledOwnerTestStore{acquireRun: func(_ context.Context, id int64, owner string) (scheduledOwnerRun, error) {
				require.Equal(t, int64(5), id)
				require.Equal(t, "legacy", owner)
				actions = append(actions, "acquire")
				return run, nil
			}}
			cached := *plan
			cached.Prompt = "stale prompt"
			cached.Enabled = false
			runner.runOnePlan(context.Background(), &cached)
			require.Len(t, *bodies, 1)
			require.Equal(t, "fresh prompt", claudeRequestText(t, (*bodies)[0]))
			want := []string{"acquire", "reload", "validate", "validate", "save"}
			if !managed {
				want = append(want, "validate", "prune")
			}
			want = append(want, "validate", "progress", "finish", "close")
			require.Equal(t, want, actions)
		})
	}
}

func TestScheduledOwnerExecutionFailuresStayUnknown(t *testing.T) {
	for _, stage := range []string{"reload", "disabled", "not due", "validate", "save ack", "prune", "quiz outcome", "progress", "cron", "finish ack"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage)
			plan := scheduledOwnerDuePlan()
			var saves, prunes, outcomes, updates, finishes, closes int
			plans := &scheduledOwnerTestPlanRepo{get: func(context.Context, int64) (*ScheduledTestPlan, error) {
				if stage == "reload" {
					return nil, failure
				}
				if stage == "disabled" {
					plan.Enabled = false
				}
				if stage == "not due" {
					future := time.Now().Add(time.Hour)
					plan.NextRunAt = &future
				}
				return plan, nil
			}, update: func(context.Context, int64, time.Time, time.Time) error {
				updates++
				if stage == "progress" {
					return failure
				}
				return nil
			},
				outcome: func(context.Context, *ScheduledTestPlan, *ScheduledTestResult) error {
					outcomes++
					if stage == "quiz outcome" {
						return failure
					}
					return nil
				}}
			results := &scheduledOwnerTestResultRepo{create: func(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
				saves++
				if stage == "save ack" {
					return nil, failure
				}
				copy := *result
				copy.ID = 91
				return &copy, nil
			}, prune: func(context.Context, int64, int) error {
				prunes++
				if stage == "prune" {
					return failure
				}
				return nil
			}}
			svc := NewScheduledTestService(plans, results)
			if stage == "quiz outcome" {
				plan.Kind = ScheduledTestKindQuiz
				plan.FailureAction = ScheduledTestFailureDisableModel
				svc.questionRepo = &fakeQuestionRepo{enabled: []*IntelligenceQuestion{{ID: 1, Prompt: "6*7=?", PromptImage: testIntelligencePromptImage, AnswerRule: IntelligenceRuleNumber, ExpectedAnswers: []string{"42"}}}}
			}
			if stage == "cron" {
				plan.CronExpression = "invalid"
			}
			probe, bodies := newClaudePromptFixture(t, "42")
			runner := newScheduledOwnerTestRunner(t, plans, svc, probe)
			run := &scheduledOwnerTestRun{metadata: scheduledOwnerRunInfo{PlanID: 5, Owner: "legacy", Managed: stage != "prune"},
				validate: func(ctx context.Context) error {
					if stage == "validate" {
						return failure
					}
					return ctx.Err()
				},
				finish: func(ctx context.Context, id int64, err error) error {
					finishes++
					require.NoError(t, ctx.Err())
					if stage == "finish ack" {
						require.NoError(t, err)
						return failure
					}
					require.Error(t, err)
					if stage == "save ack" {
						require.Zero(t, id)
					}
					return nil
				}, close: func() error { closes++; return nil }}
			runner.owner.store = &scheduledOwnerTestStore{acquireRun: func(context.Context, int64, string) (scheduledOwnerRun, error) { return run, nil }}
			runner.runOnePlan(context.Background(), plan)
			require.Equal(t, 1, finishes)
			require.Equal(t, 1, closes)
			if stage == "reload" || stage == "disabled" || stage == "not due" || stage == "validate" {
				require.Empty(t, *bodies)
				require.Zero(t, saves)
			} else {
				require.Len(t, *bodies, 1)
				require.Equal(t, 1, saves)
			}
			if stage == "progress" || stage == "finish ack" {
				require.Equal(t, 1, updates)
			} else {
				require.Zero(t, updates)
			}
			if stage == "quiz outcome" {
				require.Equal(t, 1, outcomes)
			} else {
				require.Zero(t, outcomes)
			}
			if stage == "prune" {
				require.Equal(t, 1, prunes)
			} else {
				require.Zero(t, prunes)
			}
		})
	}
}

func TestScheduledOwnerNoQuestionsAdvancesWithoutSending(t *testing.T) {
	plan := scheduledOwnerDuePlan()
	plan.Kind = ScheduledTestKindQuiz
	var updated bool
	plans := &scheduledOwnerTestPlanRepo{get: func(context.Context, int64) (*ScheduledTestPlan, error) { return plan, nil },
		update: func(context.Context, int64, time.Time, time.Time) error { updated = true; return nil }}
	svc := NewScheduledTestService(plans, &scheduledOwnerTestResultRepo{create: func(context.Context, *ScheduledTestResult) (*ScheduledTestResult, error) {
		t.Fatal("unexpected save")
		return nil, nil
	}})
	svc.questionRepo = &fakeQuestionRepo{}
	probe, bodies := newClaudePromptFixture(t, "unused")
	runner := newScheduledOwnerTestRunner(t, plans, svc, probe)
	runner.owner.store = &scheduledOwnerTestStore{acquireRun: func(context.Context, int64, string) (scheduledOwnerRun, error) {
		return &scheduledOwnerTestRun{finish: func(_ context.Context, id int64, err error) error {
			require.NoError(t, err)
			require.Zero(t, id)
			require.True(t, updated)
			return nil
		}}, nil
	}}
	runner.runOnePlan(context.Background(), plan)
	require.True(t, updated)
	require.Empty(t, *bodies)
}

func TestScheduledOwnerFenceLossAfterProviderPreventsWrites(t *testing.T) {
	plan := scheduledOwnerDuePlan()
	plans := &scheduledOwnerTestPlanRepo{get: func(context.Context, int64) (*ScheduledTestPlan, error) { return plan, nil },
		update: func(context.Context, int64, time.Time, time.Time) error { t.Fatal("stale progress write"); return nil }}
	results := &scheduledOwnerTestResultRepo{create: func(context.Context, *ScheduledTestResult) (*ScheduledTestResult, error) {
		t.Fatal("stale result write")
		return nil, nil
	}}
	probe, bodies := newClaudePromptFixture(t, "ok")
	runner := newScheduledOwnerTestRunner(t, plans, NewScheduledTestService(plans, results), probe)
	failure := errors.New("owner connection lost")
	validations := 0
	finished := false
	runner.owner.store = &scheduledOwnerTestStore{acquireRun: func(context.Context, int64, string) (scheduledOwnerRun, error) {
		return &scheduledOwnerTestRun{metadata: scheduledOwnerRunInfo{Managed: true}, validate: func(context.Context) error {
			validations++
			if validations > 1 {
				return failure
			}
			return nil
		}, finish: func(_ context.Context, resultID int64, err error) error {
			require.Zero(t, resultID)
			require.ErrorIs(t, err, failure)
			finished = true
			return nil
		}}, nil
	}}
	runner.runOnePlan(context.Background(), plan)
	require.Len(t, *bodies, 1)
	require.Equal(t, 2, validations)
	require.True(t, finished)
}

func TestScheduledOwnerAdmissionDenialNeverLoadsOrSends(t *testing.T) {
	var loads int
	plans := &scheduledOwnerTestPlanRepo{get: func(context.Context, int64) (*ScheduledTestPlan, error) { loads++; return nil, nil }}
	probe, bodies := newClaudePromptFixture(t, "unused")
	runner := newScheduledOwnerTestRunner(t, plans, nil, probe)
	runner.owner.store = &scheduledOwnerTestStore{acquireRun: func(context.Context, int64, string) (scheduledOwnerRun, error) {
		return nil, scheduledowner.ErrNotAdmitted
	}}
	runner.runOnePlan(context.Background(), scheduledOwnerDuePlan())
	require.Zero(t, loads)
	require.Empty(t, *bodies)
}

type scheduledOwnerRecoveryErrorRepo struct {
	AccountRepository
	err error
}

func (r *scheduledOwnerRecoveryErrorRepo) GetByID(context.Context, int64) (*Account, error) {
	return nil, r.err
}

func TestScheduledOwnerRecoveryFailureDoesNotAdvance(t *testing.T) {
	plan := scheduledOwnerDuePlan()
	plan.Kind = ScheduledTestKindProbe
	plan.AutoRecover = true
	failure := errors.New("recovery unavailable")
	var updated, finished, closed bool
	plans := &scheduledOwnerTestPlanRepo{get: func(context.Context, int64) (*ScheduledTestPlan, error) { return plan, nil },
		update: func(context.Context, int64, time.Time, time.Time) error { updated = true; return nil }}
	results := &scheduledOwnerTestResultRepo{create: func(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
		saved := *result
		saved.ID = 91
		return &saved, nil
	}}
	probe, _ := newClaudePromptFixture(t, "ok")
	runner := newScheduledOwnerTestRunner(t, plans, NewScheduledTestService(plans, results), probe)
	runner.rateLimitSvc = &RateLimitService{accountRepo: &scheduledOwnerRecoveryErrorRepo{err: failure}}
	runner.owner.store = &scheduledOwnerTestStore{acquireRun: func(context.Context, int64, string) (scheduledOwnerRun, error) {
		return &scheduledOwnerTestRun{metadata: scheduledOwnerRunInfo{Managed: true},
			finish: func(_ context.Context, resultID int64, err error) error {
				require.Equal(t, int64(91), resultID)
				require.ErrorIs(t, err, failure)
				finished = true
				return nil
			}, close: func() error { closed = true; return nil }}, nil
	}}
	runner.runOnePlan(context.Background(), plan)
	require.False(t, updated)
	require.True(t, finished)
	require.True(t, closed)
}

type scheduledOwnerRotationErrorRepo struct {
	*fakeQuestionRepo
	err error
}

func (r *scheduledOwnerRotationErrorRepo) AdvanceRotation(context.Context, int64) (int, error) {
	return 0, r.err
}

func TestScheduledOwnerRotationFailureDoesNotSend(t *testing.T) {
	plan := scheduledOwnerDuePlan()
	plan.Kind = ScheduledTestKindQuiz
	failure := errors.New("rotation acknowledgement lost")
	var updated, finished bool
	plans := &scheduledOwnerTestPlanRepo{get: func(context.Context, int64) (*ScheduledTestPlan, error) { return plan, nil },
		update: func(context.Context, int64, time.Time, time.Time) error { updated = true; return nil }}
	svc := NewScheduledTestService(plans, nil)
	svc.questionRepo = &scheduledOwnerRotationErrorRepo{fakeQuestionRepo: &fakeQuestionRepo{enabled: []*IntelligenceQuestion{{ID: 1}}}, err: failure}
	probe, bodies := newClaudePromptFixture(t, "unused")
	runner := newScheduledOwnerTestRunner(t, plans, svc, probe)
	runner.owner.store = &scheduledOwnerTestStore{acquireRun: func(context.Context, int64, string) (scheduledOwnerRun, error) {
		return &scheduledOwnerTestRun{finish: func(_ context.Context, resultID int64, err error) error {
			require.Zero(t, resultID)
			require.ErrorIs(t, err, failure)
			finished = true
			return nil
		}}, nil
	}}
	runner.runOnePlan(context.Background(), plan)
	require.Empty(t, *bodies)
	require.False(t, updated)
	require.True(t, finished)
}

type scheduledOwnerFreshAccountRepo struct {
	AccountRepository
	account Account
}

func (r *scheduledOwnerFreshAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	account := r.account
	return &account, nil
}

func TestScheduledOwnerQueueStopCancelsAndDrainsWorkers(t *testing.T) {
	plans := &scheduledOwnerTestPlanRepo{get: func(_ context.Context, id int64) (*ScheduledTestPlan, error) {
		plan := scheduledOwnerDuePlan()
		plan.ID = id
		return plan, nil
	}}
	probe, _ := newClaudePromptFixture(t, "unused")
	account, err := probe.accountRepo.GetByID(context.Background(), 41)
	require.NoError(t, err)
	probe.accountRepo = &scheduledOwnerFreshAccountRepo{AccountRepository: probe.accountRepo, account: *account}
	started := make(chan struct{}, scheduledTestDefaultMaxWorkers)
	var active, maxActive, finished, closed, cleanupCanceled atomic.Int64
	probe.httpUpstream = &systemOneHTTPUpstream{do: func(req *http.Request) (*http.Response, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old; old = maxActive.Load() {
			if maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		<-req.Context().Done()
		return nil, req.Context().Err()
	}}
	runner := newScheduledOwnerTestRunner(t, plans, nil, probe)
	var listed atomic.Int64
	runner.owner.store = &scheduledOwnerTestStore{queued: func(_ context.Context, limit int) ([]int64, error) {
		require.Equal(t, scheduledTestDefaultMaxWorkers, limit)
		if listed.Add(1) > 1 {
			return nil, nil
		}
		return []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, nil
	}, acquireRun: func(_ context.Context, id int64, owner string) (scheduledOwnerRun, error) {
		return &scheduledOwnerTestRun{metadata: scheduledOwnerRunInfo{PlanID: id, Owner: owner, Managed: true},
			finish: func(ctx context.Context, _ int64, err error) error {
				if ctx.Err() != nil {
					cleanupCanceled.Add(1)
				}
				if errors.Is(err, context.Canceled) {
					finished.Add(1)
				}
				return nil
			},
			close: func() error { closed.Add(1); return nil }}, nil
	}}
	runner.Start()
	for range scheduledTestDefaultMaxWorkers {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	stopDone := make(chan struct{})
	go func() { runner.Stop(); close(stopDone) }()
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not drain")
	}
	require.Equal(t, int64(scheduledTestDefaultMaxWorkers), maxActive.Load())
	require.Zero(t, active.Load())
	require.Equal(t, int64(scheduledTestDefaultMaxWorkers), finished.Load())
	require.Equal(t, int64(scheduledTestDefaultMaxWorkers), closed.Load())
	require.Zero(t, cleanupCanceled.Load())
	var afterStop atomic.Bool
	require.False(t, runner.owner.submit(context.Background(), func() { afterStop.Store(true) }))
	require.False(t, afterStop.Load())
}

func TestScheduledOwnerQueueReadFailureRejectsPartialList(t *testing.T) {
	runner := newScheduledOwnerTestRunner(t, nil, nil, nil)
	var listed, acquired atomic.Int64
	runner.owner.store = &scheduledOwnerTestStore{queued: func(context.Context, int) ([]int64, error) {
		listed.Add(1)
		return []int64{5}, errors.New("partial queue read")
	}, acquireRun: func(context.Context, int64, string) (scheduledOwnerRun, error) {
		acquired.Add(1)
		return nil, scheduledowner.ErrNotAdmitted
	}}
	runner.Start()
	require.Eventually(t, func() bool { return listed.Load() >= 2 }, 2*time.Second, 10*time.Millisecond)
	runner.Stop()
	require.Zero(t, acquired.Load())
}

func TestScheduledOwnerStopDrainsLegacyDelay(t *testing.T) {
	runner := newScheduledOwnerTestRunner(t, nil, nil, nil)
	entered := make(chan struct{})
	done := make(chan struct{})
	runner.owner.store = &scheduledOwnerTestStore{}
	go func() { defer close(done); close(entered); runner.runOwnedScheduled() }()
	<-entered
	runner.Stop()
	<-done
	// Stopped callbacks cannot register new work, even if they were delayed.
	runner.runOwnedScheduled()
}

func TestScheduledOwnerSharedWorkerLimit(t *testing.T) {
	runner := newScheduledOwnerTestRunner(t, nil, nil, nil)
	var active, maximum atomic.Int64
	gate := make(chan struct{})
	var submitted sync.WaitGroup
	for range scheduledTestDefaultMaxWorkers {
		submitted.Add(1)
		require.True(t, runner.owner.submit(runner.owner.ctx, func() {
			n := active.Add(1)
			for old := maximum.Load(); n > old; old = maximum.Load() {
				if maximum.CompareAndSwap(old, n) {
					break
				}
			}
			submitted.Done()
			<-gate
			active.Add(-1)
		}))
	}
	submitted.Wait()
	ctx, cancel := context.WithCancel(runner.owner.ctx)
	cancel()
	require.False(t, runner.owner.submit(ctx, func() { t.Error("exceeded shared worker limit") }))
	close(gate)
	runner.Stop()
	require.Equal(t, int64(scheduledTestDefaultMaxWorkers), maximum.Load())
	require.Zero(t, active.Load())
}
