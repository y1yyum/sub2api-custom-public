//go:build unit

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestNormalizeScheduledTestPlan(t *testing.T) {
	probe := &ScheduledTestPlan{Prompt: "x", ShowInCatalog: true, AutoRecover: true, MaxResults: 5}
	require.NoError(t, normalizeScheduledTestPlan(probe))
	require.Equal(t, ScheduledTestKindProbe, probe.Kind)
	require.Equal(t, ScheduledTestPickRotate, probe.PickMode)
	require.Empty(t, probe.Prompt)
	require.False(t, probe.ShowInCatalog)
	require.True(t, probe.AutoRecover, "probes keep auto-recover")

	charArt := &ScheduledTestPlan{Kind: ScheduledTestKindCharArt, Prompt: " draw ", AutoRecover: true, MaxResults: 20, ShowInCatalog: true}
	require.NoError(t, normalizeScheduledTestPlan(charArt))
	require.Equal(t, "draw", charArt.Prompt)
	require.False(t, charArt.AutoRecover)
	require.True(t, charArt.ShowInCatalog)

	quiz := &ScheduledTestPlan{Kind: ScheduledTestKindQuiz, Prompt: "ignored", PickMode: ScheduledTestPickRandom, MaxResults: 50}
	require.NoError(t, normalizeScheduledTestPlan(quiz))
	require.Empty(t, quiz.Prompt)
	require.Equal(t, ScheduledTestPickRandom, quiz.PickMode)

	for name, plan := range map[string]*ScheduledTestPlan{
		"unknown kind":         {Kind: "essay", MaxResults: 50},
		"char_art no prompt":   {Kind: ScheduledTestKindCharArt, Prompt: " ", MaxResults: 50},
		"char_art few kept":    {Kind: ScheduledTestKindCharArt, Prompt: "draw", MaxResults: 19},
		"quiz few kept":        {Kind: ScheduledTestKindQuiz, MaxResults: 10},
		"unknown pick mode":    {Kind: ScheduledTestKindQuiz, PickMode: "weighted", MaxResults: 50},
		"char_art long prompt": {Kind: ScheduledTestKindCharArt, Prompt: string(make([]rune, 2001)), MaxResults: 50},
	} {
		t.Run(name, func(t *testing.T) {
			err := normalizeScheduledTestPlan(plan)
			require.Error(t, err)
			require.True(t, infraerrors.IsBadRequest(err))
		})
	}
}

type fakeQuestionRepo struct {
	IntelligenceQuestionRepository
	enabled  []*IntelligenceQuestion
	cursor   int
	links    map[int64][]int64
	setCalls int
	setErr   error
}

func (r *fakeQuestionRepo) ListEnabledForPlan(context.Context, int64) ([]*IntelligenceQuestion, error) {
	return r.enabled, nil
}

func (r *fakeQuestionRepo) AdvanceRotation(context.Context, int64) (int, error) {
	r.cursor++
	return r.cursor - 1, nil
}

func (r *fakeQuestionRepo) SetPlanQuestions(_ context.Context, planID int64, ids []int64) error {
	r.setCalls++
	if r.setErr != nil {
		return r.setErr
	}
	if r.links == nil {
		r.links = map[int64][]int64{}
	}
	r.links[planID] = ids
	return nil
}

func (r *fakeQuestionRepo) ListPlanQuestionIDs(_ context.Context, planIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	for _, id := range planIDs {
		out[id] = r.links[id]
	}
	return out, nil
}

type fakeScheduledPlanRepo struct {
	ScheduledTestPlanRepository
	nextID       int64
	plans        map[int64]*ScheduledTestPlan
	deleted      []int64
	afterRunPlan []int64
}

func (r *fakeScheduledPlanRepo) Create(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	r.nextID++
	saved := *plan
	saved.ID = r.nextID
	if r.plans == nil {
		r.plans = map[int64]*ScheduledTestPlan{}
	}
	r.plans[saved.ID] = &saved
	return &saved, nil
}

func (r *fakeScheduledPlanRepo) Update(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	saved := *plan
	r.plans[plan.ID] = &saved
	return &saved, nil
}

func (r *fakeScheduledPlanRepo) Delete(_ context.Context, id int64) error {
	r.deleted = append(r.deleted, id)
	delete(r.plans, id)
	return nil
}

