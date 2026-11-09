package custombridge

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestBridgeReceiptSchemaExplicitTransaction(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			migration := mock.ExpectExec("CREATE SCHEMA IF NOT EXISTS bridge")
			failure := errors.New("migration failure")
			if fail {
				migration.WillReturnError(failure)
				mock.ExpectRollback()
			} else {
				migration.WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectCommit()
			}
			err = ApplyReceiptSchema(context.Background(), db)
			if fail {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
