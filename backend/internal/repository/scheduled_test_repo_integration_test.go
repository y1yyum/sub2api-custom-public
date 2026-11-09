//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestRepositoriesRoundTripKinds(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "scheduled-test-kinds"})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id = $1`, account.ID)
	})
	plans := NewScheduledTestPlanRepository(integrationDB, testEntClient(t), nil)
	results := NewScheduledTestResultRepository(integrationDB)
	nextRun := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	probe, err := plans.Create(ctx, &service.ScheduledTestPlan{
		AccountID: account.ID, ModelID: "m", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 5, NextRunAt: &nextRun,
	})
	require.NoError(t, err)
	require.Equal(t, service.ScheduledTestKindProbe, probe.Kind)
	require.Equal(t, service.ScheduledTestPickRotate, probe.PickMode)
	require.Empty(t, probe.Prompt)
	require.False(t, probe.ShowInCatalog)

	charArt, err := plans.Create(ctx, &service.ScheduledTestPlan{
		AccountID: account.ID, ModelID: "m", CronExpression: "0 * * * *", Enabled: true, MaxResults: 20,
		Kind: service.ScheduledTestKindCharArt, Prompt: "draw it", ShowInCatalog: true, NextRunAt: &nextRun,
	})
	require.NoError(t, err)
	require.Equal(t, service.ScheduledTestKindCharArt, charArt.Kind)
	require.Equal(t, "draw it", charArt.Prompt)
	require.True(t, charArt.ShowInCatalog)

	charArt.Kind = service.ScheduledTestKindQuiz
	charArt.Prompt = "draw it again"
	charArt.PickMode = service.ScheduledTestPickRandom
	charArt.ShowInCatalog = false
	updated, err := plans.Update(ctx, charArt)
	require.NoError(t, err)
	require.Equal(t, service.ScheduledTestKindCharArt, updated.Kind, "kind is fixed at creation")
	require.Equal(t, "draw it again", updated.Prompt)
	require.Equal(t, service.ScheduledTestPickRandom, updated.PickMode)
	require.False(t, updated.ShowInCatalog)

	listed, err := plans.ListByAccountID(ctx, account.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)

	started := time.Now().UTC()
	saved, err := results.Create(ctx, &service.ScheduledTestResult{
		PlanID: charArt.ID, Kind: service.ScheduledTestKindCharArt, ModelID: "upstream-model", Prompt: "draw it again",
		Status: "success", ResponseText: "#  #\n####", LatencyMs: 12, StartedAt: started, FinishedAt: started,
	})
	require.NoError(t, err)
	require.Equal(t, service.ScheduledTestKindCharArt, saved.Kind)
	require.Equal(t, "upstream-model", saved.ModelID)
	require.Nil(t, saved.QuestionID)
	require.Empty(t, saved.Verdict)

	legacy, err := results.Create(ctx, &service.ScheduledTestResult{PlanID: probe.ID, Status: "success", StartedAt: started, FinishedAt: started})
	require.NoError(t, err)
	require.Equal(t, service.ScheduledTestKindProbe, legacy.Kind)

	got, err := results.ListByPlanID(ctx, charArt.ID, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "#  #\n####", got[0].ResponseText)
	require.Equal(t, "draw it again", got[0].Prompt)
}
