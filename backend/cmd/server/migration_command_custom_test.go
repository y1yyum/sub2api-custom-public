//go:build unit

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestCustomMigrationCLIConflictingModes(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		flags := [4]bool{}
		count := 0
		for i := range flags {
			flags[i] = mask&(1<<i) != 0
			if flags[i] {
				count++
			}
		}
		err := validateServerModeFlags(flags[0], flags[1], flags[2], flags[3])
		if (err != nil) != (count > 1) {
			t.Fatalf("mask=%d conflict err=%v", mask, err)
		}
	}
}

func TestCustomMigrationCLIIsBoundedAndRedactsErrors(t *testing.T) {
	for _, validate := range []bool{false, true} {
		cfg := &config.Config{}
		loads, runs := 0, 0
		err := executeMigrationCommand(validate, func() (*config.Config, error) { loads++; return cfg, nil }, func(ctx context.Context, got *config.Config, check bool) error {
			runs++
			if got != cfg || check != validate {
				t.Fatal("wrong configuration or explicit operation")
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 10*time.Minute {
				t.Fatal("missing finite command timeout")
			}
			return nil
		})
		if err != nil || loads != 1 || runs != 1 {
			t.Fatalf("err=%v loads=%d runs=%d", err, loads, runs)
		}
	}
	const secret = "synthetic-password-do-not-log"
	runs := 0
	err := executeMigrationCommand(true, func() (*config.Config, error) { return nil, errors.New(secret) }, func(context.Context, *config.Config, bool) error { runs++; return nil })
	if err == nil || strings.Contains(err.Error(), secret) || runs != 0 {
		t.Fatalf("configuration error not safely contained: %v", err)
	}
	for _, validate := range []bool{false, true} {
		err := executeMigrationCommand(validate, func() (*config.Config, error) { return &config.Config{}, nil }, func(context.Context, *config.Config, bool) error { return errors.New(secret) })
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("migration error not safely contained: %v", err)
		}
	}
}

func TestCustomAutoSetupMigrationModePreflight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("database:\n  migration_mode: apply\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	for _, tc := range []struct {
		auto, mode string
		reject     bool
	}{
		{"true", "validate", true}, {"1", " Validate ", true}, {"yes", "validate", true},
		{"true", "apply", false}, {"true", "", false}, {"true", "skip", true},
		{"false", "validate", false}, {"", "validate", false},
	} {
		t.Run(tc.auto+"_"+tc.mode, func(t *testing.T) {
			t.Setenv("AUTO_SETUP", tc.auto)
			t.Setenv("DATABASE_MIGRATION_MODE", tc.mode)
			if err := validateAutoSetupMigrationMode(); (err != nil) != tc.reject {
				t.Fatalf("rejection=%v err=%v", tc.reject, err)
			}
		})
	}
}
