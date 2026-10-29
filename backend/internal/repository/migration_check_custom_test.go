//go:build unit

package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
)

func TestCustomMigrationLedgerReadOnlyContracts(t *testing.T) {
	const a = "247_payment_audit_refund_retry.sql"
	const b = "247_scoped_admin_api_keys.sql"
	files := fstest.MapFS{
		a:               {Data: []byte(" \nSELECT 1;\n ")},
		b:               {Data: []byte("SELECT 2;")},
		"249_empty.sql": {Data: []byte(" \n\t")},
	}
	hash := func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
	for _, tc := range []struct {
		name     string
		rows     *sqlmock.Rows
		queryErr error
		want     string
	}{
		{name: "trimmed_full_filenames_and_extra_history", rows: sqlmock.NewRows([]string{"filename", "checksum"}).AddRow(a, hash("SELECT 1;")).AddRow(b, hash("SELECT 2;")).AddRow("old.sql", "historical")},
		{name: "same_numeric_prefix_does_not_hide_pending", rows: sqlmock.NewRows([]string{"filename", "checksum"}).AddRow(a, hash("SELECT 1;")), want: b},
		{name: "checksum_drift", rows: sqlmock.NewRows([]string{"filename", "checksum"}).AddRow(a, "wrong"), want: "checksum mismatch"},
		{name: "missing_ledger", queryErr: fmt.Errorf("relation does not exist"), want: "read migration ledger"},
		{name: "duplicate_filename", rows: sqlmock.NewRows([]string{"filename", "checksum"}).AddRow(a, hash("SELECT 1;")).AddRow(a, hash("SELECT 1;")), want: "duplicate migration ledger"},
		{name: "null_checksum", rows: sqlmock.NewRows([]string{"filename", "checksum"}).AddRow(a, nil), want: "scan migration ledger"},
		{name: "row_error", rows: sqlmock.NewRows([]string{"filename", "checksum"}).AddRow(a, hash("SELECT 1;")).RowError(0, fmt.Errorf("synthetic row failure")), want: "read migration ledger rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			query := mock.ExpectQuery("SELECT filename, checksum FROM schema_migrations")
			if tc.queryErr != nil {
				query.WillReturnError(tc.queryErr)
			} else {
				query.WillReturnRows(tc.rows).RowsWillBeClosed()
			}
			err = checkMigrationsFS(context.Background(), db, files)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error=%v want substring=%q", err, tc.want)
			}
			// Only one SELECT was expected: no DDL, repair, lock or write path.
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := CheckMigrations(context.Background(), nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

func TestCustomMigrationLedgerHistoricalCompatibility(t *testing.T) {
	const name = "054_drop_legacy_cache_columns.sql"
	raw, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT filename, checksum FROM schema_migrations").WillReturnRows(sqlmock.NewRows([]string{"filename", "checksum"}).AddRow(name, "182c193f3359946cf094090cd9e57d5c3fd9abaffbc1e8fc378646b8a6fa12b4")).RowsWillBeClosed()
	if err := checkMigrationsFS(context.Background(), db, fstest.MapFS{name: {Data: raw}}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
