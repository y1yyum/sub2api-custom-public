package service

import (
	"context"
	"time"
)

// Scheduled test plan kinds. Probes keep the original connectivity check;
// char_art sends the plan prompt and keeps the reply for display; quiz asks
// one bank question per run and grades the reply.
const (
	ScheduledTestKindProbe   = "probe"
	ScheduledTestKindCharArt = "char_art"
	ScheduledTestKindQuiz    = "quiz"
)

// Quiz pick modes.
const (
	ScheduledTestPickRotate = "rotate"
	ScheduledTestPickRandom = "random"
)

// Quiz verdicts; empty for probes, char_art and failed requests.
const (
	ScheduledTestVerdictPass = "pass"
	ScheduledTestVerdictFail = "fail"
)

// ScheduledTestPlan represents a scheduled test plan domain model.
type ScheduledTestPlan struct {
	ID             int64      `json:"id"`
	AccountID      int64      `json:"account_id"`
	ModelID        string     `json:"model_id"`
	CronExpression string     `json:"cron_expression"`
	Enabled        bool       `json:"enabled"`
	MaxResults     int        `json:"max_results"`
	AutoRecover    bool       `json:"auto_recover"`
	Kind           string     `json:"kind"`
	Prompt         string     `json:"prompt"`
	PickMode       string     `json:"pick_mode"`
	RotationCursor int        `json:"rotation_cursor"`
	ShowInCatalog  bool       `json:"show_in_catalog"`
	QuestionIDs    []int64    `json:"question_ids,omitempty"` // quiz only; filled by the service
	LastRunAt      *time.Time `json:"last_run_at"`
	NextRunAt      *time.Time `json:"next_run_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ScheduledTestResult represents a single test execution result.
type ScheduledTestResult struct {
	ID             int64     `json:"id"`
	PlanID         int64     `json:"plan_id"`
	Kind           string    `json:"kind"`
	ModelID        string    `json:"model_id"`
	QuestionID     *int64    `json:"question_id"`
	Prompt         string    `json:"prompt"`
	ExpectedAnswer string    `json:"expected_answer"`
	Verdict        string    `json:"verdict"`
	Status         string    `json:"status"`
	ResponseText   string    `json:"response_text"`
	ErrorMessage   string    `json:"error_message"`
	LatencyMs      int64     `json:"latency_ms"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	CreatedAt      time.Time `json:"created_at"`
}

// ScheduledTestPlanRepository defines the data access interface for test plans.
type ScheduledTestPlanRepository interface {
	Create(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	GetByID(ctx context.Context, id int64) (*ScheduledTestPlan, error)
	ListByAccountID(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error)
	ListDue(ctx context.Context, now time.Time) ([]*ScheduledTestPlan, error)
	Update(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	Delete(ctx context.Context, id int64) error
	UpdateAfterRun(ctx context.Context, id int64, lastRunAt time.Time, nextRunAt time.Time) error
}

// ScheduledTestResultRepository defines the data access interface for test results.
type ScheduledTestResultRepository interface {
	Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error)
	ListByPlanID(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error)
	PruneOldResults(ctx context.Context, planID int64, keepCount int) error
}
