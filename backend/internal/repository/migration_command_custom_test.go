//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestCustomMigrationCommandRejectsInvalidInputsBeforeConnect(t *testing.T) {
	for _, mode := range []string{"", "apply", "validate", " VALIDATE "} {
		operation, err := startupMigrationOperation(mode)
		if err != nil || operation == nil {
			t.Fatalf("mode=%q err=%v", mode, err)
		}
	}
	if operation, err := startupMigrationOperation("skip"); err == nil || operation != nil {
		t.Fatal("unknown startup migration mode did not fail closed")
	}
	if err := RunConfiguredMigrations(context.Background(), nil, true); err == nil {
		t.Fatal("nil configuration accepted")
	}
	if err := RunConfiguredMigrations(nil, &config.Config{}, true); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, _, err := InitEnt(&config.Config{Database: config.DatabaseConfig{MigrationMode: "unknown"}}); err == nil {
		t.Fatal("invalid startup mode reached database setup")
	}
}
