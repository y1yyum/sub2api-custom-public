package edge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

var subscriptionSecret = bytes.Repeat([]byte{5}, 32)

type subscriptionServer struct {
	sync *dispatch.SnapshotServer
	stop func()
}

func startSubscriptionServer(t *testing.T, path string) subscriptionServer {
	t.Helper()
	server, err := dispatch.NewSnapshotServer("core-fixture", dispatch.SnapshotServerConfig{MaxConcurrent: 8, MaxRequestsPerSecond: 1000},
		map[string][]byte{"edge-a": subscriptionSecret})
	require.NoError(t, err)
	listener, err := dispatch.ListenSnapshotSocket(path, 16)
	require.NoError(t, err)
	httpServer := dispatch.NewSnapshotHTTPServer(server)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = httpServer.Serve(listener)
	}()
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = httpServer.Close()
			<-served
		}
	}
	t.Cleanup(stop)
	return subscriptionServer{server, stop}
}

func subscriptionSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "b2e")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func subscribe(settings *ShadowDispatchConfig, socket string, secret []byte) {
	settings.SnapshotFile = ""
	settings.PollIntervalMilliseconds = 20
	settings.SnapshotSubscription = &SnapshotSubscriptionConfig{
		SocketPath: socket, KeyID: "edge-a", SecretBase64: base64.StdEncoding.EncodeToString(secret), WaitMilliseconds: 200,
	}
}

func publishSubscription(t *testing.T, server subscriptionServer, lineage string, revision int64) dispatch.Snapshot {
	t.Helper()
	snapshot := shadowSnapshot(time.Now())
	snapshot.Revision = revision
	require.NoError(t, server.sync.Publish(dispatch.PublishedSnapshot{Lineage: lineage, Snapshot: snapshot}))
	return snapshot
}

func TestEdgeSnapshotSubscriptionConfig(t *testing.T) {
	cfg := shadowConfig(t, "https://core.test")
	file := cfg.ShadowDispatch.SnapshotFile
	subscribe(cfg.ShadowDispatch, "/run/dispatchd/s.sock", subscriptionSecret)
	cfg.ShadowDispatch.SnapshotSubscription.WaitMilliseconds = 0
	normalized, err := normalizeConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, 10000, normalized.ShadowDispatch.SnapshotSubscription.WaitMilliseconds)

	tests := map[string]func(*ShadowDispatchConfig){
		"both sources":    func(s *ShadowDispatchConfig) { s.SnapshotFile = file },
		"relative socket": func(s *ShadowDispatchConfig) { s.SnapshotSubscription.SocketPath = "s.sock" },
		"long socket": func(s *ShadowDispatchConfig) {
			s.SnapshotSubscription.SocketPath = "/" + string(bytes.Repeat([]byte("a"), 100))
		},
		"short secret": func(s *ShadowDispatchConfig) {
			s.SnapshotSubscription.SecretBase64 = base64.StdEncoding.EncodeToString([]byte("short"))
		},
		"bad key id":       func(s *ShadowDispatchConfig) { s.SnapshotSubscription.KeyID = "edge a" },
		"wait above limit": func(s *ShadowDispatchConfig) { s.SnapshotSubscription.WaitMilliseconds = 25001 },
		"negative wait":    func(s *ShadowDispatchConfig) { s.SnapshotSubscription.WaitMilliseconds = -1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			invalid := shadowConfig(t, "https://core.test")
			subscribe(invalid.ShadowDispatch, "/run/dispatchd/s.sock", subscriptionSecret)
			mutate(invalid.ShadowDispatch)
			_, err := normalizeConfig(invalid)
			require.Error(t, err)
		})
	}

	plain := shadowConfig(t, "https://core.test")
	normalizedPlain, err := normalizeConfig(plain)
	require.NoError(t, err)
	encoded, err := json.Marshal(normalizedPlain.ShadowDispatch)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "snapshot_subscription", "file mode output is unchanged")
	plain.ShadowDispatch.SnapshotFile = ""
	_, err = normalizeConfig(plain)
	require.Error(t, err, "one snapshot source is required")
}

