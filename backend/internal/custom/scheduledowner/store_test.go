//go:build unit

package scheduledowner

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUncertainSessionLockIsDiscarded(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(planLock(7)).WillReturnError(errors.New("synthetic uncertain lock acknowledgement"))
	mock.ExpectClose()
	_, err = NewStore(db).lock(context.Background(), 7)
	require.ErrorContains(t, err, "uncertain lock acknowledgement")
	require.Zero(t, db.Stats().Idle, "an uncertain session lock must not return to the connection pool")
	require.NoError(t, mock.ExpectationsWereMet())
}
