//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var integrationDB *sql.DB
var integrationDSN string

func TestMain(m *testing.M) {
	os.Exit(runPublicIntegration(m))
}

func runPublicIntegration(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23",
		tcpostgres.WithDatabase("sub2api_public_test"), tcpostgres.WithUsername("fixture"),
		tcpostgres.WithPassword("fixture-test-only"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		fmt.Fprintln(os.Stderr, "start disposable PostgreSQL:", err)
		return 1
	}
	defer func() { _ = container.Terminate(context.Background()) }()
	integrationDSN, err = container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	integrationDB, err = sql.Open("postgres", integrationDSN)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer integrationDB.Close()
	if err = ApplyMigrations(ctx, integrationDB); err != nil {
		fmt.Fprintln(os.Stderr, "apply disposable migrations:", err)
		return 1
	}
	return m.Run()
}