func TestEdgeSnapshotSubscriptionConvergesAndKeepsSnapshotUntilExpiry(t *testing.T) {
	socket := subscriptionSocket(t)
	server := startSubscriptionServer(t, socket)
	initial := publishSubscription(t, server, "lineage-a", 10)
	cfg := shadowConfig(t, "https://core.test")
	subscribe(cfg.ShadowDispatch, socket, subscriptionSecret)
	handler, err := NewHandler(cfg)
	require.NoError(t, err)
	defer handler.Close()
	stats := handler.ShadowStats()
	require.True(t, stats.LastRefreshOK, "the initial fetch is synchronous")
	require.Equal(t, int64(10), stats.SnapshotRevision)
	require.Equal(t, "lineage-a", stats.SnapshotLineage)
	require.Equal(t, initial.ExpiresAt.UnixNano(), stats.SnapshotExpiresAt.UnixNano())

	publishSubscription(t, server, "lineage-a", 11)
	waitShadowStats(t, handler, func(s ShadowStats) bool { return s.SnapshotRevision == 11 })
	publishSubscription(t, server, "lineage-b", 1)
	stats = waitShadowStats(t, handler, func(s ShadowStats) bool { return s.SnapshotLineage == "lineage-b" })
	require.Equal(t, uint64(1), stats.LineageChanges)
	require.Equal(t, int64(1), stats.SnapshotRevision)

	failures := stats.RefreshFailures
	publishSubscription(t, server, "lineage-a", 11)
	stats = waitShadowStats(t, handler, func(s ShadowStats) bool { return s.RefreshFailures > failures })
	require.Equal(t, "lineage-b", stats.SnapshotLineage, "returning to a seen lineage at its watermark is a rollback")
	require.Equal(t, int64(1), stats.SnapshotRevision)

	publishSubscription(t, server, "lineage-a", 12)
	stats = waitShadowStats(t, handler, func(s ShadowStats) bool { return s.SnapshotLineage == "lineage-a" && s.LastRefreshOK })
	require.Equal(t, int64(12), stats.SnapshotRevision)
	require.Equal(t, uint64(2), stats.LineageChanges)
	// 同 epoch 的长轮询以签名 204 结束，并计为一次成功刷新。
	accepted := waitShadowStats(t, handler, func(s ShadowStats) bool { return s.RefreshSuccesses > stats.RefreshSuccesses })
	require.Equal(t, int64(12), accepted.SnapshotRevision)

	server.stop()
	stats = waitShadowStats(t, handler, func(s ShadowStats) bool { return !s.LastRefreshOK && s.RefreshFailures > accepted.RefreshFailures })
	require.Equal(t, int64(12), stats.SnapshotRevision, "a disconnect keeps the last accepted snapshot")
	grant := cfg.Grants[0]
	handler.shadow.observe(grant, "fixture-model", time.Now())
	require.Zero(t, handler.ShadowStats().Unavailable)
	handler.shadow.observe(grant, "fixture-model", stats.SnapshotExpiresAt)
	stats = handler.ShadowStats()
	require.Equal(t, uint64(1), stats.SnapshotExpired, "after its TTL the kept snapshot is rejected as before")

	restarted := startSubscriptionServer(t, socket)
	publishSubscription(t, restarted, "lineage-a", 13)
	waitShadowStats(t, handler, func(s ShadowStats) bool { return s.SnapshotRevision == 13 && s.LastRefreshOK })
}

