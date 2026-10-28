//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
)

// The repository TestMain applies the complete embedded migration set to the
// disposable isolated PostgreSQL database. Two different 247 filenames must
// coexist; previously applied custom migration bytes must never be renumbered.
func TestUpgrade019CustomAndUpstreamMigrationLedger(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if integrationDB == nil {
		t.Fatal("isolated repository migration fixture required")
	}
	for _, name := range []string{"247_payment_audit_refund_retry.sql", "247_scoped_admin_api_keys.sql", "248_group_image_count_billing.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var checksum string
		if err := integrationDB.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename = $1", name).Scan(&checksum); err != nil {
			t.Fatalf("missing full-filename migration %s: %v", name, err)
		}
		if checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(raw))))) {
			t.Fatalf("migration checksum drift: %s", name)
		}
	}
}

// Simulate the relevant already-custom-247 schema inside a rollback-only private
// schema. The two new upstream migrations must preserve pending refund history
// and affiliate idempotency while adding admin keys and default-off image billing.
func TestUpgrade019AfterAppliedCustomRefundMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if integrationDB == nil {
		t.Fatal("isolated repository migration fixture required")
	}
	tx, err := integrationDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	schema := fmt.Sprintf("upgrade019_%d", time.Now().UnixNano())
	exec := func(q string) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE SCHEMA " + schema)
	exec("SET LOCAL search_path TO " + schema)
	exec(`CREATE TABLE users (id BIGINT PRIMARY KEY); CREATE TABLE groups (id BIGINT PRIMARY KEY);
	CREATE TABLE payment_audit_logs (id BIGSERIAL PRIMARY KEY, order_id TEXT NOT NULL, action TEXT NOT NULL);
	CREATE UNIQUE INDEX idx_payment_audit_logs_order_action_uniq ON payment_audit_logs(order_id, action);`)
	apply := func(name string) {
		t.Helper()
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		exec(string(raw))
	}
	apply("247_payment_audit_refund_retry.sql")
	exec(`INSERT INTO payment_audit_logs(order_id,action) VALUES ('existing','REFUND_PENDING'),('existing','REFUND_FAILED'),('existing','REFUND_PENDING');
	INSERT INTO users(id) VALUES (1); INSERT INTO groups(id) VALUES (1);`)
	apply("247_scoped_admin_api_keys.sql")
	apply("248_group_image_count_billing.sql")
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM payment_audit_logs WHERE order_id='existing'").Scan(&n); err != nil || n != 3 {
		t.Fatalf("existing refund audit changed: count=%d err=%v", n, err)
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT image_count_billing FROM groups WHERE id=1").Scan(&enabled); err != nil || enabled {
		t.Fatalf("image billing must default off: enabled=%v err=%v", enabled, err)
	}
	exec(`INSERT INTO scoped_admin_api_keys(id,name,key_prefix,key_hash,created_by) VALUES ('fixture','fixture','fixture','fixture-hash',1);`)
	const claim = `INSERT INTO payment_audit_logs(order_id,action) VALUES ('affiliate','AFFILIATE_REBATE_APPLIED') ON CONFLICT (order_id,action) WHERE action NOT IN ('REFUND_PENDING','REFUND_FAILED') DO NOTHING`
	for i, want := range []int64{1, 0} {
		result, err := tx.ExecContext(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
		got, err := result.RowsAffected()
		if err != nil || got != want {
			t.Fatalf("affiliate claim %d: rows=%d want=%d err=%v", i, got, want, err)
		}
	}
}
