//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/isolatedfixture"
	"github.com/lib/pq"
)

func TestCustomMigrationLedgerReadOnlyRole(t *testing.T) {
	if integrationDB == nil {
		t.Fatal("isolated repository database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := fmt.Sprintf("migration_check_%d", time.Now().UnixNano())
	ident := pq.QuoteIdentifier(name)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := integrationDB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE ROLE " + ident + " NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT")
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := integrationDB.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+ident+" CASCADE"); err != nil {
			t.Error(err)
		}
		if _, err := integrationDB.ExecContext(cleanupCtx, "DROP ROLE "+ident); err != nil {
			t.Error(err)
		}
	})
	exec("CREATE SCHEMA " + ident)
	exec("GRANT USAGE ON SCHEMA " + ident + " TO " + ident)
	dsn, err := isolatedfixture.PostgresDSN("repository")
	if err != nil {
		t.Fatal(err)
	}
	readDB, err := sql.Open("postgres", dsn+"&search_path="+name)
	if err != nil {
		t.Fatal(err)
	}
	readDB.SetMaxOpenConns(1)
	readDB.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := readDB.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, query := range []string{"SET ROLE " + ident, "SET default_transaction_read_only = on"} {
		if _, err := readDB.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		var role, readOnly string
		if err := readDB.QueryRowContext(ctx, "SELECT current_user, current_setting('transaction_read_only')").Scan(&role, &readOnly); err != nil {
			t.Fatal(err)
		}
		if role != name || readOnly != "on" {
			t.Fatalf("fixture lost restricted role/read-only boundary: role=%s readOnly=%s", role, readOnly)
		}
		err := CheckMigrations(ctx, readDB)
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Fatalf("check error=%v want=%q", err, want)
		}
	}
	check("read migration ledger") // No table is created by the check.
	exec("CREATE TABLE " + ident + ".schema_migrations AS SELECT * FROM public.schema_migrations")
	exec("GRANT SELECT ON " + ident + ".schema_migrations TO " + ident)
	var canInsert bool
	if err := readDB.QueryRowContext(ctx, "SELECT has_table_privilege(current_user, $1, 'INSERT')", name+".schema_migrations").Scan(&canInsert); err != nil {
		t.Fatal(err)
	}
	if canInsert {
		t.Fatal("restricted role unexpectedly has write permission")
	}
	if _, err := readDB.ExecContext(ctx, "DELETE FROM schema_migrations"); err == nil {
		t.Fatal("read-only connection accepted a write")
	}
	check("")
	validateOperation, err := startupMigrationOperation("validate")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOperation(ctx, readDB); err != nil {
		t.Fatalf("startup validate attempted a write or rejected current ledger: %v", err)
	}
	applyOperation, err := startupMigrationOperation("")
	if err != nil {
		t.Fatal(err)
	}
	if err := applyOperation(ctx, readDB); err == nil {
		t.Fatal("ordinary/default startup unexpectedly used read-only validation instead of existing apply runner")
	}
	exec("INSERT INTO " + ident + ".schema_migrations(filename, checksum) VALUES ('historical_extra.sql', 'old')")
	check("")
	const file = "247_scoped_admin_api_keys.sql"
	exec("DELETE FROM "+ident+".schema_migrations WHERE filename=$1", file)
	check(file) // The other custom247 entry cannot mask this pending filename.
	exec("INSERT INTO "+ident+".schema_migrations SELECT * FROM public.schema_migrations WHERE filename=$1", file)
	check("")
	exec("UPDATE "+ident+".schema_migrations SET checksum='synthetic-do-not-log' WHERE filename=$1", file)
	err = CheckMigrations(ctx, readDB)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || strings.Contains(err.Error(), "synthetic-do-not-log") {
		t.Fatalf("checksum drift diagnostic invalid: %v", err)
	}
}