func TestEdgeSnapshotSubscriptionAuthFailureFailsClosed(t *testing.T) {
	socket := subscriptionSocket(t)
	server := startSubscriptionServer(t, socket)
	publishSubscription(t, server, "lineage-a", 10)
	cfg := shadowConfig(t, "https://core.test")
	subscribe(cfg.ShadowDispatch, socket, bytes.Repeat([]byte{6}, 32))
	handler, err := NewHandler(cfg)
	require.NoError(t, err)
	defer handler.Close()
	stats := handler.ShadowStats()
	require.False(t, stats.LastRefreshOK)
	require.NotZero(t, stats.RefreshFailures)
	require.Zero(t, stats.SnapshotRevision)
	require.Empty(t, stats.SnapshotLineage)
	handler.shadow.observe(cfg.Grants[0], "fixture-model", time.Now())
	require.Equal(t, uint64(1), handler.ShadowStats().SnapshotUnavailable)
}

func TestEdgeStrictSnapshotSubscriptionReportsLineage(t *testing.T) {
	socket := subscriptionSocket(t)
	server := startSubscriptionServer(t, socket)
	publishSubscription(t, server, "lineage-a", 10)
	cfg := strictConfig(t, "https://core.test")
	subscribe(cfg.StrictDispatch, socket, subscriptionSecret)
	handler, err := NewHandler(cfg)
	require.NoError(t, err)
	defer handler.Close()
	stats := handler.StrictStats()
	require.Equal(t, "lineage-a", stats.SnapshotLineage)
	require.Equal(t, int64(10), stats.SnapshotRevision)
	encoded, err := json.Marshal(stats)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "lineage_changes", "zero counters stay omitted")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func TestEdgeSnapshotSubscriptionBacksOffOnRejectedSnapshot(t *testing.T) {
	const serverLimit = 10
	server, err := dispatch.NewSnapshotServer("core-fixture", dispatch.SnapshotServerConfig{MaxConcurrent: 8, MaxRequestsPerSecond: serverLimit},
		map[string][]byte{"edge-a": subscriptionSecret})
	require.NoError(t, err)
	var requests, limited atomic.Int64
	counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		recorder := &statusRecorder{ResponseWriter: w}
		server.ServeHTTP(recorder, r)
		if recorder.status == http.StatusTooManyRequests {
			limited.Add(1)
		}
	})
	socket := subscriptionSocket(t)
	listener, err := dispatch.ListenSnapshotSocket(socket, 16)
	require.NoError(t, err)
	httpServer := dispatch.NewSnapshotHTTPServer(counting)
	go func() { _ = httpServer.Serve(listener) }()
	t.Cleanup(func() { _ = httpServer.Close() })
	fixture := subscriptionServer{sync: server}
	publishSubscription(t, fixture, "lineage-a", 10)

	cfg := shadowConfig(t, "https://core.test")
	subscribe(cfg.ShadowDispatch, socket, subscriptionSecret)
	handler, err := NewHandler(cfg)
	require.NoError(t, err)
	defer handler.Close()
	require.Equal(t, int64(10), handler.ShadowStats().SnapshotRevision)

	publishSubscription(t, fixture, "lineage-a", 9)
	waitShadowStats(t, handler, func(s ShadowStats) bool { return s.RefreshFailures > 0 })
	window := 1500 * time.Millisecond
	before := requests.Load()
	time.Sleep(window)
	sent := requests.Load() - before
	require.LessOrEqual(t, sent, int64(serverLimit*window/time.Second), "a persistently rejected snapshot is retried with backoff")
	require.Zero(t, limited.Load(), "the subscriber never hits the server rate limit")
	require.Equal(t, int64(10), handler.ShadowStats().SnapshotRevision, "the rollback stays rejected")
}

func TestEdgeSubscriptionBackoffBounds(t *testing.T) {
	base := 20 * time.Millisecond
	for failures := 1; failures <= 40; failures++ {
		ceiling := maxSubscriptionBackoff
		if failures <= 16 && base<<(failures-1) < maxSubscriptionBackoff {
			ceiling = base << (failures - 1)
		}
		for range 50 {
			delay := subscriptionBackoff(base, failures)
			require.GreaterOrEqual(t, delay, ceiling/2)
			require.LessOrEqual(t, delay, ceiling)
		}
	}
	require.LessOrEqual(t, subscriptionBackoff(30*time.Second, 1), maxSubscriptionBackoff)
}
