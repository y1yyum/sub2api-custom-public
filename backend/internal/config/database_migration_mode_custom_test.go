//go:build unit

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomDatabaseMigrationModeOptIn(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", DatabaseMigrationModeApply}, {"apply", DatabaseMigrationModeApply},
		{" APPLY ", DatabaseMigrationModeApply}, {"validate", DatabaseMigrationModeValidate},
		{" Validate ", DatabaseMigrationModeValidate},
	} {
		got, err := (DatabaseConfig{MigrationMode: tc.input}).EffectiveMigrationMode()
		if err != nil || got != tc.want {
			t.Fatalf("input=%q mode=%q err=%v", tc.input, got, err)
		}
	}
	for _, invalid := range []string{"auto", "disabled", "skip", "validate; apply"} {
		if _, err := (DatabaseConfig{MigrationMode: invalid}).EffectiveMigrationMode(); err == nil {
			t.Fatalf("invalid mode accepted: %q", invalid)
		}
	}
}

func TestCustomDatabaseMigrationModeBootstrapSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	t.Setenv("CONFIG_FILE", path)
	for _, tc := range []struct {
		name, yaml, env, want string
		invalid               bool
	}{
		{"default", "database: {}", "", "apply", false},
		{"config", "database:\n  migration_mode: validate", "", "validate", false},
		{"env_apply_over_config", "database:\n  migration_mode: validate", "apply", "apply", false},
		{"env_validate_over_config", "database:\n  migration_mode: apply", " Validate ", "validate", false},
		{"invalid_mode", "database: {}", "skip", "", true},
		{"invalid_file", "database: [fixture-secret", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_MIGRATION_MODE", tc.env)
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadDatabaseMigrationMode()
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("mode=%q err=%v", got, err)
			}
			if err != nil && strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("config contents leaked")
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDatabaseMigrationMode(); err == nil {
		t.Fatal("missing explicit CONFIG_FILE must fail closed")
	}
}
