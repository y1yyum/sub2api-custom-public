//go:build unit

package service

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "modernc.org/sqlite"
)

func TestBuildAffiliateClaimQueryPostgresPartialIndex(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// 仅构建 SQL，底层连接不执行 PostgreSQL 语句，也不访问网络。
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	query, args := buildAffiliateRebateAuditClaimQuery(client, "42", "{}")
	normalized := strings.Join(strings.Fields(query), " ")
	const target = "ON CONFLICT (order_id, action) WHERE action NOT IN ('REFUND_PENDING', 'REFUND_FAILED') DO NOTHING RETURNING id"
	if !strings.HasSuffix(normalized, target) {
		t.Fatalf("PostgreSQL conflict target must match migration 247: %s", normalized)
	}
	if !reflect.DeepEqual(args, []any{"42", "{}"}) {
		t.Fatalf("unexpected claim arguments: %#v", args)
	}
}

func TestAffiliateClaimSQLiteFullUniqueIndex(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// 对齐现有 SQLite 单测自建的全局唯一索引，不套用 PostgreSQL 迁移。
	_, err = db.ExecContext(ctx, `CREATE TABLE payment_audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		order_id TEXT NOT NULL, action TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '',
		operator TEXT NOT NULL DEFAULT 'system', created_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX idx_payment_audit_logs_order_action_uniq
	ON payment_audit_logs(order_id, action);`)
	if err != nil {
		t.Fatal(err)
	}
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	query, args := buildAffiliateRebateAuditClaimQuery(client, "42", "{}")
	if strings.Contains(query, "action NOT IN") || !reflect.DeepEqual(args, []any{"42", "{}", "42"}) {
		t.Fatalf("SQLite must retain the full-index conflict target: %s %#v", query, args)
	}
	svc := &PaymentService{}
	for attempt, want := range []bool{true, false} {
		claimed, err := svc.tryClaimAffiliateRebateAudit(ctx, client, 42, 1)
		if err != nil || claimed != want {
			t.Fatalf("attempt %d: claimed=%v want=%v err=%v", attempt, claimed, want, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO payment_audit_logs (order_id, action, created_at)
		VALUES ('43', 'AFFILIATE_REBATE_SKIPPED', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if claimed, err := svc.tryClaimAffiliateRebateAudit(ctx, client, 43, 1); err != nil || claimed {
		t.Fatalf("skipped order: claimed=%v err=%v", claimed, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM payment_audit_logs").Scan(&count); err != nil || count != 2 {
		t.Fatalf("expected exactly two affiliate markers: count=%d err=%v", count, err)
	}
}
