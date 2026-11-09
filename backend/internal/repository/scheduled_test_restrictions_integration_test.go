//go:build integration

package repository

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledQuizRestrictionsLifecycle(t *testing.T) {
	ctx := context.Background()
	cache := NewSchedulerCache(testRedis(t))
	accounts := newAccountRepositoryWithSQL(testEntClient(t), integrationDB, cache)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "quiz-restrictions", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})
	plans := NewScheduledTestPlanRepository(integrationDB, testEntClient(t), cache)
	started := time.Now().UTC().Truncate(time.Second)
	due := started.Add(-time.Minute)
	newPlan := func(action string) *service.ScheduledTestPlan {
		p, err := plans.Create(ctx, &service.ScheduledTestPlan{AccountID: account.ID, ModelID: "public", CronExpression: "0 * * * *", Enabled: true, MaxResults: 20, Kind: service.ScheduledTestKindQuiz, FailureAction: action, NextRunAt: &due})
		require.NoError(t, err)
		return p
	}
	outcome := func(p *service.ScheduledTestPlan, verdict string, offset time.Duration) {
		require.NoError(t, plans.ApplyQuizOutcome(ctx, p, &service.ScheduledTestResult{Kind: service.ScheduledTestKindQuiz, Status: "success", Verdict: verdict, ModelID: "upstream", StartedAt: started.Add(offset)}))
	}
	get := func() *service.Account {
		a, err := accounts.GetByID(ctx, account.ID)
		require.NoError(t, err)
		return a
	}
	modelPlan := newPlan(service.ScheduledTestFailureDisableModel)
	accountPlan := newPlan(service.ScheduledTestFailureDisableAccount)
	bucket := service.SchedulerBucket{GroupID: 0, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{*get()}))
	staleAccount := get()
	outcome(modelPlan, service.ScheduledTestVerdictFail, 0)
	require.True(t, get().IsScheduledTestModelBlocked("public"))
	require.True(t, get().IsScheduledTestModelBlocked("upstream"))
	require.False(t, get().IsScheduledTestModelBlocked("healthy"))
	require.True(t, get().IsSchedulable())
	cached, err := cache.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, cached.IsScheduledTestModelBlocked("upstream"))
	snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, snapshot, 1)
	require.True(t, snapshot[0].IsScheduledTestModelBlocked("public"))
	outcome(accountPlan, service.ScheduledTestVerdictFail, 0)
	staleAccount.Name = "edited while restricted"
	require.NoError(t, accounts.Update(ctx, staleAccount))
	require.True(t, get().IsScheduledTestModelBlocked("public"))
	require.False(t, get().IsSchedulable(), "账号普通编辑不能覆盖新限制")
	staleRestrictions := get()
	listed, err := plans.ListDue(ctx, started)
	require.NoError(t, err)
	var ids []int64
	for _, p := range listed {
		ids = append(ids, p.ID)
	}
	require.Contains(t, ids, accountPlan.ID, "隔离后继续执行定时复测")
	outcome(modelPlan, service.ScheduledTestVerdictPass, time.Second)
	require.True(t, get().IsScheduledTestAccountBlocked(), "另一个计划的限制不能解除")
	require.NoError(t, plans.ApplyQuizOutcome(ctx, accountPlan, &service.ScheduledTestResult{Status: "failed", StartedAt: started.Add(2 * time.Second)}))
	require.True(t, get().IsScheduledTestAccountBlocked())
	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET status = 'disabled', schedulable = false, rate_limit_reset_at = $2 WHERE id = $1", account.ID, started.Add(time.Hour))
	require.NoError(t, err)
	outcome(accountPlan, service.ScheduledTestVerdictPass, 3*time.Second)
	restored := get()
	require.False(t, restored.IsScheduledTestAccountBlocked())
	require.Equal(t, "disabled", restored.Status)
	require.False(t, restored.Schedulable)
	require.NotNil(t, restored.RateLimitResetAt)
	outcome(accountPlan, service.ScheduledTestVerdictFail, 2*time.Second)
	require.False(t, get().IsScheduledTestAccountBlocked(), "迟到的失败不能覆盖更新的通过")
	staleRestrictions.Status = restored.Status
	staleRestrictions.Schedulable = restored.Schedulable
	require.NoError(t, accounts.Update(ctx, staleRestrictions))
	require.False(t, get().IsScheduledTestAccountBlocked(), "旧编辑快照不能复活已解除限制")
	outcome(modelPlan, service.ScheduledTestVerdictFail, 4*time.Second)
	modelPlan.Enabled = false
	updated, err := plans.Update(ctx, modelPlan)
	require.NoError(t, err)
	require.Greater(t, updated.PolicyVersion, modelPlan.PolicyVersion)
	require.False(t, get().IsScheduledTestModelBlocked("public"))
	outcome(modelPlan, service.ScheduledTestVerdictFail, 5*time.Second)
	require.False(t, get().IsScheduledTestModelBlocked("public"), "旧配置结果不能加回限制")
	outcome(accountPlan, service.ScheduledTestVerdictFail, 6*time.Second)
	require.NoError(t, plans.Delete(ctx, accountPlan.ID))
	require.False(t, get().IsScheduledTestAccountBlocked())
	outcome(accountPlan, service.ScheduledTestVerdictFail, 7*time.Second)
	require.False(t, get().IsScheduledTestAccountBlocked())
}

func TestScheduledQuizRestrictionsConcurrentOwnersAndPolicyEdits(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{Name: "quiz-concurrent"})
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", account.ID) })
	plans := NewScheduledTestPlanRepository(integrationDB, client, nil)
	accounts := newAccountRepositoryWithSQL(client, integrationDB, nil)
	var owners []*service.ScheduledTestPlan
	for range 5 {
		p, err := plans.Create(ctx, &service.ScheduledTestPlan{AccountID: account.ID, ModelID: "public", CronExpression: "0 * * * *", Enabled: true, MaxResults: 20, Kind: service.ScheduledTestKindQuiz, FailureAction: service.ScheduledTestFailureDisableModel})
		require.NoError(t, err)
		owners = append(owners, p)
	}
	result := &service.ScheduledTestResult{Status: "success", Verdict: service.ScheduledTestVerdictFail, ModelID: "upstream", StartedAt: time.Now().UTC()}
	var wg sync.WaitGroup
	errs := make(chan error, len(owners))
	for _, p := range owners {
		wg.Go(func() { errs <- plans.ApplyQuizOutcome(ctx, p, result) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	getRestrictions := func() map[string]any {
		a, err := accounts.GetByID(ctx, account.ID)
		require.NoError(t, err)
		restrictions, _ := a.Extra[service.AccountExtraScheduledTestRestrictions].(map[string]any)
		return restrictions
	}
	require.Len(t, getRestrictions(), 5)
	for i, p := range owners[:3] {
		switch i {
		case 0:
			p.ModelID = "other"
		case 1:
			p.FailureAction = service.ScheduledTestFailureNone
		case 2:
			p.FailureAction = service.ScheduledTestFailureDisableAccount
		}
		_, err := plans.Update(ctx, p)
		require.NoError(t, err)
		require.NotContains(t, getRestrictions(), strconv.FormatInt(p.ID, 10))
	}
	require.Len(t, getRestrictions(), 2)
	pass := *result
	pass.Verdict = service.ScheduledTestVerdictPass
	pass.StartedAt = pass.StartedAt.Add(time.Second)
	require.NoError(t, plans.ApplyQuizOutcome(ctx, owners[3], &pass))
	require.Len(t, getRestrictions(), 1)
}
