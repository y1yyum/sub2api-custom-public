package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

func TestDispatchRotateLineageRequiresUnambiguousAction(t *testing.T) {
	t.Setenv("DISPATCH_DSN", "invalid-sensitive-test")
	for _, args := range [][]string{
		{"--rotate-publish-lineage"},
		{"--rotate-publish-lineage", "--once", "--config", "missing"},
		{"--rotate-publish-lineage", "--apply-schema", "--config", "missing"},
	} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, run(context.Background(), args, &stdout, &stderr))
		require.Empty(t, stdout.String())
		require.NotContains(t, stderr.String(), "sensitive-test")
	}
}

func TestDispatchSnapshotServerLifecycle(t *testing.T) {
	dir, err := os.MkdirTemp("", "b2d")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	secret := bytes.Repeat([]byte{3}, 32)
	keys := filepath.Join(dir, "keys.json")
	require.NoError(t, os.WriteFile(keys, []byte(`{"version":1,"keys":[{"id":"edge-a","secret_base64":"`+base64.StdEncoding.EncodeToString(secret)+`"}]}`), 0o600))
	socket := filepath.Join(dir, "s.sock")
	cfg := dispatch.Config{CoreInstance: "core", SnapshotServer: &dispatch.SnapshotServerConfig{SocketPath: socket, KeysFile: filepath.Join(dir, "missing.json"), MaxConcurrent: 2, MaxRequestsPerSecond: 10}}
	_, _, err = startSnapshotServer(t.Context(), cfg, nil, io.Discard)
	require.Error(t, err, "a missing key file blocks startup")

	cfg.SnapshotServer.KeysFile = keys
	server, stop, err := startSnapshotServer(t.Context(), cfg, nil, io.Discard)
	require.NoError(t, err)
	now := time.Now()
	snapshot := dispatch.Snapshot{Version: dispatch.SnapshotVersion, CoreInstance: "core", Revision: 4, GeneratedAt: now, ExpiresAt: now.Add(20 * time.Second)}
	require.NoError(t, server.Publish(dispatch.PublishedSnapshot{Lineage: "lineage-a", Snapshot: snapshot}))
	client := dispatch.NewSnapshotClient(socket, "edge-a", secret, "core")
	defer client.Close()
	fetched, err := client.Fetch(t.Context(), "", 0, 0)
	require.NoError(t, err)
	require.Equal(t, "lineage-a", fetched.Envelope.PublishLineage)
	require.Equal(t, int64(4), fetched.Envelope.Snapshot.Revision)
	invalidations := dispatch.NewInvalidationClient(socket, "edge-a", secret, "core")
	defer invalidations.Close()
	_, err = invalidations.Fetch(t.Context(), "", 0, 0)
	require.ErrorContains(t, err, "status 404")
	stop()
	_, err = os.Lstat(socket)
	require.ErrorIs(t, err, os.ErrNotExist, "shutdown removes the socket")
}

