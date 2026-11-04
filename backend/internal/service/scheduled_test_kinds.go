package service

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const scheduledTestDisplayMinResults = 20

func invalidScheduledTestPlan(message string) error {
	return infraerrors.BadRequest("SCHEDULED_TEST_PLAN_INVALID", message)
}

// normalizeScheduledTestPlan applies kind defaults and rejects fields that do
// not fit the kind. Only probes auto-recover accounts, and they never show in
// the catalog; char_art and quiz keep at least 20 results so the catalog can
// show the last 20 runs.
func normalizeScheduledTestPlan(plan *ScheduledTestPlan) error {
	if plan.Kind == "" {
		plan.Kind = ScheduledTestKindProbe
	}
	if plan.FailureAction == "" {
		plan.FailureAction = ScheduledTestFailureNone
	}
	switch plan.FailureAction {
	case ScheduledTestFailureNone, ScheduledTestFailureDisableModel, ScheduledTestFailureDisableAccount:
	default:
		return invalidScheduledTestPlan("failure_action must be none, disable_model or disable_account")
	}
	if plan.Kind != ScheduledTestKindQuiz && plan.FailureAction != ScheduledTestFailureNone {
		return invalidScheduledTestPlan("failure_action is only supported for quiz plans")
	}
	if plan.PickMode == "" {
		plan.PickMode = ScheduledTestPickRotate
	}
	if plan.PickMode != ScheduledTestPickRotate && plan.PickMode != ScheduledTestPickRandom {
		return invalidScheduledTestPlan("pick_mode must be rotate or random")
	}
	plan.Prompt = strings.TrimSpace(plan.Prompt)

	switch plan.Kind {
	case ScheduledTestKindProbe:
		plan.Prompt = ""
		plan.ShowInCatalog = false
		return nil
	case ScheduledTestKindCharArt:
		if plan.Prompt == "" {
			return invalidScheduledTestPlan("prompt is required for char_art plans")
		}
		if utf8.RuneCountInString(plan.Prompt) > intelligencePromptMaxRunes {
			return invalidScheduledTestPlan("prompt must be at most 2000 characters")
		}
	case ScheduledTestKindQuiz:
		plan.Prompt = ""
	default:
		return invalidScheduledTestPlan("kind must be probe, char_art or quiz")
	}
	plan.AutoRecover = false
	if plan.MaxResults < scheduledTestDisplayMinResults {
		return invalidScheduledTestPlan("max_results must be at least 20 for char_art and quiz plans")
	}
	return nil
}

// ListQuestions returns the whole bank.
func (s *ScheduledTestService) ListQuestions(ctx context.Context) ([]*IntelligenceQuestion, error) {
	return s.questionRepo.List(ctx)
}

func (s *ScheduledTestService) CreateQuestion(ctx context.Context, q *IntelligenceQuestion) (*IntelligenceQuestion, error) {
	if err := q.Normalize(); err != nil {
		return nil, err
	}
	return s.questionRepo.Create(ctx, q)
}

func (s *ScheduledTestService) GetQuestion(ctx context.Context, id int64) (*IntelligenceQuestion, error) {
	return s.questionRepo.GetByID(ctx, id)
}

func (s *ScheduledTestService) UpdateQuestion(ctx context.Context, q *IntelligenceQuestion) (*IntelligenceQuestion, error) {
	if err := q.Normalize(); err != nil {
		return nil, err
	}
	return s.questionRepo.Update(ctx, q)
}

// DeleteQuestion removes the question and its plan links; results keep their snapshots.
func (s *ScheduledTestService) DeleteQuestion(ctx context.Context, id int64) error {
	return s.questionRepo.Delete(ctx, id)
}

// GradePreview grades a sample reply against an unsaved question.
func (s *ScheduledTestService) GradePreview(q *IntelligenceQuestion, reply string) (bool, error) {
	if err := q.normalizeAnswerSpec(); err != nil {
		return false, err
	}
	return q.Grade(reply), nil
}

// CreatePlanWithQuestions creates the plan and links quiz questions; the plan
// is removed again when the questions cannot be linked.
func (s *ScheduledTestService) CreatePlanWithQuestions(ctx context.Context, plan *ScheduledTestPlan, questionIDs []int64) (*ScheduledTestPlan, error) {
	if len(questionIDs) > 0 && plan.Kind != ScheduledTestKindQuiz {
		return nil, invalidScheduledTestPlan("question_ids only apply to quiz plans")
	}
	created, err := s.CreatePlan(ctx, plan)
	if err != nil || created.Kind != ScheduledTestKindQuiz {
		return created, err
	}
	if err := s.SetPlanQuestions(ctx, created, questionIDs); err != nil {
		_ = s.planRepo.Delete(ctx, created.ID)
		return nil, err
	}
	return created, nil
}

// UpdatePlanWithQuestions validates the plan before touching its questions so
// a rejected update changes nothing; nil questionIDs keeps the current links.
func (s *ScheduledTestService) UpdatePlanWithQuestions(ctx context.Context, plan *ScheduledTestPlan, questionIDs []int64) (*ScheduledTestPlan, error) {
	if err := normalizeScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	if _, err := computeNextRun(plan.CronExpression, time.Now()); err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	if questionIDs != nil {
		if err := s.SetPlanQuestions(ctx, plan, questionIDs); err != nil {
			return nil, err
		}
	}
	updated, err := s.UpdatePlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	return updated, s.attachPlanQuestionIDs(ctx, []*ScheduledTestPlan{updated})
}

// SetPlanQuestions replaces a quiz plan's questions, keeping the given order.
func (s *ScheduledTestService) SetPlanQuestions(ctx context.Context, plan *ScheduledTestPlan, questionIDs []int64) error {
	if plan.Kind != ScheduledTestKindQuiz {
		return invalidScheduledTestPlan("question_ids only apply to quiz plans")
	}
	ids := make([]int64, 0, len(questionIDs))
	seen := make(map[int64]struct{}, len(questionIDs))
	for _, id := range questionIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if err := s.questionRepo.SetPlanQuestions(ctx, plan.ID, ids); err != nil {
		return err
	}
	plan.QuestionIDs = ids
	return nil
}

func (s *ScheduledTestService) attachPlanQuestionIDs(ctx context.Context, plans []*ScheduledTestPlan) error {
	var quizIDs []int64
	for _, plan := range plans {
		if plan.Kind == ScheduledTestKindQuiz {
			quizIDs = append(quizIDs, plan.ID)
		}
	}
	if len(quizIDs) == 0 {
		return nil
	}
	links, err := s.questionRepo.ListPlanQuestionIDs(ctx, quizIDs)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if plan.Kind == ScheduledTestKindQuiz {
			plan.QuestionIDs = append([]int64{}, links[plan.ID]...)
		}
	}
	return nil
}

// nextQuizQuestion picks the question for this run, or nil when the plan has
// no enabled questions.
func (s *ScheduledTestService) nextQuizQuestion(ctx context.Context, plan *ScheduledTestPlan) (*IntelligenceQuestion, error) {
	questions, err := s.questionRepo.ListEnabledForPlan(ctx, plan.ID)
	if err != nil || len(questions) == 0 {
		return nil, err
	}
	if plan.PickMode == ScheduledTestPickRandom {
		return questions[rand.IntN(len(questions))], nil
	}
	cursor, err := s.questionRepo.AdvanceRotation(ctx, plan.ID)
	if err != nil {
		return nil, err
	}
	index := cursor % len(questions)
	if index < 0 {
		index += len(questions)
	}
	return questions[index], nil
}