func (r *fakeScheduledPlanRepo) UpdateAfterRun(_ context.Context, id int64, _, _ time.Time) error {
	r.afterRunPlan = append(r.afterRunPlan, id)
	return nil
}

type fakeScheduledResultRepo struct {
	ScheduledTestResultRepository
	saved []*ScheduledTestResult
}

func (r *fakeScheduledResultRepo) Create(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	r.saved = append(r.saved, result)
	return result, nil
}

func (r *fakeScheduledResultRepo) PruneOldResults(context.Context, int64, int) error { return nil }

func newKindsTestService(questions *fakeQuestionRepo) (*ScheduledTestService, *fakeScheduledPlanRepo, *fakeScheduledResultRepo) {
	plans := &fakeScheduledPlanRepo{}
	results := &fakeScheduledResultRepo{}
	svc := NewScheduledTestService(plans, results)
	svc.questionRepo = questions
	return svc, plans, results
}

func TestNextQuizQuestionRotatesAndSkipsEmpty(t *testing.T) {
	questions := &fakeQuestionRepo{enabled: []*IntelligenceQuestion{{ID: 1}, {ID: 2}, {ID: 3}}}
	svc, _, _ := newKindsTestService(questions)
	plan := &ScheduledTestPlan{ID: 9, Kind: ScheduledTestKindQuiz, PickMode: ScheduledTestPickRotate}

	var picked []int64
	for range 4 {
		q, err := svc.nextQuizQuestion(context.Background(), plan)
		require.NoError(t, err)
		picked = append(picked, q.ID)
	}
	require.Equal(t, []int64{1, 2, 3, 1}, picked)

	plan.PickMode = ScheduledTestPickRandom
	q, err := svc.nextQuizQuestion(context.Background(), plan)
	require.NoError(t, err)
	require.Contains(t, []int64{1, 2, 3}, q.ID)
	require.Equal(t, 4, questions.cursor, "random picks leave the rotation cursor alone")

	questions.enabled = nil
	q, err = svc.nextQuizQuestion(context.Background(), plan)
	require.NoError(t, err)
	require.Nil(t, q)
}

func TestCreatePlanWithQuestions(t *testing.T) {
	ctx := context.Background()
	questions := &fakeQuestionRepo{}
	svc, plans, _ := newKindsTestService(questions)

	quiz, err := svc.CreatePlanWithQuestions(ctx, &ScheduledTestPlan{Kind: ScheduledTestKindQuiz, CronExpression: "0 * * * *"}, []int64{3, 1, 3})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 1}, quiz.QuestionIDs)
	require.Equal(t, []int64{3, 1}, questions.links[quiz.ID])
	require.Equal(t, 50, quiz.MaxResults)

	_, err = svc.CreatePlanWithQuestions(ctx, &ScheduledTestPlan{Kind: ScheduledTestKindCharArt, Prompt: "draw", CronExpression: "0 * * * *"}, []int64{1})
	require.Error(t, err)
	require.Len(t, plans.plans, 1, "rejected before creating the plan")

	questions.setErr = ErrIntelligenceQuestionNotFound
	_, err = svc.CreatePlanWithQuestions(ctx, &ScheduledTestPlan{Kind: ScheduledTestKindQuiz, CronExpression: "0 * * * *"}, []int64{99})
	require.ErrorIs(t, err, ErrIntelligenceQuestionNotFound)
	require.Len(t, plans.plans, 1, "plan removed when its questions cannot be linked")
	require.Len(t, plans.deleted, 1)
}

func TestUpdatePlanWithQuestionsValidatesFirst(t *testing.T) {
	ctx := context.Background()
	questions := &fakeQuestionRepo{}
	svc, plans, _ := newKindsTestService(questions)
	created, err := svc.CreatePlanWithQuestions(ctx, &ScheduledTestPlan{Kind: ScheduledTestKindQuiz, CronExpression: "0 * * * *"}, []int64{1})
	require.NoError(t, err)

	bad := *created
	bad.MaxResults = 5
	_, err = svc.UpdatePlanWithQuestions(ctx, &bad, []int64{2})
	require.Error(t, err)
	badCron := *created
	badCron.CronExpression = "not cron"
	_, err = svc.UpdatePlanWithQuestions(ctx, &badCron, []int64{2})
	require.Error(t, err)
	require.Equal(t, 1, questions.setCalls, "rejected updates leave the links untouched")

	updated, err := svc.UpdatePlanWithQuestions(ctx, created, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, updated.QuestionIDs, "nil keeps and reports the current links")
	require.Equal(t, 1, questions.setCalls)
	require.Contains(t, plans.plans, created.ID)
}

