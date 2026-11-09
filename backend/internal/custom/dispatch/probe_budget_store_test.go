package dispatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func budgetCloseFixture() ProbeBudgetCloseRequest {
	return ProbeBudgetCloseRequest{Token: uuid.NameSpaceDNS.String(), ExpectedVersion: 1,
		Actor: "operator-234", Reason: "已确认按原整次请求成本上界关闭", EvidenceRef: "review-234:close"}
}

func TestProbeBudgetCloseRequestBoundary(t *testing.T) {
	request := budgetCloseFixture()
	require.NoError(t, request.Validate())
	for name, change := range map[string]func(*ProbeBudgetCloseRequest){
		"empty token":         func(r *ProbeBudgetCloseRequest) { r.Token = "" },
		"uuid upper":          func(r *ProbeBudgetCloseRequest) { r.Token = strings.ToUpper(r.Token) },
		"uuid short":          func(r *ProbeBudgetCloseRequest) { r.Token = strings.ReplaceAll(r.Token, "-", "") },
		"uuid urn":            func(r *ProbeBudgetCloseRequest) { r.Token = "urn:uuid:" + r.Token },
		"uuid zero":           func(r *ProbeBudgetCloseRequest) { r.Token = "00000000-0000-0000-0000-000000000000" },
		"zero version":        func(r *ProbeBudgetCloseRequest) { r.ExpectedVersion = 0 },
		"negative version":    func(r *ProbeBudgetCloseRequest) { r.ExpectedVersion = -1 },
		"empty actor":         func(r *ProbeBudgetCloseRequest) { r.Actor = "" },
		"large actor":         func(r *ProbeBudgetCloseRequest) { r.Actor = strings.Repeat("a", 129) },
		"actor whitespace":    func(r *ProbeBudgetCloseRequest) { r.Actor = "operator 234" },
		"empty evidence":      func(r *ProbeBudgetCloseRequest) { r.EvidenceRef = "" },
		"URL evidence":        func(r *ProbeBudgetCloseRequest) { r.EvidenceRef = "https://example.invalid/private" },
		"empty reason":        func(r *ProbeBudgetCloseRequest) { r.Reason = "" },
		"blank reason":        func(r *ProbeBudgetCloseRequest) { r.Reason = " \u3000 " },
		"large reason":        func(r *ProbeBudgetCloseRequest) { r.Reason = strings.Repeat("a", 513) },
		"multibyte reason":    func(r *ProbeBudgetCloseRequest) { r.Reason = strings.Repeat("字", 171) },
		"newline":             func(r *ProbeBudgetCloseRequest) { r.Reason = "reason\nnext" },
		"null":                func(r *ProbeBudgetCloseRequest) { r.Reason = "reason\x00" },
		"unicode control":     func(r *ProbeBudgetCloseRequest) { r.Reason = "reason\u0085" },
		"line separator":      func(r *ProbeBudgetCloseRequest) { r.Reason = "reason\u2028" },
		"paragraph separator": func(r *ProbeBudgetCloseRequest) { r.Reason = "reason\u2029" },
		"invalid utf8":        func(r *ProbeBudgetCloseRequest) { r.Reason = string([]byte{0xff}) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			change(&changed)
			require.Error(t, changed.Validate())
		})
	}
	request.Reason = strings.Repeat("a", 512)
	require.NoError(t, request.Validate())
	request.Actor, request.EvidenceRef = strings.Repeat("a", 128), strings.Repeat("e", 128)
	require.NoError(t, request.Validate())
}

func TestProbeBudgetCloseRequestStrictJSON(t *testing.T) {
	request := budgetCloseFixture()
	body, err := json.Marshal(request)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "close.json")
	require.NoError(t, os.WriteFile(path, body, 0600))
	got, err := LoadProbeBudgetCloseRequest(path)
	require.NoError(t, err)
	require.Equal(t, request, got)
	for name, raw := range map[string]string{
		"null":               "null",
		"array":              "[]",
		"unknown":            strings.TrimSuffix(string(body), "}") + `,"secret":"private-fixture"}`,
		"duplicate":          strings.TrimSuffix(string(body), "}") + `,"token":"private-fixture"}`,
		"uppercase":          strings.Replace(string(body), `"actor"`, `"Actor"`, 1),
		"trailing object":    string(body) + `{}`,
		"trailing scalar":    string(body) + `true`,
		"quoted version":     strings.Replace(string(body), `"expected_version":1`, `"expected_version":"1"`, 1),
		"fractional version": strings.Replace(string(body), `"expected_version":1`, `"expected_version":1.0`, 1),
		"missing evidence":   strings.Replace(string(body), `"evidence_ref":"review-234:close"`, `"evidence_ref":null`, 1),
		"invalid utf8 file":  strings.Replace(string(body), "已", string([]byte{0xff}), 1),
		"limit":              strings.Repeat(" ", 16<<10) + string(body),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(path, []byte(raw), 0600))
			_, err := LoadProbeBudgetCloseRequest(path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-fixture")
		})
	}
	_, err = LoadProbeBudgetCloseRequest(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)
}

