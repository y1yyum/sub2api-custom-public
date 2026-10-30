package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/migrations"
)

// CheckMigrations reads only the migration ledger. It does not create tables,
// take a migration lock, repair Atlas metadata, or inspect the actual schema.
// Every nonempty embedded migration must have a compatible full-filename ledger
// entry. Extra historical entries are allowed, as in ApplyMigrations.
func CheckMigrations(ctx context.Context, db *sql.DB) error {
	return checkMigrationsFS(ctx, db, migrations.FS)
}

func checkMigrationsFS(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	if db == nil {
		return errors.New("nil sql db")
	}
	rows, err := db.QueryContext(ctx, "SELECT filename, checksum FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("read migration ledger (apply migrations separately before validation): %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := make(map[string]string)
	for rows.Next() {
		var name, checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			return fmt.Errorf("scan migration ledger: %w", err)
		}
		if _, exists := applied[name]; exists {
			return fmt.Errorf("duplicate migration ledger filename %s", name)
		}
		applied[name] = checksum
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read migration ledger rows: %w", err)
	}
	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)
	var pending []string
	for _, name := range files {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		content := strings.TrimSpace(string(raw))
		if content == "" {
			continue
		}
		sum := sha256.Sum256([]byte(content))
		checksum := hex.EncodeToString(sum[:])
		existing, ok := applied[name]
		if !ok {
			pending = append(pending, name)
			continue
		}
		if existing != checksum && !isMigrationChecksumCompatible(name, existing, checksum) {
			// Do not echo arbitrary database values into operator-facing errors.
			return fmt.Errorf("migration %s checksum mismatch", name)
		}
	}
	if len(pending) != 0 {
		return fmt.Errorf("migration ledger has %d pending migration(s): %s", len(pending), strings.Join(pending, ", "))
	}
	return nil
}
