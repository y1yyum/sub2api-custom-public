package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// ListIntelligencePlans skips plans whose account was deleted.
func (r *intelligenceQuestionRepository) ListIntelligencePlans(ctx context.Context) ([]*service.IntelligencePlanOverview, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+scheduledTestPlanColumns+`
		FROM scheduled_test_plans
		WHERE kind IN ($1, $2)
		ORDER BY created_at DESC, id DESC
	`, service.ScheduledTestKindCharArt, service.ScheduledTestKindQuiz)
	if err != nil {
		return nil, err
	}
	plans, err := scanPlans(rows)
	_ = rows.Close()
	if err != nil || len(plans) == 0 {
		return nil, err
	}

	accountIDs := make([]int64, 0, len(plans))
	for _, p := range plans {
		accountIDs = append(accountIDs, p.AccountID)
	}
	accountRows, err := r.db.QueryContext(ctx, `
		SELECT id, name, platform FROM accounts WHERE id = ANY($1) AND deleted_at IS NULL
	`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = accountRows.Close() }()
	type account struct{ name, platform string }
	accounts := map[int64]account{}
	for accountRows.Next() {
		var id int64
		var a account
		if err := accountRows.Scan(&id, &a.name, &a.platform); err != nil {
			return nil, err
		}
		accounts[id] = a
	}
	if err := accountRows.Err(); err != nil {
		return nil, err
	}

	out := make([]*service.IntelligencePlanOverview, 0, len(plans))
	for _, p := range plans {
		a, ok := accounts[p.AccountID]
		if !ok {
			continue
		}
		out = append(out, &service.IntelligencePlanOverview{ScheduledTestPlan: p, AccountName: a.name, AccountPlatform: a.platform})
	}
	return out, nil
}

func (r *intelligenceQuestionRepository) ListRecentResults(ctx context.Context, planIDs []int64, perPlan int) ([]*service.ScheduledTestResult, error) {
	return listRecentScheduledTestResults(ctx, r.db, planIDs, perPlan)
}
