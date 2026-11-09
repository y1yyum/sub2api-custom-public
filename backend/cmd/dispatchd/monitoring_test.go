package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

func TestDispatchMetricsHandlerServesOnlyLimitedScrapes(t *testing.T) {
	metrics := dispatch.NewProcessMetrics()
	handler := &metricsHandler{metrics: metrics}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/", nil),
		httptest.NewRequest(http.MethodGet, "/metrics/extra", nil),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusNotFound, recorder.Code)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	require.Equal(t, http.StatusMethodNotAllowed, recorder.Code)

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "text/plain; version=0.0.4; charset=utf-8", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Body.String(), "dispatch_rounds_total{result=\"success\"} 0\n")

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusTooManyRequests, recorder.Code, "scrapes are rate limited")
	time.Sleep(metricsMinInterval)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/metrics", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Body.String())
}

func TestDispatchMetricsListenerRejectsUnsafeAddressesAndExistingSockets(t *testing.T) {
	metrics := dispatch.NewProcessMetrics()
	for _, listen := range []string{"0.0.0.0:9464", "localhost:9464", ":9464", "unix:relative.sock"} {
		_, err := serveMetrics(listen, metrics)
		require.Error(t, err, listen)
	}
	socket := filepath.Join(t.TempDir(), "metrics.sock")
	require.NoError(t, os.WriteFile(socket, []byte("operator file"), 0o600))
	_, err := serveMetrics("unix:"+socket, metrics)
	require.Error(t, err, "an existing path is never replaced")
	body, err := os.ReadFile(socket)
	require.NoError(t, err)
	require.Equal(t, "operator file", string(body))
}

func TestDispatchMetricsUnixSocketServesPrivateEndpoint(t *testing.T) {
	directory, err := os.MkdirTemp("", "dm")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "m.sock")
	stop, err := serveMetrics("unix:"+socket, dispatch.NewProcessMetrics())
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	info, err := os.Stat(socket)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	client := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}, Timeout: 5 * time.Second}
	response, err := client.Get("http://metrics/metrics")
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Contains(t, string(body), "# TYPE dispatch_rounds_total counter\n")
	stop()
	_, err = os.Stat(socket)
	require.ErrorIs(t, err, os.ErrNotExist, "closing the listener removes the socket it created")
}

func TestDispatchMetricsReportRequiresUnambiguousAction(t *testing.T) {
	t.Setenv("DISPATCH_DSN", "invalid-sensitive-test")
	for _, args := range [][]string{
		{"--metrics-report"},
		{"--metrics-report", "--once", "--config", "missing"},
		{"--metrics-report", "--health-report", "--config", "missing"},
		{"--metrics-report", "--apply-schema"},
	} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, run(context.Background(), args, &stdout, &stderr))
		require.Empty(t, stdout.String())
		require.NotContains(t, stderr.String(), "sensitive-test")
	}
}

