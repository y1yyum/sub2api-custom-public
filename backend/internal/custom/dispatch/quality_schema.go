package dispatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
)

//go:embed migrations/002_quality.sql
var qualitySchema string

// ApplyQualitySchema 保持独立的 quality 002 迁移账本。
func ApplyQualitySchema(ctx context.Context, db *sql.DB) error {
	if err := ApplySchema(ctx, db); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, storeLock); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS dispatch.quality_schema_migrations (
	 version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`); err != nil {
		return err
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(qualitySchema)))
	rows, err := tx.QueryContext(ctx, `SELECT version,checksum FROM dispatch.quality_schema_migrations`)
	if err != nil {
		return err
	}
	var applied bool
	for rows.Next() {
		var version, recorded string
		if err := rows.Scan(&version, &recorded); err != nil {
			_ = rows.Close()
			return err
		}
		if version != "002" || recorded != checksum {
			_ = rows.Close()
			return errors.New("dispatch quality schema version or checksum mismatch")
		}
		applied = true
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if !applied {
		if _, err := tx.ExecContext(ctx, qualitySchema); err != nil {
			return fmt.Errorf("apply dispatch quality schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.quality_schema_migrations(version,checksum) VALUES ('002',$1)`, checksum); err != nil {
			return err
		}
	}
	return tx.Commit()
}