func TestDispatchHardInvalidationServerLifecycle(t *testing.T) {
	dir, err := os.MkdirTemp("", "b3d")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	keys, socket := filepath.Join(dir, "keys.json"), filepath.Join(dir, "s.sock")
	require.NoError(t, os.WriteFile(keys, []byte(`{"version":1,"keys":[{"id":"edge-a","secret_base64":"`+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))+`"}]}`), 0o600))
	cfg := dispatch.Config{CoreInstance: "core", SnapshotServer: &dispatch.SnapshotServerConfig{
		SocketPath: socket, KeysFile: keys, MaxConcurrent: 1, MaxRequestsPerSecond: 10,
		HardInvalidation: &dispatch.HardInvalidationConfig{PollIntervalMilliseconds: 100},
	}}
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, coreExists := range []bool{false, false, true, false, false} {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT to_regclass").WillReturnRows(sqlmock.NewRows([]string{"ready", "system", "oid"}).AddRow(true, 1, 2))
		rows := sqlmock.NewRows([]string{"revision", "lineage", "system", "oid"})
		if coreExists {
			rows.AddRow(0, nil, 0, 0)
		}
		mock.ExpectQuery("SELECT c.revision").WithArgs("core").WillReturnRows(rows)
		mock.ExpectRollback()
	}
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, stop, err := startSnapshotServer(ctx, cfg, dispatch.NewStore(db), &stderr)
	require.NoError(t, err)
	stop = sync.OnceFunc(stop)
	t.Cleanup(stop)

	// 前两个连接不发请求头，第三个仍能访问失效路径；三个连接占满后，第四个须等到有连接释放。
	dial := func() net.Conn {
		t.Helper()
		conn, err := net.DialTimeout("unix", socket, time.Second)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	request := func(conn net.Conn) {
		t.Helper()
		_, err := io.WriteString(conn, "GET "+dispatch.InvalidationSyncPath+" HTTP/1.1\r\nHost: dispatchd\r\n\r\n")
		require.NoError(t, err)
	}
	readResponse := func(conn net.Conn) {
		t.Helper()
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, "mounted invalidation route rejects malformed queries")
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	first, second, third := dial(), dial(), dial()
	queued := dial()
	request(queued)
	require.NoError(t, queued.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	_, err = queued.Read(make([]byte, 1))
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
	request(third)
	readResponse(third)
	require.NoError(t, third.Close())
	readResponse(queued)
	require.NoError(t, first.Close())
	require.NoError(t, second.Close())

	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, 2*time.Second, 10*time.Millisecond)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	before := db.Stats()
	require.Eventually(t, func() bool { return db.Stats().WaitCount > before.WaitCount }, time.Second, time.Millisecond)
	stop()
	require.NoError(t, ctx.Err(), "stop does not need the parent to cancel")
	require.Greater(t, db.Stats().WaitDuration, before.WaitDuration, "stop cancels and joins the blocked poll")
	waits := db.Stats().WaitCount
	require.Never(t, func() bool { return db.Stats().WaitCount != waits }, 250*time.Millisecond, 10*time.Millisecond)
	require.Equal(t, strings.Repeat("dispatchd 硬失效轮询失败：找不到配置的 core 行；失效改由下一份快照传播\n", 2), stderr.String(),
		"a core awaiting its first lineage ends the failure streak without logging")
	_, err = os.Lstat(socket)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDispatchHardInvalidationWatcherSharesRoundStderr(t *testing.T) {
	dir, err := os.MkdirTemp("", "b3w")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	keys := filepath.Join(dir, "keys.json")
	require.NoError(t, os.WriteFile(keys, []byte(`{"version":1,"keys":[{"id":"edge-a","secret_base64":"`+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))+`"}]}`), 0o600))
	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 user=none dbname=none sslmode=disable connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := dispatch.Config{CoreInstance: "core", SnapshotServer: &dispatch.SnapshotServerConfig{
		SocketPath: filepath.Join(dir, "s.sock"), KeysFile: keys, MaxConcurrent: 2, MaxRequestsPerSecond: 10,
		HardInvalidation: &dispatch.HardInvalidationConfig{PollIntervalMilliseconds: 100},
	}}

	var raw bytes.Buffer
	stderr := continuousStderr(cfg, nil, &raw)
	_, stop, err := startSnapshotServer(t.Context(), cfg, dispatch.NewStore(db), stderr)
	require.NoError(t, err)
	started := time.Now()
	runRounds(t.Context(), false, time.Millisecond, nil, stderr, func() (int, bool) {
		_, _ = io.WriteString(stderr, "round\n")
		return 0, time.Since(started) > 1500*time.Millisecond
	})
	stop()
	require.Contains(t, raw.String(), "dispatchd 硬失效轮询失败", "the watcher reports through the same writer as rounds")
	require.Equal(t, 1, strings.Count(raw.String(), "硬失效轮询失败"), "a failure streak is reported once")
}
