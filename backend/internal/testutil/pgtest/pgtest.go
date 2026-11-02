//go:build unit

// The unit tag keeps testcontainers out of untagged builds, so govulncheck
// and production binaries never treat its dependencies as reachable.

// Package pgtest gives tests a real PostgreSQL: SCOPED_ADMIN_TEST_DSN when
// set, otherwise one disposable container per test binary (removed by the
// testcontainers reaper on exit). Like the integration harness, a missing
// database fails under CI and skips elsewhere.
package pgtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const image = "postgres:18.1-alpine3.23"

var (
	once     sync.Once
	dsn      string
	startErr error
)

// DSN returns a key=value connection string, so callers can append options
// such as " search_path=<schema>".
func DSN(t *testing.T) string {
	t.Helper()
	if env := os.Getenv("SCOPED_ADMIN_TEST_DSN"); env != "" {
		return env
	}
	once.Do(start)
	if startErr != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("disposable PostgreSQL unavailable (CI=true): %v", startErr)
		}
		t.Skipf("disposable PostgreSQL unavailable (start Docker to enable): %v", startErr)
	}
	return dsn
}

func start() {
	// testcontainers panics instead of returning an error when no Docker
	// host is reachable.
	defer func() {
		if r := recover(); r != nil {
			startErr = fmt.Errorf("%v", r)
		}
	}()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("sub2api_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		startErr = err
		return
	}
	host, hostErr := container.Host(ctx)
	port, portErr := container.MappedPort(ctx, "5432/tcp")
	if startErr = errors.Join(hostErr, portErr); startErr != nil {
		return
	}
	dsn = fmt.Sprintf("host=%s port=%d user=postgres password=postgres dbname=sub2api_test sslmode=disable",
		host, port.Int())
}
