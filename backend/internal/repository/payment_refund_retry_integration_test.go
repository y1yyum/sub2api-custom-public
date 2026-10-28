//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestPaymentAuditLogRefundRetryConstraints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if integrationDB == nil {
		t.Fatal("integrationDB must be initialized by TestMain with ApplyMigrations")
	}

	t.Run("repeated refund attempts retain every event", func(t *testing.T) {
		tx, err := integrationDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()

		// 两次 pending -> failed 后允许第三次 pending；不依赖 ent 自动建表。
		for attempt, action := range []string{
			"REFUND_PENDING", "REFUND_FAILED",
			"REFUND_PENDING", "REFUND_FAILED", "REFUND_PENDING",
		} {
			_, err := tx.ExecContext(ctx, `INSERT INTO payment_audit_logs (order_id, action, detail)
				VALUES ($1, $2, $3)`, "refund-retry-regression", action, strconv.Itoa(attempt))
			if err != nil {
				t.Fatalf("event %d (%s): %v", attempt, action, err)
			}
		}
		var total, details int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(DISTINCT detail)
			FROM payment_audit_logs WHERE order_id = $1`, "refund-retry-regression").Scan(&total, &details); err != nil {
			t.Fatal(err)
		}
		if total != 5 || details != 5 {
			t.Fatalf("audit events lost: total=%d distinct details=%d", total, details)
		}
	})

	t.Run("affiliate claim upsert remains compatible", func(t *testing.T) {
		tx, err := integrationDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		// 保持 payment_fulfillment.go 的 PostgreSQL 返利占位 SQL 形状。
		// 普通 INSERT 的重复约束测试不能覆盖 ON CONFLICT 的索引推断。
		const claim = `INSERT INTO payment_audit_logs (order_id, action, detail, operator, created_at)
SELECT $1::text, 'AFFILIATE_REBATE_APPLIED', $2::text, 'system', NOW()
WHERE NOT EXISTS (
	SELECT 1 FROM payment_audit_logs
	WHERE order_id = $1::text
	  AND action IN ('AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED')
)
ON CONFLICT (order_id, action)
WHERE action NOT IN ('REFUND_PENDING', 'REFUND_FAILED')
DO NOTHING
RETURNING id`
		var id int64
		if err := tx.QueryRowContext(ctx, claim, "refund-retry-rebate-claim", "{}").Scan(&id); err != nil {
			t.Fatalf("first affiliate claim: %v", err)
		}
		if err := tx.QueryRowContext(ctx, claim, "refund-retry-rebate-claim", "{}").Scan(&id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("replayed affiliate claim must be a no-op, got %v", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO payment_audit_logs (order_id, action)
			VALUES ($1, 'AFFILIATE_REBATE_SKIPPED')`, "refund-retry-rebate-skipped"); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(ctx, claim, "refund-retry-rebate-skipped", "{}").Scan(&id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("skipped affiliate claim must remain a no-op, got %v", err)
		}
	})

	for _, action := range []string{
		"AFFILIATE_REBATE_APPLIED", "AFFILIATE_REBATE_SKIPPED",
		"REFUND_ROLLBACK_FAILED", "REFUND_SUCCESS", "REFUND_REQUESTED",
		"REFUND_QUERY_PENDING", "COMPLETED", "FUTURE_ACTION",
	} {
		t.Run("unique "+action, func(t *testing.T) {
			tx, err := integrationDB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			const insert = `INSERT INTO payment_audit_logs (order_id, action) VALUES ($1, $2)`
			if _, err := tx.ExecContext(ctx, insert, "refund-retry-protected", action); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, "SAVEPOINT duplicate_action"); err != nil {
				t.Fatal(err)
			}
			_, err = tx.ExecContext(ctx, insert, "refund-retry-protected", action)
			var pgErr *pq.Error
			if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.Constraint != "idx_payment_audit_logs_order_action_uniq" {
				t.Fatalf("expected original order/action uniqueness, got %v", err)
			}
			if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT duplicate_action"); err != nil {
				t.Fatal(err)
			}
			// 相同动作在不同订单仍应允许。
			if _, err := tx.ExecContext(ctx, insert, "refund-retry-other-order", action); err != nil {
				t.Fatal(err)
			}
		})
	}
}