func TestRunnerQuizPlanGradesAndSnapshots(t *testing.T) {
	ctx := context.Background()
	question := &IntelligenceQuestion{ID: 7, Prompt: "6*7=?", PromptImage: testIntelligencePromptImage, AnswerRule: IntelligenceRuleNumber, ExpectedAnswers: []string{"42"}}
	questions := &fakeQuestionRepo{enabled: []*IntelligenceQuestion{question}}
	svc, plans, results := newKindsTestService(questions)

	for _, tc := range []struct {
		reply   string
		verdict string
	}{{"6*7 = 42", ScheduledTestVerdictPass}, {"41", ScheduledTestVerdictFail}} {
		accountTestSvc, bodies := newClaudePromptFixture(t, tc.reply)
		runner := NewScheduledTestRunnerService(plans, svc, accountTestSvc, nil, nil)
		plan := &ScheduledTestPlan{ID: 5, AccountID: 41, ModelID: "public-model", Kind: ScheduledTestKindQuiz, PickMode: ScheduledTestPickRotate, MaxResults: 20, CronExpression: "0 * * * *"}
		runner.runOnePlan(ctx, plan)

		content := (*bodies)[0]["messages"].([]any)[0].(map[string]any)["content"].([]any)
		require.Len(t, content, 1)
		require.Equal(t, "image", content[0].(map[string]any)["type"])
		require.Equal(t, strings.TrimPrefix(testIntelligencePromptImage, intelligenceImagePrefix), content[0].(map[string]any)["source"].(map[string]any)["data"])
		saved := results.saved[len(results.saved)-1]
		require.Equal(t, ScheduledTestKindQuiz, saved.Kind)
		require.Equal(t, int64(7), *saved.QuestionID)
		require.Equal(t, "6*7=?", saved.Prompt)
		require.Equal(t, "42", saved.ExpectedAnswer)
		require.Equal(t, "upstream-model", saved.ModelID)
		require.Equal(t, tc.verdict, saved.Verdict)
	}
	require.Equal(t, []int64{5, 5}, plans.afterRunPlan)
}

func TestRunnerSkipsQuizWithoutQuestionsButAdvances(t *testing.T) {
	svc, plans, results := newKindsTestService(&fakeQuestionRepo{})
	accountTestSvc, bodies := newClaudePromptFixture(t, "unused")
	runner := NewScheduledTestRunnerService(plans, svc, accountTestSvc, nil, nil)

	runner.runOnePlan(context.Background(), &ScheduledTestPlan{ID: 6, AccountID: 41, Kind: ScheduledTestKindQuiz, CronExpression: "0 * * * *"})

	require.Empty(t, *bodies)
	require.Empty(t, results.saved)
	require.Equal(t, []int64{6}, plans.afterRunPlan)
}

func TestRunnerCharArtAndProbePrompts(t *testing.T) {
	svc, plans, results := newKindsTestService(&fakeQuestionRepo{})
	accountTestSvc, bodies := newClaudePromptFixture(t, "#")
	runner := NewScheduledTestRunnerService(plans, svc, accountTestSvc, nil, nil)

	runner.runOnePlan(context.Background(), &ScheduledTestPlan{ID: 1, AccountID: 41, ModelID: "public-model", Kind: ScheduledTestKindCharArt, Prompt: "draw it", CronExpression: "0 * * * *"})
	runner.runOnePlan(context.Background(), &ScheduledTestPlan{ID: 2, AccountID: 41, ModelID: "public-model", Kind: ScheduledTestKindProbe, CronExpression: "0 * * * *"})

	require.Equal(t, "draw it", claudeRequestText(t, (*bodies)[0]))
	require.Equal(t, "hi", claudeRequestText(t, (*bodies)[1]))
	require.Equal(t, ScheduledTestKindCharArt, results.saved[0].Kind)
	require.Equal(t, "#", results.saved[0].ResponseText)
	require.Empty(t, results.saved[0].Verdict)
	require.Equal(t, ScheduledTestKindProbe, results.saved[1].Kind)
}
