package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestScheduledOwnerCommandDefaultOff(t *testing.T) {
	t.Setenv("SCHEDULED_OWNER_DSN", "invalid-sensitive-source")
	t.Setenv("SCHEDULED_OWNER_DSN_FILE", "missing-file")
	for _, args := range [][]string{nil, {"--dsn-file", "missing-file"}, {"--help"}} {
		var stdout, stderr bytes.Buffer
		require.Zero(t, run(t.Context(), args, &stdout, &stderr))
		require.NotEmpty(t, stdout.String())
		require.Empty(t, stderr.String())
		require.NotContains(t, stdout.String(), "sensitive-source")
	}
}

func TestScheduledOwnerCommandRequiresExactTransition(t *testing.T) {
	id := uuid.NewString()
	for _, args := range [][]string{
		{"--import"}, {"--import", "--plans", "1"},
		{"--import", "--plans", "1,1", "--database-id", id},
		{"--import", "--plans", "01", "--database-id", id},
		{"--import", "--plans", "0", "--database-id", id},
		{"--import", "--plans", "-1", "--database-id", id},
		{"--import", "--plans", "9223372036854775808", "--database-id", id},
		{"--import", "--plans", "1,", "--database-id", id},
		{"--freeze", "--plan", "1", "--database-id", id},
		{"--freeze", "--plan", "1", "--epoch", "1", "--database-id", "other-source"},
		{"--activate", "--plan", "1", "--epoch", "1", "--database-id", id},
		{"--activate", "--plan", "1", "--epoch", "1", "--owner", "other", "--database-id", id},
		{"--ack-unknown", "--plan", "1", "--epoch", "1", "--database-id", id},
		{"--ack-unknown", "--plan", "1", "--epoch", "1", "--database-id", id, "--reason", strings.Repeat("x", 513)},
		{"--ack-unknown", "--plan", "1", "--epoch", "1", "--database-id", id, "--reason", "two\nlines"},
		{"--review-edit", "--plan", "1", "--epoch", "1", "--database-id", id},
		{"--review-edit", "--plan", "1", "--epoch", "0", "--database-id", id, "--reason", "reviewed"},
		{"--freeze", "--plan", "1", "--epoch", "1", "--plans", "1", "--database-id", id},
		{"--status", "--plan", "1"}, {"--status", "--database-id", id},
		{"--status", "--reason", "other"}, {"--apply-schema", "--plans", "1"},
		{"--apply-schema", "--status"}, {"--status", "unexpected"}, {"--unknown"},
	} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, run(t.Context(), args, &stdout, &stderr), "%v", args)
		require.Empty(t, stdout.String())
		require.NotEmpty(t, stderr.String())
	}
	ids, err := parsePlanIDs("3,1,2")
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, ids)
	_, err = parsePlanIDs(strings.Repeat("1,", 1000) + "2")
	require.Error(t, err)
}

func TestScheduledOwnerCommandRedactsDatabaseSource(t *testing.T) {
	t.Setenv("SCHEDULED_OWNER_DSN", "invalid-sensitive-source")
	t.Setenv("SCHEDULED_OWNER_DSN_FILE", "")
	var stdout, stderr bytes.Buffer
	require.Equal(t, 1, run(t.Context(), []string{"--status"}, &stdout, &stderr))
	require.Empty(t, stdout.String())
	require.NotContains(t, stderr.String(), "sensitive-source")
	path := filepath.Join(t.TempDir(), "dsn")
	require.NoError(t, os.WriteFile(path, []byte("invalid-sensitive-source"), 0600))
	t.Setenv("SCHEDULED_OWNER_DSN_FILE", path)
	_, err := openDB(context.Background(), "")
	require.Error(t, err, "multiple DSN inputs must fail before opening a database")
	t.Setenv("SCHEDULED_OWNER_DSN", "")
	_, err = openDB(context.Background(), filepath.Dir(path))
	require.Error(t, err)
	t.Setenv("SCHEDULED_OWNER_DSN_FILE", "")
	_, err = openDB(context.Background(), filepath.Dir(path))
	require.Error(t, err, "directories are not runtime secret files")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 16<<10+1)), 0600))
	_, err = openDB(context.Background(), path)
	require.Error(t, err)
}
