package service

import (
	"context"
	"errors"
)

// errScheduledTestSkipped marks a run with nothing to send; the plan still
// advances to its next run.
var errScheduledTestSkipped = errors.New("scheduled test skipped: no enabled questions")

// runPlanTest executes one run of the plan according to its kind.
func (s *ScheduledTestRunnerService) runPlanTest(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestResult, error) {
	switch plan.Kind {
	case ScheduledTestKindCharArt:
		return s.runKindTest(ctx, plan, plan.Prompt)
	case ScheduledTestKindQuiz:
		question, err := s.scheduledSvc.nextQuizQuestion(ctx, plan)
		if err != nil {
			return nil, err
		}
		if question == nil {
			return nil, errScheduledTestSkipped
		}
		result, err := s.runKindTest(ctx, plan, question.Prompt)
		if err != nil {
			return nil, err
		}
		result.QuestionID = &question.ID
		result.ExpectedAnswer = question.ExpectedAnswerText()
		if result.Status == "success" {
			result.Verdict = ScheduledTestVerdictFail
			if question.Grade(result.ResponseText) {
				result.Verdict = ScheduledTestVerdictPass
			}
		}
		return result, nil
	default:
		return s.runKindTest(ctx, plan, "")
	}
}

func (s *ScheduledTestRunnerService) runKindTest(ctx context.Context, plan *ScheduledTestPlan, prompt string) (*ScheduledTestResult, error) {
	result, err := s.accountTestSvc.RunTestBackground(ctx, plan.AccountID, plan.ModelID, prompt)
	if err != nil {
		return nil, err
	}
	result.Kind = plan.Kind
	return result, nil
}