func TestDispatchSlowMaintenanceDoesNotDelayRounds(t *testing.T) {
	var calls atomic.Int32
	m := &monitor{metrics: dispatch.NewProcessMetrics(), maintain: func(ctx context.Context, _ io.Writer) error {
		calls.Add(1)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var rounds []time.Time
	started := time.Now()
	code := runRounds(ctx, false, 10*time.Millisecond, m, io.Discard, func() (int, bool) {
		rounds = append(rounds, time.Now())
		return 0, len(rounds) == 5
	})
	require.Zero(t, code)
	require.Len(t, rounds, 5)
	require.Less(t, rounds[4].Sub(started), 500*time.Millisecond, "a slow maintenance pass must not delay the next round")
	require.EqualValues(t, 1, calls.Load(), "triggers during a running pass are coalesced")
}

func TestDispatchOnceMaintenanceFailureExitsOne(t *testing.T) {
	for _, item := range []struct {
		once bool
		err  error
		code int
	}{{true, errors.New("rollup failed"), 1}, {true, nil, 0}, {false, errors.New("rollup failed"), 0}} {
		var calls atomic.Int32
		m := &monitor{metrics: dispatch.NewProcessMetrics(), maintain: func(context.Context, io.Writer) error {
			calls.Add(1)
			return item.err
		}}
		rounds := 0
		code := runRounds(context.Background(), item.once, time.Millisecond, m, io.Discard, func() (int, bool) {
			rounds++
			if !item.once && rounds == 3 {
				return 0, true
			}
			return 0, false
		})
		require.Equal(t, item.code, code, item)
		if item.once {
			require.Equal(t, 1, rounds)
			require.EqualValues(t, 1, calls.Load())
		} else {
			require.Equal(t, 3, rounds, "continuous rounds keep running after maintenance failures")
		}
	}
}

func TestDispatchMetricsListenerLimitsConnectionsAndHeaders(t *testing.T) {
	directory, err := os.MkdirTemp("", "dm")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "m.sock")
	stop, err := serveMetrics("unix:"+socket, dispatch.NewProcessMetrics())
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	defer stop()
	dial := func() net.Conn {
		conn, err := net.Dial("unix", socket)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	held := make([]net.Conn, 0, metricsMaxConns)
	for range metricsMaxConns {
		conn := dial()
		_, err := conn.Write([]byte("GET /metrics HTTP/1.1\r\nHost: metrics\r\n"))
		require.NoError(t, err)
		held = append(held, conn)
	}
	extra := dial()
	_, err = extra.Write([]byte("GET /metrics HTTP/1.1\r\nHost: metrics\r\nConnection: close\r\n\r\n"))
	require.NoError(t, err)
	require.NoError(t, extra.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
	_, err = extra.Read(make([]byte, 1))
	var timeout net.Error
	require.ErrorAs(t, err, &timeout, "a fifth connection waits while four are held")
	require.True(t, timeout.Timeout())
	require.NoError(t, held[0].Close())
	require.NoError(t, extra.SetReadDeadline(time.Now().Add(2*time.Second)))
	response, err := io.ReadAll(extra)
	require.NoError(t, err)
	require.Contains(t, string(response), "HTTP/1.1 200 OK")

	for _, conn := range held[1:] {
		require.NoError(t, conn.Close())
	}
	large := dial()
	_, err = large.Write([]byte("GET /metrics HTTP/1.1\r\nHost: metrics\r\nX-Fill: " + strings.Repeat("a", 16<<10) + "\r\n\r\n"))
	require.NoError(t, err)
	require.NoError(t, large.SetReadDeadline(time.Now().Add(2*time.Second)))
	rejected, err := http.ReadResponse(bufio.NewReader(large), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusRequestHeaderFieldsTooLarge, rejected.StatusCode)
}

func TestDispatchStoppedMaintenanceIsNotReportedAsFailure(t *testing.T) {
	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 user=none dbname=none sslmode=disable connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	newTestMonitor := func() *monitor {
		return &monitor{cfg: dispatch.MonitoringConfig{Aggregation: &dispatch.AggregationConfig{MinuteRetentionDays: 1, HourRetentionDays: 1}},
			core: "core", revision: 1, store: dispatch.NewStore(db), metrics: dispatch.NewProcessMetrics(), lastRetention: time.Now()}
	}
	failure := `dispatch_maintenance_total{task="rollup",result="failure"} 1`

	stopped := newTestMonitor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	require.NoError(t, stopped.afterRound(ctx, &stderr))
	require.Empty(t, stderr.String())
	require.NotContains(t, string(stopped.metrics.Render()), failure)

	failed := newTestMonitor()
	stderr.Reset()
	require.Error(t, failed.afterRound(context.Background(), &stderr))
	require.Contains(t, stderr.String(), "dispatchd 指标聚合失败")
	require.Contains(t, string(failed.metrics.Render()), failure)
}
