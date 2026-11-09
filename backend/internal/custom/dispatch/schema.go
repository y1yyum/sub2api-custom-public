package dispatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
)

//go:embed migrations/001_dispatch.sql
var dispatchSchema string

//go:embed migrations/003_failure_holds.sql
var failureHoldsSchema string

//go:embed migrations/004_probe_budget.sql
var probeBudgetSchema string

//go:embed migrations/005_publish_lineage.sql
var publishLineageSchema string

//go:embed migrations/006_metrics_retention.sql
var metricsSchema string

const storeLock int64 = 813028810208

// ApplySchema 仅由显式迁移命令调用，不接入 Core 的迁移或启动路径。
func ApplySchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, storeLock); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS dispatch;
		CREATE TABLE IF NOT EXISTS dispatch.schema_migrations (
		version TEXT PRIMARY KEY, checksum TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`); err != nil {
		return err
	}
	migrations := []struct{ version, body string }{{"001", dispatchSchema}, {"003", failureHoldsSchema}, {"004", probeBudgetSchema},
		{"005", publishLineageSchema}, {"006", metricsSchema}}
	checksums := make(map[string]string, len(migrations))
	for _, migration := range migrations {
		checksums[migration.version] = fmt.Sprintf("%x", sha256.Sum256([]byte(migration.body)))
	}
	rows, err := tx.QueryContext(ctx, `SELECT version, checksum FROM dispatch.schema_migrations`)
	if err != nil {
		return err
	}
	applied := make(map[string]bool)
	for rows.Next() {
		var version, recorded string
		if err := rows.Scan(&version, &recorded); err != nil {
			_ = rows.Close()
			return err
		}
		if checksums[version] == "" || recorded != checksums[version] {
			_ = rows.Close()
			return errors.New("dispatch schema version or checksum mismatch")
		}
		applied[version] = true
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	missing := false
	for _, migration := range migrations {
		if !applied[migration.version] {
			missing = true
		} else if missing {
			return errors.New("dispatch schema migration order mismatch")
		}
	}
	for _, migration := range migrations {
		if applied[migration.version] {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.body); err != nil {
			return fmt.Errorf("apply dispatch schema %s: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.schema_migrations(version, checksum) VALUES ($1, $2)`, migration.version, checksums[migration.version]); err != nil {
			return err
		}
	}
	return tx.Commit()
}
