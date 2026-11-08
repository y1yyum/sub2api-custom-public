//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestFailureActionValidation(t *testing.T) {
	for _, kind := range []string{ScheduledTestKindProbe, ScheduledTestKindCharArt, ScheduledTestKindQuiz} {
		for _, action := range []string{"", ScheduledTestFailureNone, ScheduledTestFailureDisableModel, ScheduledTestFailureDisableAccount, "invalid"} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				p := &ScheduledTestPlan{Kind: kind, FailureAction: action, Prompt: "draw", MaxResults: 20}
				err := normalizeScheduledTestPlan(p)
				if action == "invalid" || (kind != ScheduledTestKindQuiz && action != "" && action != ScheduledTestFailureNone) {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.NotEmpty(t, p.FailureAction)
				}
			})
		}
	}
}

type quizOutcomePlanRepo struct {
	*fakeScheduledPlanRepo
	outcomes []*ScheduledTestResult
}

func (r *quizOutcomePlanRepo) ApplyQuizOutcome(_ context.Context, _ *ScheduledTestPlan, result *ScheduledTestResult) error {
	r.outcomes = append(r.outcomes, result)
	return nil
}

type quizFailureResultRepo struct{ ScheduledTestResultRepository }

func (quizFailureResultRepo) Create(context.Context, *ScheduledTestResult) (*ScheduledTestResult, error) {
	return nil, errors.New("save failed")
}

type quizProbeAccountRepo struct {
	AccountRepository
	setErrors   int
	clearErrors int
}

func (r *quizProbeAccountRepo) SetError(context.Context, int64, string) error {
	r.setErrors++
	return nil
}
func (r *quizProbeAccountRepo) ClearError(context.Context, int64) error { r.clearErrors++; return nil }

func TestScheduledQuizRunnerAppliesVerdictsOnlyAfterSave(t *testing.T) {
	for _, reply := range []string{"42", "41"} {
		for _, failSave := range []bool{false, true} {
			svc, fakePlans, results := newKindsTestService(&fakeQuestionRepo{enabled: []*IntelligenceQuestion{{ID: 1, Prompt: "6*7=?", PromptImage: testIntelligencePromptImage, AnswerRule: IntelligenceRuleNumber, ExpectedAnswers: []string{"42"}}}})
			plans := &quizOutcomePlanRepo{fakeScheduledPlanRepo: fakePlans}
			if failSave {
				svc.resultRepo = quizFailureResultRepo{}
			}
			probe, _ := newClaudePromptFixture(t, reply)
			runner := NewScheduledTestRunnerService(plans, svc, probe, nil, nil)
			runner.runOnePlan(context.Background(), &ScheduledTestPlan{ID: 1, AccountID: 41, ModelID: "public-model", Kind: ScheduledTestKindQuiz, FailureAction: ScheduledTestFailureDisableModel, PickMode: ScheduledTestPickRotate, CronExpression: "0 * * * *"})
			if failSave {
				require.Empty(t, plans.outcomes)
				continue
			}
			require.Len(t, plans.outcomes, 1)
			require.Same(t, results.saved[0], plans.outcomes[0])
			want := ScheduledTestVerdictFail
			if reply == "42" {
				want = ScheduledTestVerdictPass
			}
			require.Equal(t, want, plans.outcomes[0].Verdict)
			require.Equal(t, "upstream-model", plans.outcomes[0].ModelID)
		}
	}
}

func TestScheduledQuizRestrictionsGeminiMessagesCompat(t *testing.T) {
	svc := &GeminiMessagesCompatService{}
	account := &Account{Platform: PlatformAntigravity, Credentials: map[string]any{"model_mapping": map[string]any{"blocked": "upstream-blocked", "alias": "upstream-blocked", "healthy": "upstream-healthy"}}, Extra: map[string]any{AccountExtraScheduledTestRestrictions: map[string]any{"1": map[string]any{"action": ScheduledTestFailureDisableModel, "model_id": "blocked", "upstream_model": "upstream-blocked"}}}}
	require.False(t, svc.isModelSupportedByAccount(account, "blocked"))
	require.False(t, svc.isModelSupportedByAccount(account, "alias"))
	require.True(t, svc.isModelSupportedByAccount(account, "healthy"))
}

func TestScheduledQuizHTTPFailureDoesNotSetPermanentAccountError(t *testing.T) {
	for _, kind := range []string{ScheduledTestKindQuiz, ScheduledTestKindProbe} {
		probe, _ := newClaudePromptFixture(t, "")
		repo := &quizProbeAccountRepo{AccountRepository: probe.accountRepo}
		probe.accountRepo = repo
		probe.httpUpstream = &systemOneHTTPUpstream{do: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unauthorized"))}, nil
		}}
		runner := &ScheduledTestRunnerService{accountTestSvc: probe}
		result, err := runner.runKindTest(context.Background(), &ScheduledTestPlan{AccountID: 41, ModelID: "public-model", Kind: kind}, "6*7=?")
		require.NoError(t, err)
		require.NotEqual(t, "success", result.Status)
		if kind == ScheduledTestKindQuiz {
			require.Zero(t, repo.setErrors)
		} else {
			require.Equal(t, 1, repo.setErrors)
		}
		require.Same(t, repo, probe.accountRepo)
	}
}
