package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type catalogIntelligenceRepository struct {
	db *sql.DB
}

func NewCatalogIntelligenceRepository(db *sql.DB) service.CatalogIntelligenceRepository {
	return &catalogIntelligenceRepository{db: db}
}

func (r *catalogIntelligenceRepository) ListCatalogPlans(ctx context.Context) ([]service.CatalogIntelligencePlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, model_id, kind
		FROM scheduled_test_plans
		WHERE show_in_catalog AND kind IN ($1, $2)
		ORDER BY id
	`, service.ScheduledTestKindCharArt, service.ScheduledTestKindQuiz)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var plans []service.CatalogIntelligencePlan
	for rows.Next() {
		var p service.CatalogIntelligencePlan
		if err := rows.Scan(&p.ID, &p.AccountID, &p.ModelID, &p.Kind); err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

func (r *catalogIntelligenceRepository) ListRecentResults(ctx context.Context, planIDs []int64, perPlan int) ([]*service.ScheduledTestResult, error) {
	if len(planIDs) == 0 || perPlan <= 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+scheduledTestResultColumns+`
		FROM (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY plan_id ORDER BY created_at DESC, id DESC) AS rn
			FROM scheduled_test_results
			WHERE kind <> 'probe' AND plan_id = ANY($1)
		) ranked
		WHERE rn <= $2
		ORDER BY plan_id, created_at DESC, id DESC
	`, pq.Array(planIDs), perPlan)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*service.ScheduledTestResult
	for rows.Next() {
		res, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, rows.Err()
}
