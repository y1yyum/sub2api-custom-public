//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceQuestionRepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "intelligence-questions"})
	repo := NewIntelligenceQuestionRepository(integrationDB)
	var questionIDs []int64
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = integrationDB.ExecContext(bg, `DELETE FROM accounts WHERE id = $1`, account.ID)
		for _, id := range questionIDs {
			_, _ = integrationDB.ExecContext(bg, `DELETE FROM intelligence_questions WHERE id = $1`, id)
		}
	})

	create := func(prompt string, enabled bool) *service.IntelligenceQuestion {
		q, err := repo.Create(ctx, &service.IntelligenceQuestion{
			Prompt: prompt, AnswerRule: service.IntelligenceRuleNumber, ExpectedAnswers: []string{"42", "42.0"}, Tolerance: 0.5, Enabled: enabled,
			PromptImage: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4//8/AAX+Av4N70a4AAAAAElFTkSuQmCC",
		})
		require.NoError(t, err)
		questionIDs = append(questionIDs, q.ID)
		return q
	}
	first := create("q1", true)
	second := create("q2", true)
	disabled := create("q3", false)
	require.Equal(t, []string{"42", "42.0"}, first.ExpectedAnswers)
	require.Equal(t, 0.5, first.Tolerance)
	require.Zero(t, first.PlanCount)
	require.NotEmpty(t, first.PromptImage)

	plan, err := NewScheduledTestPlanRepository(integrationDB, testEntClient(t), nil).Create(ctx, &service.ScheduledTestPlan{
		AccountID: account.ID, ModelID: "m", CronExpression: "0 * * * *", Enabled: true, MaxResults: 20, Kind: service.ScheduledTestKindQuiz,
	})
	require.NoError(t, err)

	require.NoError(t, repo.SetPlanQuestions(ctx, plan.ID, []int64{second.ID, disabled.ID, first.ID}))
	links, err := repo.ListPlanQuestionIDs(ctx, []int64{plan.ID})
	require.NoError(t, err)
	require.Equal(t, []int64{second.ID, disabled.ID, first.ID}, links[plan.ID])

	enabled, err := repo.ListEnabledForPlan(ctx, plan.ID)
	require.NoError(t, err)
	require.Len(t, enabled, 2)
	require.Equal(t, second.ID, enabled[0].ID)
	require.Equal(t, first.ID, enabled[1].ID)
	require.Equal(t, first.PromptImage, enabled[1].PromptImage)

	err = repo.SetPlanQuestions(ctx, plan.ID, []int64{first.ID, -1})
	require.ErrorIs(t, err, service.ErrIntelligenceQuestionNotFound)
	links, err = repo.ListPlanQuestionIDs(ctx, []int64{plan.ID})
	require.NoError(t, err)
	require.Len(t, links[plan.ID], 3, "a failed replace keeps the previous links")

	for want := 0; want < 3; want++ {
		cursor, err := repo.AdvanceRotation(ctx, plan.ID)
		require.NoError(t, err)
		require.Equal(t, want, cursor)
	}

	got, err := repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, 1, got.PlanCount)
	got.Prompt = "q1 edited"
	got.PromptImage = second.PromptImage
	got.Enabled = false
	updated, err := repo.Update(ctx, got)
	require.NoError(t, err)
	require.Equal(t, "q1 edited", updated.Prompt)
	require.Equal(t, got.PromptImage, updated.PromptImage)
	require.False(t, updated.Enabled)

	now := time.Now().UTC()
	result, err := NewScheduledTestResultRepository(integrationDB).Create(ctx, &service.ScheduledTestResult{
		PlanID: plan.ID, Kind: service.ScheduledTestKindQuiz, QuestionID: &first.ID, Prompt: "q1", ExpectedAnswer: "42 ±0.5",
		Verdict: service.ScheduledTestVerdictPass, Status: "success", StartedAt: now, FinishedAt: now,
	})
	require.NoError(t, err)

	require.NoError(t, repo.Delete(ctx, first.ID))
	require.ErrorIs(t, repo.Delete(ctx, first.ID), service.ErrIntelligenceQuestionNotFound)
	_, err = repo.GetByID(ctx, first.ID)
	require.ErrorIs(t, err, service.ErrIntelligenceQuestionNotFound)
	links, err = repo.ListPlanQuestionIDs(ctx, []int64{plan.ID})
	require.NoError(t, err)
	require.Equal(t, []int64{second.ID, disabled.ID}, links[plan.ID])

	kept, err := NewScheduledTestResultRepository(integrationDB).ListByPlanID(ctx, plan.ID, 10)
	require.NoError(t, err)
	require.Len(t, kept, 1)
	require.Equal(t, result.ID, kept[0].ID)
	require.Nil(t, kept[0].QuestionID, "deleting a question keeps result snapshots")
	require.Equal(t, "42 ±0.5", kept[0].ExpectedAnswer)

	all, err := repo.List(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 2)
}
