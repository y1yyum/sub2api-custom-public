package custombridge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
)

//go:embed migrations/002_receipt_delivery.sql
var relaySchema string

// ApplyRelaySchema 显式应用投递状态迁移，要求已有 M0 回执表。
func ApplyRelaySchema(ctx context.Context, db *sql.DB) error {
	return applyRelayMigration(ctx, db, "bridge", "relay_schema_migrations", "002", relaySchema)
}

func applyRelayMigration(ctx context.Context, db *sql.DB, schema, table, version, migration string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, schema+"."+table); err != nil {
		return err
	}
	qualified := schema + "." + table
	if _, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema+`; CREATE TABLE IF NOT EXISTS `+qualified+` (
		version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`); err != nil {
		return err
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(migration)))
	var recorded string
	err = tx.QueryRowContext(ctx, `SELECT checksum FROM `+qualified+` WHERE version = $1`, version).Scan(&recorded)
	if err == nil {
		if recorded != checksum {
			return errors.New("bridge migration checksum mismatch")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, migration); err != nil {
		return fmt.Errorf("apply bridge migration %s: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+qualified+` (version, checksum) VALUES ($1, $2)`, version, checksum); err != nil {
		return err
	}
	return tx.Commit()
}
