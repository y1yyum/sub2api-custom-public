package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

func TestDispatchProbeBudgetCommandRequiresExplicitAction(t *testing.T) {
	t.Setenv("DISPATCH_DSN", "invalid-private-budget-dsn")
	for _, args := range [][]string{
		{"--probe-budget-report"},
		{"--close-probe-budget-file", "missing"},
		{"--probe-budget-report", "--once", "--config", "missing"},
		{"--probe-budget-report", "--health-report", "--config", "missing"},
		{"--probe-budget-report", "--close-probe-budget-file", "missing", "--config", "missing"},
		{"--close-probe-budget-file", "missing", "--apply-schema", "--config", "missing"},
		{"--close-probe-budget-file", "missing", "--observe-file", "missing", "--config", "missing"},
	} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, run(context.Background(), args, &stdout, &stderr))
		require.Empty(t, stdout.String())
		require.NotContains(t, stderr.String(), "private-budget-dsn")
	}
	for _, args := range [][]string{nil, {"--config", "missing"}, {"--probe-budget-report=false", "--config", "missing"}, {"--help"}} {
		var stdout, stderr bytes.Buffer
		require.Zero(t, run(context.Background(), args, &stdout, &stderr))
		require.Empty(t, stderr.String())
		require.Contains(t, stdout.String(), "--probe-budget-report")
		require.Contains(t, stdout.String(), "--close-probe-budget-file")
	}
}

func TestDispatchProbeBudgetCloseRejectsMalformedBeforeStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close.json")
	for _, body := range []string{
		`{`,
		`{"token":"private-invalid-token","expected_version":1,"actor":"ops","reason":"confirmed","evidence_ref":"evidence"}`,
		`{"token":"00000000-0000-4000-8000-000000000001","expected_version":0,"actor":"ops","reason":"confirmed","evidence_ref":"evidence"}`,
		`{"token":"00000000-0000-4000-8000-000000000001","expected_version":1,"actor":"ops","reason":"","evidence_ref":"evidence"}`,
		`{"token":"00000000-0000-4000-8000-000000000001","expected_version":1,"actor":"ops","reason":"confirmed","evidence_ref":"evidence","refund":true}`,
		`{"token":"private-first","token":"private-second","expected_version":1}`,
		`{"Token":"private-case-alias","expected_version":1}`,
		strings.Repeat("x", 64<<10+1),
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0600))
		var stdout, stderr bytes.Buffer
		require.Equal(t, 1, runProbeBudgetAction(t.Context(), nil, dispatch.Config{CoreInstance: "core", ConfigRevision: 1}, false, path, &stdout, &stderr))
		require.Empty(t, stdout.String())
		require.NotContains(t, stderr.String(), "private-")
	}
}

func TestDispatchProbeBudgetActionsRedactDatabaseErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"token":"00000000-0000-4000-8000-000000000001","expected_version":1,"actor":"ops-example","reason":"已核对合成终态","evidence_ref":"evidence-example"}`), 0600))
	for _, report := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectBegin().WillReturnError(errors.New("private-database-diagnostic"))
		mock.ExpectClose()
		var stdout, stderr bytes.Buffer
		require.Equal(t, 1, runProbeBudgetAction(t.Context(), dispatch.NewStore(db), dispatch.Config{CoreInstance: "core", ConfigRevision: 1}, report, path, &stdout, &stderr))
		require.Empty(t, stdout.String())
		require.NotContains(t, stderr.String(), "private-database-diagnostic")
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	}
}
