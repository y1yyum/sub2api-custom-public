package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ApplyQuizOutcome serializes results against plan edits, then changes only this
// plan's restriction. The watermark survives a pass so late failures cannot undo it.
func (r *scheduledTestPlanRepository) ApplyQuizOutcome(ctx context.Context, plan *service.ScheduledTestPlan, result *service.ScheduledTestResult) error {
	if plan.Kind != service.ScheduledTestKindQuiz || result.Status != "success" ||
		(result.Verdict != service.ScheduledTestVerdictPass && result.Verdict != service.ScheduledTestVerdictFail) {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var accountID int64
	err = tx.QueryRowContext(ctx, `UPDATE scheduled_test_plans SET last_quiz_outcome_at = $3
  WHERE id = $1 AND policy_version = $2 AND enabled = true
   AND kind = 'quiz' AND failure_action IN ('disable_model', 'disable_account')
   AND (last_quiz_outcome_at IS NULL OR last_quiz_outcome_at < $3)
  RETURNING account_id
  `, plan.ID, plan.PolicyVersion, result.StartedAt).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if result.Verdict == service.ScheduledTestVerdictPass {
		err = clearScheduledTestRestriction(ctx, tx, accountID, plan.ID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET extra = jsonb_set(COALESCE(extra, '{}'::jsonb),
    '{scheduled_test_restrictions}',
    COALESCE(extra -> 'scheduled_test_restrictions', '{}'::jsonb) ||
     jsonb_build_object($2::text, jsonb_build_object('action', $3::text, 'model_id', $4::text, 'upstream_model', $5::text))),
    updated_at = NOW()
   WHERE id = $1 AND deleted_at IS NULL
    `, accountID, strconv.FormatInt(plan.ID, 10), plan.FailureAction, plan.ModelID, result.ModelID)
	}
	if err != nil {
		return err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	r.accounts.syncSchedulerAccountSnapshotDetached(ctx, accountID)
	return nil
}

func clearScheduledTestRestriction(ctx context.Context, tx *sql.Tx, accountID, planID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE accounts SET extra = extra #- ARRAY['scheduled_test_restrictions', $2::text], updated_at = NOW()
  WHERE id = $1 AND extra -> 'scheduled_test_restrictions' ? $2::text
  `, accountID, strconv.FormatInt(planID, 10))
	return err
}

func (r *scheduledTestPlanRepository) updateWithQuizRestriction(ctx context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	previous, err := scanPlan(tx.QueryRowContext(ctx, "SELECT "+scheduledTestPlanColumns+" FROM scheduled_test_plans WHERE id = $1 FOR UPDATE", plan.ID))
	if err != nil {
		return nil, err
	}
	if plan.FailureAction == "" {
		plan.FailureAction = service.ScheduledTestFailureNone
	}
	updated, err := updateScheduledTestPlan(ctx, tx, plan)
	if err != nil {
		return nil, err
	}
	clear := !updated.Enabled || updated.FailureAction != previous.FailureAction || updated.ModelID != previous.ModelID
	if clear {
		if err = clearScheduledTestRestriction(ctx, tx, previous.AccountID, plan.ID); err != nil {
			return nil, err
		}
		if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &previous.AccountID, nil, nil); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if clear {
		r.accounts.syncSchedulerAccountSnapshotDetached(ctx, previous.AccountID)
	}
	return updated, nil
}

func (r *scheduledTestPlanRepository) deleteWithQuizRestriction(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var accountID int64
	err = tx.QueryRowContext(ctx, "DELETE FROM scheduled_test_plans WHERE id = $1 RETURNING account_id", id).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = clearScheduledTestRestriction(ctx, tx, accountID, id); err != nil {
		return err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	r.accounts.syncSchedulerAccountSnapshotDetached(ctx, accountID)
	return nil
}
