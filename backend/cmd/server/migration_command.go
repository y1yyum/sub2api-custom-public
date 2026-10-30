package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/setup"
)

func validateServerModeFlags(setup, version, migrate, check bool) error {
	count := 0
	for _, enabled := range []bool{setup, version, migrate, check} {
		if enabled {
			count++
		}
	}
	if count > 1 {
		return errors.New("--setup, --version, --migrate-only and --check-migrations are mutually exclusive")
	}
	return nil
}

func runMigrationCommand(validateOnly bool) error {
	if err := executeMigrationCommand(validateOnly, config.LoadForBootstrap, repository.RunConfiguredMigrations); err != nil {
		return err
	}
	if validateOnly {
		log.Println("Migration ledger is current (read-only check; not a schema audit)")
	} else {
		log.Println("Database migrations applied; application was not started")
	}
	return nil
}

// Keep CLI dispatch independent from setup, application initialization and its
// side effects. Errors are deliberately classified, not logged with DSN/details.
func executeMigrationCommand(validateOnly bool, load func() (*config.Config, error), run func(context.Context, *config.Config, bool) error) error {
	cfg, err := load()
	if err != nil {
		return errors.New("migration configuration could not be loaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := run(ctx, cfg, validateOnly); err != nil {
		if validateOnly {
			return errors.New("migration ledger check failed: verify connectivity, read permissions and missing/pending/incompatible ledger entries")
		}
		return errors.New("migration apply failed: verify connectivity, migration permissions and database migration state")
	}
	return nil
}

// Explicit CLI operations are dispatched before this startup-only guard. Reject
// the combination even for installed deployments: auto-setup is a write-capable
// policy and must not silently override a requested validation-only migration.
func validateAutoSetupMigrationMode() error {
	if !setup.AutoSetupEnabled() {
		return nil
	}
	mode, err := config.LoadDatabaseMigrationMode()
	if err != nil {
		return err
	}
	if mode == config.DatabaseMigrationModeValidate {
		return errors.New("AUTO_SETUP is incompatible with DATABASE_MIGRATION_MODE=validate; finish setup in apply mode, then disable AUTO_SETUP before selecting validate")
	}
	return nil
}
