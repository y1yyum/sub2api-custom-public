package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/lib/pq"
)

// startupMigrationOperation changes only migration handling, not subsequent
// application bootstrap/seeders. A validate-mode server is NOT wholly read-only.
func startupMigrationOperation(mode string) (func(context.Context, *sql.DB) error, error) {
	mode, err := (config.DatabaseConfig{MigrationMode: mode}).EffectiveMigrationMode()
	if err != nil {
		return nil, err
	}
	if mode == config.DatabaseMigrationModeValidate {
		return CheckMigrations, nil
	}
	return ApplyMigrations, nil
}

// RunConfiguredMigrations opens only PostgreSQL. It deliberately does not use
// InitEnt or application wiring, Redis, workers, secret bootstrap, or seeders.
// The explicit CLI operation overrides the startup-only migration_mode setting.
func RunConfiguredMigrations(ctx context.Context, cfg *config.Config, validateOnly bool) error {
	if ctx == nil || cfg == nil {
		return errors.New("migration command requires context and config")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	connector, err := pq.NewConnector(cfg.Database.DSNWithTimezone(cfg.Timezone))
	if err != nil {
		return errors.New("invalid database connection configuration")
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()
	applyDBPoolSettings(db, cfg)
	if err := db.PingContext(ctx); err != nil {
		return errors.New("migration database connection failed")
	}
	if validateOnly {
		return CheckMigrations(ctx, db)
	}
	return ApplyMigrations(ctx, db)
}
