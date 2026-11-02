package service

import (
	"context"
	"sort"
	"strings"
	"time"
)

// CatalogBlockIntelligenceUnavailable marks a detail intelligence block whose
// scheduled test data could not be read.
const CatalogBlockIntelligenceUnavailable = "intelligence_unavailable"

// catalogIntelligenceLimit is how many recent results of each kind a row shows.
const catalogIntelligenceLimit = 20

// Outcomes of catalog intelligence items. Quiz items are pass, fail or error;
// char art items are success or error.
const (
	CatalogOutcomePass    = "pass"
	CatalogOutcomeFail    = "fail"
	CatalogOutcomeSuccess = "success"
	CatalogOutcomeError   = "error"
)

// CatalogIntelligencePlan is a char art or quiz plan an admin chose to show
// in the catalog.
type CatalogIntelligencePlan struct {
	ID        int64
	AccountID int64
	ModelID   string
	Kind      string
}

// CatalogIntelligenceRepository reads the scheduled test data shown in the catalog.
type CatalogIntelligenceRepository interface {
	// ListCatalogPlans returns char art and quiz plans with show_in_catalog set.
	ListCatalogPlans(ctx context.Context) ([]CatalogIntelligencePlan, error)
	// ListRecentResults returns up to perPlan newest results of each plan.
	ListRecentResults(ctx context.Context, planIDs []int64, perPlan int) ([]*ScheduledTestResult, error)
}

type catalogAccountSource interface {
	GetByIDs(ctx context.Context, ids []int64) ([]*Account, error)
}

// CatalogIntelligenceSummary is the quiz score of a list row: passed out of
// the successful requests among the recent results.
type CatalogIntelligenceSummary struct {
	Passed int `json:"passed"`
	Scored int `json:"scored"`
}

type CatalogIntelligence struct {
	Quiz    CatalogQuiz          `json:"quiz"`
	CharArt []CatalogCharArtItem `json:"char_art"`
}

type CatalogQuiz struct {
	Passed int               `json:"passed"`
	Scored int               `json:"scored"`
	Items  []CatalogQuizItem `json:"items"`
}

// CatalogQuizItem is one graded run, oldest first. Model is the upstream model
// after account mapping. A failed request carries no reply.
type CatalogQuizItem struct {
	CheckedAt      time.Time `json:"checked_at"`
	Model          string    `json:"model"`
	Outcome        string    `json:"outcome"`
	Prompt         string    `json:"prompt"`
	ResponseText   string    `json:"response_text"`
	ExpectedAnswer string    `json:"expected_answer"`
}

type CatalogCharArtItem struct {
	CheckedAt    time.Time `json:"checked_at"`
	Model        string    `json:"model"`
	Outcome      string    `json:"outcome"`
	ResponseText string    `json:"response_text"`
}

type catalogIntelligenceReader struct {
	repo     CatalogIntelligenceRepository
	accounts catalogAccountSource
}

type catalogAccountSlot struct {
	groupID  int64
	platform string
}

// matchPlans maps each row (key → displayed model name) to the catalog plans
// whose account serves it: the account belongs to the row group, runs the row
// platform, and maps the plan model to the same upstream model as the row
// model. Matching on the plan rather than on result snapshots keeps rows
// working for test paths that never report the upstream model.
func (r *catalogIntelligenceReader) matchPlans(ctx context.Context, rows map[CatalogRowKey]string) (map[CatalogRowKey][]CatalogIntelligencePlan, error) {
	plans, err := r.repo.ListCatalogPlans(ctx)
	if err != nil || len(plans) == 0 {
		return nil, err
	}
	ids := make([]int64, 0, len(plans))
	for _, p := range plans {
		ids = append(ids, p.AccountID)
	}
	accounts, err := r.accounts.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	bySlot := map[catalogAccountSlot][]int{}
	for i, p := range plans {
		a := byID[p.AccountID]
		if a == nil {
			continue
		}
		platform := strings.ToLower(strings.TrimSpace(a.Platform))
		for _, gid := range a.GroupIDs {
			slot := catalogAccountSlot{groupID: gid, platform: platform}
			bySlot[slot] = append(bySlot[slot], i)
		}
	}
	out := map[CatalogRowKey][]CatalogIntelligencePlan{}
	for key, model := range rows {
		for _, i := range bySlot[catalogAccountSlot{groupID: key.GroupID, platform: key.Platform}] {
			a := byID[plans[i].AccountID]
			if strings.EqualFold(a.GetMappedModel(plans[i].ModelID), a.GetMappedModel(model)) {
				out[key] = append(out[key], plans[i])
			}
		}
	}
	return out, nil
}

