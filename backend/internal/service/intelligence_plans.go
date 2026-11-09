package service

import (
	"context"
	"time"
)

// intelligencePlanRecentLimit is how many recent runs each plan shows on the
// admin intelligence page.
const intelligencePlanRecentLimit = 10

// IntelligencePlanOverview is a char art or quiz plan with its account and
// recent runs, oldest first.
type IntelligencePlanOverview struct {
	*ScheduledTestPlan
	AccountName     string            `json:"account_name"`
	AccountPlatform string            `json:"account_platform"`
	Recent          []IntelligenceRun `json:"recent"`
}

// IntelligenceRun is one plan run. Quiz runs are pass, fail or error; char art
// runs are success or error.
type IntelligenceRun struct {
	CheckedAt time.Time `json:"checked_at"`
	Outcome   string    `json:"outcome"`
}

// ListIntelligencePlans returns the char art and quiz plans of every account.
func (s *ScheduledTestService) ListIntelligencePlans(ctx context.Context) ([]*IntelligencePlanOverview, error) {
	overviews, err := s.questionRepo.ListIntelligencePlans(ctx)
	if err != nil {
		return nil, err
	}
	plans := make([]*ScheduledTestPlan, len(overviews))
	ids := make([]int64, len(overviews))
	for i, o := range overviews {
		plans[i], ids[i] = o.ScheduledTestPlan, o.ID
		o.Recent = []IntelligenceRun{}
	}
	if err := s.attachPlanQuestionIDs(ctx, plans); err != nil {
		return nil, err
	}
	results, err := s.questionRepo.ListRecentResults(ctx, ids, intelligencePlanRecentLimit)
	if err != nil {
		return nil, err
	}
	byPlan := make(map[int64][]IntelligenceRun, len(overviews))
	for _, res := range results {
		byPlan[res.PlanID] = append(byPlan[res.PlanID], IntelligenceRun{CheckedAt: res.StartedAt, Outcome: intelligenceOutcome(res)})
	}
	for _, o := range overviews {
		runs := byPlan[o.ID]
		for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
			runs[i], runs[j] = runs[j], runs[i]
		}
		if runs != nil {
			o.Recent = runs
		}
	}
	return overviews, nil
}

func intelligenceOutcome(res *ScheduledTestResult) string {
	switch {
	case res.Status != "success":
		return CatalogOutcomeError
	case res.Kind == ScheduledTestKindCharArt:
		return CatalogOutcomeSuccess
	case res.Verdict == ScheduledTestVerdictPass:
		return CatalogOutcomePass
	default:
		return CatalogOutcomeFail
	}
}
