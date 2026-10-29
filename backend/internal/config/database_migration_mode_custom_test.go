//go:build unit

package config

import "testing"

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
