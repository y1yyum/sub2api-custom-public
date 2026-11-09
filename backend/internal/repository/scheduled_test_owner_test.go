//go:build unit

package repository

import (
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestScheduledOwnerWorkerPoolReservesExecutionConnection(t *testing.T) {
	db, err := sql.Open("postgres", "")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := &scheduledTestPlanRepository{db: db}
	for _, tc := range []struct{ maxOpen, workers int }{
		{0, 10}, {1, 0}, {2, 1}, {5, 4}, {11, 10}, {50, 10},
	} {
		db.SetMaxOpenConns(tc.maxOpen)
		require.Equal(t, tc.workers, repo.ScheduledOwnerMaxWorkers())
	}
}