func TestProbeBudgetDayIsUTC(t *testing.T) {
	require.Equal(t, "2026-11-06", probeBudgetDay(time.Date(2026, 11, 7, 1, 0, 0, 0, time.FixedZone("east", 2*3600))))
	require.Equal(t, "2026-11-08", probeBudgetDay(time.Date(2026, 11, 7, 23, 0, 0, 0, time.FixedZone("west", -2*3600))))
}

func budgetMockTx(t *testing.T) (*sql.Tx, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	mock.ExpectBegin()
	// t.Context() 在 Cleanup 前取消；此事务由下方 Cleanup 显式回滚。
	tx, err := db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { mock.ExpectRollback(); require.NoError(t, tx.Rollback()) })
	return tx, mock
}

func TestProbeBudgetNumericSumOverflowFailsClosed(t *testing.T) {
	for _, value := range []string{"9223372036854775807", "9223372036854775808", "18446744073709551616"} {
		t.Run(value, func(t *testing.T) {
			tx, mock := budgetMockTx(t)
			mock.ExpectQuery("SELECT").WithArgs("2026-11-07", "").WillReturnRows(sqlmock.NewRows([]string{"reserved", "unknown", "charged", "exposure"}).AddRow(value, "0", "0", value))
			totals, err := probeBudgetTotals(t.Context(), tx, "2026-11-07", "")
			if value == "9223372036854775807" {
				require.NoError(t, err)
				require.EqualValues(t, 9223372036854775807, totals.ExposureMicrousd)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestProbeBudgetSchemaCompatibilityFence(t *testing.T) {
	for _, tc := range []struct {
		name                string
		present             int
		migration, recorded bool
		checksum            string
		ready               bool
		fail                bool
	}{
		{name: "legacy003", migration: true},
		{name: "no schema"},
		{name: "removed tables", migration: true, recorded: true, fail: true},
		{name: "partial004", present: 1, fail: true},
		{name: "unrecorded004", present: 5, fail: true},
		{name: "altered004", present: 5, checksum: "tampered", fail: true},
		{name: "ready004", present: 5, checksum: fmt.Sprintf("%x", sha256.Sum256([]byte(probeBudgetSchema))), ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, mock := budgetMockTx(t)
			mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tc.present))
			switch tc.present {
			case 0:
				mock.ExpectQuery("SELECT to_regclass").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(tc.migration))
				if tc.migration {
					mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(tc.recorded))
				}
			case 5:
				rows := sqlmock.NewRows([]string{"checksum"})
				if tc.checksum != "" {
					rows.AddRow(tc.checksum)
				}
				mock.ExpectQuery("SELECT checksum").WillReturnRows(rows)
			}
			ready, err := probeBudgetSchemaReady(context.Background(), tx)
			if tc.fail {
				require.ErrorIs(t, err, ErrProbeBudgetSchema)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.ready, ready)
		})
	}
}

func TestProbeBudgetSchemaAppliedSetMustBePrefix(t *testing.T) {
	for _, versions := range [][]string{{"003"}, {"004"}, {"001", "004"}} {
		t.Run(strings.Join(versions, "_"), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("CREATE SCHEMA").WillReturnResult(sqlmock.NewResult(0, 1))
			bodies := map[string]string{"001": dispatchSchema, "003": failureHoldsSchema, "004": probeBudgetSchema}
			rows := sqlmock.NewRows([]string{"version", "checksum"})
			for _, version := range versions {
				rows.AddRow(version, fmt.Sprintf("%x", sha256.Sum256([]byte(bodies[version]))))
			}
			mock.ExpectQuery("SELECT version, checksum").WillReturnRows(rows)
			mock.ExpectRollback()
			require.ErrorContains(t, ApplySchema(t.Context(), db), "migration order mismatch")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