func (r *catalogIntelligenceReader) recentResults(ctx context.Context, matched map[CatalogRowKey][]CatalogIntelligencePlan, kind string) (map[int64][]*ScheduledTestResult, error) {
	seen := map[int64]struct{}{}
	var ids []int64
	for _, plans := range matched {
		for _, p := range plans {
			if _, ok := seen[p.ID]; ok || (kind != "" && p.Kind != kind) {
				continue
			}
			seen[p.ID] = struct{}{}
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	results, err := r.repo.ListRecentResults(ctx, ids, catalogIntelligenceLimit)
	if err != nil {
		return nil, err
	}
	byPlan := map[int64][]*ScheduledTestResult{}
	for _, res := range results {
		byPlan[res.PlanID] = append(byPlan[res.PlanID], res)
	}
	return byPlan, nil
}

// Summaries scores the recent quiz runs of each row; rows without quiz
// results are absent.
func (r *catalogIntelligenceReader) Summaries(ctx context.Context, rows map[CatalogRowKey]string) (map[CatalogRowKey]CatalogIntelligenceSummary, error) {
	matched, err := r.matchPlans(ctx, rows)
	if err != nil || len(matched) == 0 {
		return nil, err
	}
	byPlan, err := r.recentResults(ctx, matched, ScheduledTestKindQuiz)
	if err != nil {
		return nil, err
	}
	out := map[CatalogRowKey]CatalogIntelligenceSummary{}
	for key, plans := range matched {
		quiz := latestCatalogResults(plans, byPlan, ScheduledTestKindQuiz)
		if len(quiz) == 0 {
			continue
		}
		q := buildCatalogQuiz(quiz)
		out[key] = CatalogIntelligenceSummary{Passed: q.Passed, Scored: q.Scored}
	}
	return out, nil
}

// Details returns the recent quiz and char art runs of one row, or nil when
// the row has neither.
func (r *catalogIntelligenceReader) Details(ctx context.Context, key CatalogRowKey, model string) (*CatalogIntelligence, error) {
	matched, err := r.matchPlans(ctx, map[CatalogRowKey]string{key: model})
	if err != nil || len(matched[key]) == 0 {
		return nil, err
	}
	byPlan, err := r.recentResults(ctx, matched, "")
	if err != nil {
		return nil, err
	}
	quiz := latestCatalogResults(matched[key], byPlan, ScheduledTestKindQuiz)
	charArt := latestCatalogResults(matched[key], byPlan, ScheduledTestKindCharArt)
	if len(quiz) == 0 && len(charArt) == 0 {
		return nil, nil
	}
	out := &CatalogIntelligence{Quiz: buildCatalogQuiz(quiz), CharArt: make([]CatalogCharArtItem, 0, len(charArt))}
	for _, res := range charArt {
		item := CatalogCharArtItem{CheckedAt: res.StartedAt, Model: res.ModelID, Outcome: CatalogOutcomeError}
		if res.Status == "success" {
			item.Outcome, item.ResponseText = CatalogOutcomeSuccess, res.ResponseText
		}
		out.CharArt = append(out.CharArt, item)
	}
	return out, nil
}

// latestCatalogResults merges the results of the matched plans of one kind
// and keeps the newest catalogIntelligenceLimit, oldest first.
func latestCatalogResults(plans []CatalogIntelligencePlan, byPlan map[int64][]*ScheduledTestResult, kind string) []*ScheduledTestResult {
	var merged []*ScheduledTestResult
	for _, p := range plans {
		if p.Kind == kind {
			merged = append(merged, byPlan[p.ID]...)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if !merged[i].CreatedAt.Equal(merged[j].CreatedAt) {
			return merged[i].CreatedAt.After(merged[j].CreatedAt)
		}
		return merged[i].ID > merged[j].ID
	})
	if len(merged) > catalogIntelligenceLimit {
		merged = merged[:catalogIntelligenceLimit]
	}
	for i, j := 0, len(merged)-1; i < j; i, j = i+1, j-1 {
		merged[i], merged[j] = merged[j], merged[i]
	}
	return merged
}

func buildCatalogQuiz(results []*ScheduledTestResult) CatalogQuiz {
	q := CatalogQuiz{Items: make([]CatalogQuizItem, 0, len(results))}
	for _, res := range results {
		item := CatalogQuizItem{
			CheckedAt:      res.StartedAt,
			Model:          res.ModelID,
			Outcome:        CatalogOutcomeError,
			Prompt:         res.Prompt,
			ExpectedAnswer: res.ExpectedAnswer,
		}
		if res.Status == "success" {
			q.Scored++
			item.Outcome, item.ResponseText = CatalogOutcomeFail, res.ResponseText
			if res.Verdict == ScheduledTestVerdictPass {
				q.Passed++
				item.Outcome = CatalogOutcomePass
			}
		}
		q.Items = append(q.Items, item)
	}
	return q
}
