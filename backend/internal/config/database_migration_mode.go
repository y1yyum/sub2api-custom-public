package config

import (
	"fmt"
	"strings"
)

const (
	DatabaseMigrationModeApply    = "apply"
	DatabaseMigrationModeValidate = "validate"
)

// EffectiveMigrationMode retains apply for zero-value/ordinary configurations.
// Invalid explicit values fail closed rather than silently applying migrations.
func (d DatabaseConfig) EffectiveMigrationMode() (string, error) {
	mode := strings.ToLower(strings.TrimSpace(d.MigrationMode))
	if mode == "" {
		mode = DatabaseMigrationModeApply
	}
	switch mode {
	case DatabaseMigrationModeApply, DatabaseMigrationModeValidate:
		return mode, nil
	default:
		return "", fmt.Errorf("database.migration_mode must be %q or %q", DatabaseMigrationModeApply, DatabaseMigrationModeValidate)
	}
}
