package scheduledowner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

//go:embed schema.sql
var schema string

const schemaLock int64 = 739438781220014

func ApplySchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, schemaLock); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS scheduled_owner;
 CREATE TABLE IF NOT EXISTS scheduled_owner.schema_migrations(version TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`); err != nil {
		return err
	}
	applied, err := checkLedger(ctx, tx)
	if err != nil {
		return err
	}
	if !applied {
		if _, err = tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scheduled_owner.database_identity(database_id) VALUES($1)`, uuid.NewString()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scheduled_owner.schema_migrations(version,checksum) VALUES('001',$1)`, checksum()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checksum() string { return fmt.Sprintf("%x", sha256.Sum256([]byte(schema))) }

func checkLedger(ctx context.Context, q querier) (bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT version,checksum FROM scheduled_owner.schema_migrations`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	applied := false
	for rows.Next() {
		var version, digest string
		if err := rows.Scan(&version, &digest); err != nil {
			return false, err
		}
		if version != "001" || digest != checksum() {
			return false, errors.New("scheduled owner schema version or checksum mismatch")
		}
		applied = true
	}
	return applied, rows.Err()
}

func (s *Store) CheckSchema(ctx context.Context) error {
	applied, err := checkLedger(ctx, s.db)
	if err != nil {
		return err
	}
	if !applied {
		return errors.New("scheduled owner schema not applied")
	}
	_, err = s.DatabaseID(ctx)
	return err
}
