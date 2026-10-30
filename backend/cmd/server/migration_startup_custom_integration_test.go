//go:build integration

package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/isolatedfixture"
	"github.com/lib/pq"
)

// Exercise main's actual ordering, rather than just the repository dispatcher.
// Before the fix, auto-setup creates the missing database before checking Redis,
// even though the later repository startup dispatcher would select validate.
func TestCustomValidateAutoSetupStartupRejectsBeforeWrites(t *testing.T) {
	if err := isolatedfixture.RequireIsolated(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("SUB2API_TEST_VALIDATE_AUTO_SETUP_CHILD") == "1" {
		// The subprocess is this compiled test binary; no additional Go build or
		// external server executable is needed. Let the real main parse no flags.
		flag.CommandLine = flag.NewFlagSet("server", flag.ExitOnError)
		os.Args = os.Args[:1]
		if os.Getenv("SUB2API_TEST_VALIDATE_AUTO_SETUP_COMMAND") == "check" {
			os.Args = append(os.Args, "--check-migrations")
		}
		main()
		t.Fatal("unsafe startup unexpectedly returned successfully")
	}

	for _, scenario := range []string{"environment", "config_file", "explicit_check"} {
		t.Run(scenario, func(t *testing.T) {
			testValidateAutoSetupStartup(t, scenario)
		})
	}
}

func testValidateAutoSetupStartup(t *testing.T, scenario string) {

	dsn, err := isolatedfixture.PostgresDSN("repository")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("validate_startup_%d", time.Now().UnixNano())
	exists := func() bool {
		t.Helper()
		var found bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	if exists() {
		t.Fatal("synthetic database must be absent before startup")
	}
	// Also clean up the mutation when running the regression against old code.
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := db.ExecContext(cleanupCtx, "DROP DATABASE IF EXISTS "+pq.QuoteIdentifier(name)+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})

	dataDir := t.TempDir()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCustomValidateAutoSetupStartupRejectsBeforeWrites$")
	cmd.Dir = dataDir
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + dataDir, "TZ=UTC",
		"SUB2API_ISOLATED_INTEGRATION=1", "SUB2API_TEST_VALIDATE_AUTO_SETUP_CHILD=1",
		"DATA_DIR=" + dataDir, "AUTO_SETUP=true", "DATABASE_MIGRATION_MODE=validate",
		"DATABASE_HOST=127.0.0.1", "DATABASE_PORT=55432", "DATABASE_USER=fixture",
		"DATABASE_PASSWORD=fixture-only", "DATABASE_DBNAME=" + name, "DATABASE_SSLMODE=disable",
		// No Redis listener is needed: rejection must precede any setup work.
		"REDIS_HOST=127.0.0.1", "REDIS_PORT=1", "JWT_SECRET=fixture-not-a-production-jwt-secret",
	}
	wantDiagnostic := "AUTO_SETUP is incompatible with DATABASE_MIGRATION_MODE=validate"
	if scenario == "config_file" {
		configPath := filepath.Join(t.TempDir(), "policy.yaml")
		if err := os.WriteFile(configPath, []byte("database:\n  migration_mode: validate\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for i, entry := range cmd.Env {
			if strings.HasPrefix(entry, "DATABASE_MIGRATION_MODE=") {
				cmd.Env[i] = "CONFIG_FILE=" + configPath
			}
		}
	}
	if scenario == "explicit_check" {
		cmd.Env = append(cmd.Env, "SUB2API_TEST_VALIDATE_AUTO_SETUP_COMMAND=check")
		// A missing database must fail a read-only check, not run auto-setup or
		// the incompatible-mode guard. This proves explicit CLI precedence.
		wantDiagnostic = "migration ledger check failed"
	}
	output, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("startup did not reject within the bounded deadline: %v", ctx.Err())
	}
	if runErr == nil {
		t.Error("validate plus AUTO_SETUP must fail startup")
	}
	if !strings.Contains(string(output), wantDiagnostic) {
		t.Errorf("startup did not report expected rejection class %q", wantDiagnostic)
	}
	if strings.Contains(string(output), "fixture-only") {
		t.Error("startup diagnostic exposed the database password")
	}
	if exists() {
		t.Error("validate startup created a database before rejecting setup")
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("validate startup wrote files into the initially empty data directory: %v", entries)
	}
}
