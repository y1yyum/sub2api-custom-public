package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/viper"
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

// LoadDatabaseMigrationMode reads only the startup migration policy, using the
// same sources and environment precedence as Load. Unlike Load, it does not
// initialize timezone state, generate secrets or validate unrelated settings.
// This must run before setup's data-directory probing or database bootstrap.
func LoadDatabaseMigrationMode() (string, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	configureConfigSource(v.SetConfigFile, v.AddConfigPath)
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.SetDefault("database.migration_mode", DatabaseMigrationModeApply)
	if err := v.ReadInConfig(); err != nil {
		var missing viper.ConfigFileNotFoundError
		if !errors.As(err, &missing) {
			// Do not surface config contents, paths or parser diagnostics.
			return "", errors.New("startup migration policy configuration could not be read")
		}
	}
	return (DatabaseConfig{MigrationMode: v.GetString("database.migration_mode")}).EffectiveMigrationMode()
}
