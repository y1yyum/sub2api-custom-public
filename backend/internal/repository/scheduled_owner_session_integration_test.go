//go:build integration

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/custom/scheduledowner"
	"github.com/stretchr/testify/require"
)

func TestScheduledOwnerSessionTimezonePreservesCorePool(t *testing.T) {
	db, store := ownerStoreDB(t)
	db.SetMaxOpenConns(1)
	ownerStoreExec(t, db, `SET TIME ZONE 'Africa/Johannesburg'`)
	plan := ownerStorePlan(t, db, "probe")
	var before string
	require.NoError(t, db.QueryRowContext(t.Context(), `SHOW TimeZone`).Scan(&before))
	require.Equal(t, "Africa/Johannesburg", before)
	_, err := store.Import(t.Context(), []int64{plan})
	require.NoError(t, err)
	var after string
	require.NoError(t, db.QueryRowContext(t.Context(), `SHOW TimeZone`).Scan(&after))
	require.Equal(t, before, after, "import must return the pooled session with its original timezone")
	run, err := store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.NoError(t, err)
	require.NoError(t, run.Validate(t.Context()), "canonical fingerprints must not depend on the caller session timezone")
	require.NoError(t, run.Close())
	require.NoError(t, db.QueryRowContext(t.Context(), `SHOW TimeZone`).Scan(&after))
	require.Equal(t, before, after, "validation and unlock must preserve the Core session timezone")
}
