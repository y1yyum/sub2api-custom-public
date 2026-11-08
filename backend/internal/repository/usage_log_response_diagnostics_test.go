//go:build unit

package repository

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogResponseDiagnosticsRoundTrip(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "historical_null", true: "recorded"}[present], func(t *testing.T) {
			log := &service.UsageLog{UserID: 1, APIKeyID: 2, AccountID: 3, Model: "claude-example", RequestID: "example", CreatedAt: time.Now().UTC()}
			if present {
				log.ResponseDiagnostics = &service.ResponseDiagnostics{Protocol: "anthropic", StreamComplete: true, MessageID: "msg_example"}
			}
			prepared := prepareUsageLogInsert(log)
			values := append([]driver.Value{int64(7)}, anySliceToDriverValues(prepared.args)...)
			rows := sqlmock.NewRows(strings.Split(usageLogSelectColumns, ", ")).AddRow(values...)
			db, mock := newSQLMock(t)
			mock.ExpectQuery("SELECT").WillReturnRows(rows)
			result, err := db.Query("SELECT usage row")
			require.NoError(t, err)
			defer result.Close()
			require.True(t, result.Next())
			decoded, err := scanUsageLog(result)
			require.NoError(t, err)
			require.Equal(t, log.ResponseDiagnostics, decoded.ResponseDiagnostics)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageLogResponseDiagnosticsCorruptJSONFails(t *testing.T) {
	log := &service.UsageLog{CreatedAt: time.Now().UTC()}
	prepared := prepareUsageLogInsert(log)
	columns := strings.Split(usageLogSelectColumns, ", ")
	values := append([]driver.Value{int64(7)}, anySliceToDriverValues(prepared.args)...)
	for i, name := range columns {
		if name == "response_diagnostics" {
			values[i] = "{invalid"
		}
	}
	rows := sqlmock.NewRows(columns).AddRow(values...)
	db, mock := newSQLMock(t)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	result, err := db.Query("SELECT usage row")
	require.NoError(t, err)
	defer result.Close()
	require.True(t, result.Next())
	_, err = scanUsageLog(result)
	require.ErrorContains(t, err, "decode response diagnostics")
	require.NoError(t, mock.ExpectationsWereMet())
}
