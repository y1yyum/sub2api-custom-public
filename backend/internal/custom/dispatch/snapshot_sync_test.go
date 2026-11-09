package dispatch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var syncSecret = bytes.Repeat([]byte{7}, 32)

func syncSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "b2s")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func serveSyncHandler(t *testing.T, handler http.Handler) string {
	t.Helper()
	path := syncSocketPath(t)
	listener, err := ListenSnapshotSocket(path, 16)
	require.NoError(t, err)
	server := NewSnapshotHTTPServer(handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return path
}

func startSyncServer(t *testing.T, cfg SnapshotServerConfig) (*SnapshotServer, string) {
	t.Helper()
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 8
	}
	if cfg.MaxRequestsPerSecond == 0 {
		cfg.MaxRequestsPerSecond = 1000
	}
	server, err := NewSnapshotServer("core-fixture", cfg, map[string][]byte{"edge-a": syncSecret})
	require.NoError(t, err)
	return server, serveSyncHandler(t, server)
}

func syncClient(t *testing.T, path, keyID string, secret []byte, core string) *SnapshotClient {
	t.Helper()
	client := NewSnapshotClient(path, keyID, secret, core)
	t.Cleanup(client.Close)
	return client
}

func publishedFixture(lineage string, revision int64) PublishedSnapshot {
	snapshot := testSnapshot(time.Now())
	snapshot.Revision = revision
	return PublishedSnapshot{Lineage: lineage, Snapshot: snapshot}
}

func signedSyncRequest(t *testing.T, keyID string, secret []byte, timestamp time.Time, query snapshotQuery) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, SnapshotSyncPath+"?"+query.values().Encode(), nil)
	ts := strconv.FormatInt(timestamp.Unix(), 10)
	nonce := strings.Repeat("ab", 16)
	request.Header.Set(headerSnapshotKey, keyID)
	request.Header.Set(headerSnapshotTime, ts)
	request.Header.Set(headerSnapshotNonce, nonce)
	request.Header.Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(snapshotRequestMAC(secret, keyID, ts, nonce, query)))
	return request
}

func TestDispatchSnapshotSyncLongPollOverUDS(t *testing.T) {
	server, path := startSyncServer(t, SnapshotServerConfig{})
	client := syncClient(t, path, "edge-a", syncSecret, "core-fixture")
	ctx := context.Background()

	_, err := client.Fetch(ctx, "", 0, 0)
	require.ErrorContains(t, err, "status 503", "no published snapshot yet")

	require.NoError(t, server.Publish(publishedFixture("lineage-a", 1)))
	fetched, err := client.Fetch(ctx, "", 0, 0)
	require.NoError(t, err)
	require.False(t, fetched.NotModified)
	require.Equal(t, "lineage-a", fetched.Envelope.PublishLineage)
	require.Equal(t, int64(1), fetched.Envelope.Snapshot.Revision)

	started := time.Now()
	fetched, err = client.Fetch(ctx, "lineage-a", 1, 50*time.Millisecond)
	require.NoError(t, err)
	require.True(t, fetched.NotModified, "same epoch waits and returns a signed 204")
	require.GreaterOrEqual(t, time.Since(started), 50*time.Millisecond)

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = server.Publish(publishedFixture("lineage-a", 2))
	}()
	started = time.Now()
	fetched, err = client.Fetch(ctx, "lineage-a", 1, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, int64(2), fetched.Envelope.Snapshot.Revision)
	require.Less(t, time.Since(started), 4*time.Second, "publish wakes the waiting request")

	require.NoError(t, server.Publish(publishedFixture("lineage-b", 1)))
	fetched, err = client.Fetch(ctx, "lineage-a", 2, time.Second)
	require.NoError(t, err)
	require.Equal(t, "lineage-b", fetched.Envelope.PublishLineage, "a lineage change is served immediately")

	_, err = syncClient(t, path, "edge-a", syncSecret, "core-other").Fetch(ctx, "", 0, 0)
	require.ErrorContains(t, err, "status 404")
	_, err = client.Fetch(ctx, "lineage-a", 0, 0)
	require.ErrorContains(t, err, "position")
	_, err = client.Fetch(ctx, "", 0, MaxSnapshotSyncWait+time.Millisecond)
	require.ErrorContains(t, err, "position")
}

func TestDispatchSnapshotSyncLongPollOutlivesReadTimeout(t *testing.T) {
	server, path := startSyncServer(t, SnapshotServerConfig{})
	require.NoError(t, server.Publish(publishedFixture("lineage-a", 1)))
	started := time.Now()
	fetched, err := syncClient(t, path, "edge-a", syncSecret, "core-fixture").Fetch(context.Background(), "lineage-a", 1, 7*time.Second)
	require.NoError(t, err)
	require.True(t, fetched.NotModified, "a wait longer than five seconds still ends in a signed 204")
	require.GreaterOrEqual(t, time.Since(started), 7*time.Second)
}

func TestDispatchSnapshotSyncUnsignedFloodDoesNotStarveSigned(t *testing.T) {
	limited, err := NewSnapshotServer("core-fixture", SnapshotServerConfig{MaxConcurrent: 1, MaxRequestsPerSecond: 1}, map[string][]byte{"edge-a": syncSecret})
	require.NoError(t, err)
	fixed := time.Now()
	limited.now = func() time.Time { return fixed }
	require.NoError(t, limited.Publish(publishedFixture("lineage-a", 1)))
	query := snapshotQuery{core: "core-fixture"}
	for range 20 {
		recorder := httptest.NewRecorder()
		limited.ServeHTTP(recorder, signedSyncRequest(t, "edge-a", bytes.Repeat([]byte{8}, 32), fixed, query))
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Equal(t, "close", recorder.Header().Get("Connection"), "rejected requests do not keep the connection")
	}
	recorder := httptest.NewRecorder()
	limited.ServeHTTP(recorder, signedSyncRequest(t, "edge-a", syncSecret, fixed, query))
	require.Equal(t, http.StatusOK, recorder.Code, "unsigned requests consume neither rate nor slots")

	server, path := startSyncServer(t, SnapshotServerConfig{MaxConcurrent: 1, MaxRequestsPerSecond: 3})
	require.NoError(t, server.Publish(publishedFixture("lineage-a", 1)))
	forged := syncClient(t, path, "edge-a", bytes.Repeat([]byte{8}, 32), "core-fixture")
	var flood sync.WaitGroup
	for range 50 {
		flood.Go(func() {
			_, err := forged.Fetch(context.Background(), "", 0, 0)
			assert.ErrorContains(t, err, "status 401")
		})
	}
	flood.Wait()
	fetched, err := syncClient(t, path, "edge-a", syncSecret, "core-fixture").Fetch(context.Background(), "", 0, 0)
	require.NoError(t, err, "a signed request after the flood is neither 429 nor 503")
	require.Equal(t, int64(1), fetched.Envelope.Snapshot.Revision)
}

func TestDispatchSnapshotSocketConnectionLimit(t *testing.T) {
	path := syncSocketPath(t)
	listener, err := ListenSnapshotSocket(path, 1)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	_, err = ListenSnapshotSocket(syncSocketPath(t), 0)
	require.ErrorContains(t, err, "invalid")

	first, err := net.Dial("unix", path)
	require.NoError(t, err)
	accepted, err := listener.Accept()
	require.NoError(t, err)
	second, err := net.Dial("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	next := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			next <- conn
		}
	}()
	select {
	case <-next:
		t.Fatal("a second connection is accepted beyond the limit")
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, accepted.Close())
	require.NoError(t, first.Close())
	select {
	case conn := <-next:
		require.NoError(t, conn.Close())
	case <-time.After(2 * time.Second):
		t.Fatal("releasing a connection frees the limit")
	}
}

func TestDispatchSnapshotSocketKeepsSocketOnAmbiguousDial(t *testing.T) {
	path := syncSocketPath(t)
	stale, err := net.Listen("unix", path)
	require.NoError(t, err)
	unixListener, ok := stale.(*net.UnixListener)
	require.True(t, ok)
	unixListener.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())
	original := dialSnapshotSocket
	t.Cleanup(func() { dialSnapshotSocket = original })
	for name, dialErr := range map[string]error{
		"timeout": &net.OpError{Op: "dial", Net: "unix", Err: os.ErrDeadlineExceeded},
		"eagain":  &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", syscall.EAGAIN)},
	} {
		t.Run(name, func(t *testing.T) {
			dialSnapshotSocket = func(string) (net.Conn, error) { return nil, dialErr }
			_, err := ListenSnapshotSocket(path, 16)
			require.ErrorContains(t, err, "may still be in use")
			info, err := os.Lstat(path)
			require.NoError(t, err, "an instance that may be alive keeps its socket")
			require.Equal(t, os.ModeSocket, info.Mode().Type())
		})
	}
	dialSnapshotSocket = original
	replaced, err := ListenSnapshotSocket(path, 16)
	require.NoError(t, err, "ECONNREFUSED alone proves the socket stale")
	require.NoError(t, replaced.Close())
}

func TestDispatchSnapshotSyncRejectsUnauthenticatedRequests(t *testing.T) {
	server, path := startSyncServer(t, SnapshotServerConfig{})
	require.NoError(t, server.Publish(publishedFixture("lineage-a", 1)))
	ctx := context.Background()
	_, err := syncClient(t, path, "edge-a", bytes.Repeat([]byte{8}, 32), "core-fixture").Fetch(ctx, "", 0, 0)
	require.ErrorContains(t, err, "status 401")
	_, err = syncClient(t, path, "edge-unknown", syncSecret, "core-fixture").Fetch(ctx, "", 0, 0)
	require.ErrorContains(t, err, "status 401")

	query := snapshotQuery{core: "core-fixture"}
	tests := map[string]func(*http.Request){
		"stale timestamp": func(r *http.Request) {
			*r = *signedSyncRequest(t, "edge-a", syncSecret, time.Now().Add(-time.Minute), query)
		},
		"future timestamp": func(r *http.Request) {
			*r = *signedSyncRequest(t, "edge-a", syncSecret, time.Now().Add(time.Minute), query)
		},
		"tampered signature": func(r *http.Request) {
			r.Header.Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
		},
		"signature over another position": func(r *http.Request) {
			r.URL.RawQuery = snapshotQuery{core: "core-fixture", wait: time.Millisecond}.values().Encode()
		},
		"uppercase nonce": func(r *http.Request) {
			r.Header.Set(headerSnapshotNonce, strings.Repeat("AB", 16))
		},
		"missing signature": func(r *http.Request) { r.Header.Del(headerSnapshotSignature) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := signedSyncRequest(t, "edge-a", syncSecret, time.Now(), query)
			mutate(request)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.Empty(t, recorder.Body.Bytes())
			require.Empty(t, recorder.Header().Get(headerSnapshotSignature))
		})
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, signedSyncRequest(t, "edge-a", syncSecret, time.Now(), query))
	require.Equal(t, http.StatusOK, recorder.Code, "the unmodified request is accepted")
}

func TestDispatchSnapshotSyncRequestShapeLimits(t *testing.T) {
	server, _ := startSyncServer(t, SnapshotServerConfig{})
	require.NoError(t, server.Publish(publishedFixture("lineage-a", 1)))
	valid := func() *http.Request {
		return signedSyncRequest(t, "edge-a", syncSecret, time.Now(), snapshotQuery{core: "core-fixture"})
	}
	tests := map[string]struct {
		mutate func(*http.Request)
		status int
	}{
		"path":            {func(r *http.Request) { r.URL.Path = "/v1/dispatch/other" }, http.StatusNotFound},
		"method":          {func(r *http.Request) { r.Method = http.MethodPost }, http.StatusMethodNotAllowed},
		"extra parameter": {func(r *http.Request) { r.URL.RawQuery += "&extra=1" }, http.StatusBadRequest},
		"repeated parameter": {func(r *http.Request) {
			r.URL.RawQuery = strings.Replace(r.URL.RawQuery, "wait_ms=0", "wait_ms=0&wait_ms=0", 1)
		}, http.StatusBadRequest},
		"non canonical revision": {func(r *http.Request) {
			r.URL.RawQuery = strings.Replace(r.URL.RawQuery, "after_revision=0", "after_revision=00", 1)
		}, http.StatusBadRequest},
		"revision without lineage": {func(r *http.Request) {
			r.URL.RawQuery = strings.Replace(r.URL.RawQuery, "after_revision=0", "after_revision=3", 1)
		}, http.StatusBadRequest},
		"wait above limit": {func(r *http.Request) {
			r.URL.RawQuery = strings.Replace(r.URL.RawQuery, "wait_ms=0", "wait_ms=25001", 1)
		}, http.StatusBadRequest},
		"body": {func(r *http.Request) { r.ContentLength = 1 }, http.StatusBadRequest},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			request := valid()
			tc.mutate(request)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		})
	}
}

func TestDispatchSnapshotSyncRateConcurrencyAndExpiry(t *testing.T) {
	limited, err := NewSnapshotServer("core-fixture", SnapshotServerConfig{MaxConcurrent: 8, MaxRequestsPerSecond: 1}, map[string][]byte{"edge-a": syncSecret})
	require.NoError(t, err)
	fixed := time.Now()
	limited.now = func() time.Time { return fixed }
	require.NoError(t, limited.Publish(publishedFixture("lineage-a", 1)))
	query := snapshotQuery{core: "core-fixture"}
	first := httptest.NewRecorder()
	limited.ServeHTTP(first, signedSyncRequest(t, "edge-a", syncSecret, fixed, query))
	require.Equal(t, http.StatusOK, first.Code)
	second := httptest.NewRecorder()
	limited.ServeHTTP(second, signedSyncRequest(t, "edge-a", syncSecret, fixed, query))
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	fixed = fixed.Add(time.Second)
	third := httptest.NewRecorder()
	limited.ServeHTTP(third, signedSyncRequest(t, "edge-a", syncSecret, fixed, query))
	require.Equal(t, http.StatusOK, third.Code, "the rate window resets each second")

	server, path := startSyncServer(t, SnapshotServerConfig{MaxConcurrent: 1})
	require.NoError(t, server.Publish(publishedFixture("lineage-a", 1)))
	client := syncClient(t, path, "edge-a", syncSecret, "core-fixture")
	waiting := make(chan error, 1)
	go func() {
		_, err := client.Fetch(context.Background(), "lineage-a", 1, 2*time.Second)
		waiting <- err
	}()
	require.Eventually(t, func() bool { return len(server.slots) == 1 }, 2*time.Second, 5*time.Millisecond)
	_, err = syncClient(t, path, "edge-a", syncSecret, "core-fixture").Fetch(context.Background(), "", 0, 0)
	require.ErrorContains(t, err, "status 503")
	require.NoError(t, <-waiting)

	expired := publishedFixture("lineage-a", 2)
	expired.Snapshot.GeneratedAt = time.Now().Add(-20 * time.Second)
	expired.Snapshot.ExpiresAt = time.Now().Add(-time.Second)
	require.NoError(t, server.Publish(expired))
	_, err = client.Fetch(context.Background(), "", 0, 0)
	require.ErrorContains(t, err, "status 503", "an expired snapshot is never served")
	_, err = client.Fetch(context.Background(), "lineage-a", 1, 10*time.Millisecond)
	require.ErrorContains(t, err, "status 503")
	require.ErrorContains(t, server.Publish(publishedFixture("bad lineage", 3)), "identity")
	other := publishedFixture("lineage-a", 3)
	other.Snapshot.CoreInstance = "core-other"
	require.ErrorContains(t, server.Publish(other), "identity")
}

func TestDispatchSnapshotClientRejectsForgedResponses(t *testing.T) {
	envelope, err := json.Marshal(SnapshotEnvelope{Version: SnapshotEnvelopeVersion, PublishLineage: "lineage-a", Snapshot: testSnapshot(time.Now())})
	require.NoError(t, err)
	tests := map[string]struct {
		handler func(http.ResponseWriter, *http.Request)
		message string
	}{
		"unsigned snapshot": {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(envelope) }, "signature invalid"},
		"signature for another nonce": {func(w http.ResponseWriter, _ *http.Request) {
			writeSignedSnapshot(w, syncSecret, strings.Repeat("cd", 16), http.StatusOK, envelope)
		}, "signature invalid"},
		"signature with another key": {func(w http.ResponseWriter, r *http.Request) {
			writeSignedSnapshot(w, bytes.Repeat([]byte{9}, 32), r.Header.Get(headerSnapshotNonce), http.StatusOK, envelope)
		}, "signature invalid"},
		"unsigned not modified": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }, "signature invalid"},
		"204 signature replayed as 200": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(snapshotResponseMAC(syncSecret, r.Header.Get(headerSnapshotNonce), http.StatusNoContent, nil)))
			_, _ = w.Write(envelope)
		}, "signature invalid"},
		"oversize body": {func(w http.ResponseWriter, r *http.Request) {
			writeSignedSnapshot(w, syncSecret, r.Header.Get(headerSnapshotNonce), http.StatusOK, bytes.Repeat([]byte(" "), maxSnapshotEnvelope+1))
		}, "read limit"},
		"redirect": {func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "http://example.invalid/")
			w.WriteHeader(http.StatusFound)
		}, "status 302"},
		"signed core mismatch": {func(w http.ResponseWriter, r *http.Request) {
			snapshot := testSnapshot(time.Now())
			snapshot.CoreInstance = "core-other"
			body, _ := json.Marshal(SnapshotEnvelope{Version: SnapshotEnvelopeVersion, PublishLineage: "lineage-a", Snapshot: snapshot})
			writeSignedSnapshot(w, syncSecret, r.Header.Get(headerSnapshotNonce), http.StatusOK, body)
		}, "core identity"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			path := serveSyncHandler(t, http.HandlerFunc(tc.handler))
			_, err := syncClient(t, path, "edge-a", syncSecret, "core-fixture").Fetch(context.Background(), "", 0, 0)
			require.ErrorContains(t, err, tc.message)
		})
	}
	_, err = syncClient(t, syncSocketPath(t), "edge-a", syncSecret, "core-fixture").Fetch(context.Background(), "", 0, 0)
	require.ErrorContains(t, err, "unavailable")
}

func TestDispatchSnapshotEnvelopeStrictParse(t *testing.T) {
	now := time.Now()
	valid := SnapshotEnvelope{Version: SnapshotEnvelopeVersion, PublishLineage: "lineage-a", Snapshot: testSnapshot(now)}
	body, err := json.Marshal(valid)
	require.NoError(t, err)
	parsed, err := ParseSnapshotEnvelope(body, now)
	require.NoError(t, err)
	require.Equal(t, "lineage-a", parsed.PublishLineage)

	expired := valid
	expired.Snapshot.ExpiresAt = now.Add(-time.Second)
	expired.Snapshot.GeneratedAt = now.Add(-10 * time.Second)
	tests := map[string]any{
		"version":       SnapshotEnvelope{Version: 2, PublishLineage: "lineage-a", Snapshot: valid.Snapshot},
		"lineage":       SnapshotEnvelope{Version: 1, PublishLineage: "bad lineage", Snapshot: valid.Snapshot},
		"empty lineage": SnapshotEnvelope{Version: 1, Snapshot: valid.Snapshot},
		"expired":       expired,
		"unknown field": map[string]any{"version": 1, "publish_lineage": "lineage-a", "snapshot": valid.Snapshot, "extra": true},
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			_, err = ParseSnapshotEnvelope(encoded, now)
			require.Error(t, err)
		})
	}
	_, err = ParseSnapshotEnvelope(bytes.Repeat([]byte(" "), maxSnapshotEnvelope+1), now)
	require.Error(t, err)
	require.NotContains(t, string(body), "secret", "the envelope carries only the file-mode snapshot fields")
}

func TestDispatchSnapshotSocketListen(t *testing.T) {
	path := syncSocketPath(t)
	listener, err := ListenSnapshotSocket(path, 16)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o660), info.Mode().Perm())
	_, err = ListenSnapshotSocket(path, 16)
	require.ErrorContains(t, err, "in use")
	require.NoError(t, listener.Close())

	stale, err := net.Listen("unix", path)
	require.NoError(t, err)
	unixListener, ok := stale.(*net.UnixListener)
	require.True(t, ok)
	unixListener.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())
	_, err = os.Lstat(path)
	require.NoError(t, err, "stale socket remains on disk")
	replaced, err := ListenSnapshotSocket(path, 16)
	require.NoError(t, err, "a stale socket nobody listens on is replaced")
	require.NoError(t, replaced.Close())

	regular := filepath.Join(filepath.Dir(path), "regular")
	require.NoError(t, os.WriteFile(regular, []byte("keep"), 0o600))
	_, err = ListenSnapshotSocket(regular, 16)
	require.ErrorContains(t, err, "not a socket")
	content, err := os.ReadFile(regular)
	require.NoError(t, err)
	require.Equal(t, "keep", string(content))

	for _, invalid := range []string{"", "relative.sock", "/tmp/../tmp/x.sock", "/" + strings.Repeat("a", MaxSnapshotSocketPath)} {
		_, err = ListenSnapshotSocket(invalid, 16)
		require.ErrorContains(t, err, "invalid", invalid)
	}
}

func TestDispatchSnapshotServerConfigAndKeys(t *testing.T) {
	base := Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, SnapshotFile: "snapshot.json", Targets: []Target{testTarget("a", 1)}}
	withServer := base
	withServer.SnapshotServer = &SnapshotServerConfig{SocketPath: "/run/dispatchd/s.sock", KeysFile: "keys.json"}
	cfg, err := normalizeConfig(withServer)
	require.NoError(t, err)
	require.Equal(t, 8, cfg.SnapshotServer.MaxConcurrent)
	require.Equal(t, 20, cfg.SnapshotServer.MaxRequestsPerSecond)
	plain, err := normalizeConfig(base)
	require.NoError(t, err)
	require.Nil(t, plain.SnapshotServer)
	encoded, err := json.Marshal(plain)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "snapshot_server", "the field is omitted when disabled")

	for name, server := range map[string]SnapshotServerConfig{
		"relative socket":  {SocketPath: "s.sock", KeysFile: "keys.json"},
		"missing keys":     {SocketPath: "/run/s.sock"},
		"concurrency high": {SocketPath: "/run/s.sock", KeysFile: "keys.json", MaxConcurrent: 65},
		"rate negative":    {SocketPath: "/run/s.sock", KeysFile: "keys.json", MaxRequestsPerSecond: -1},
		"rate high":        {SocketPath: "/run/s.sock", KeysFile: "keys.json", MaxRequestsPerSecond: 1001},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := base
			invalid.SnapshotServer = &server
			_, err := normalizeConfig(invalid)
			require.Error(t, err)
		})
	}

	plainDigest, err := runtimeConfigDigest(plain, plain.ProbeLimits())
	require.NoError(t, err)
	serverDigest, err := runtimeConfigDigest(cfg, cfg.ProbeLimits())
	require.NoError(t, err)
	require.Equal(t, plainDigest, serverDigest, "snapshot_server is a transport setting outside the routing config digest")

	dir := t.TempDir()
	writeKeys := func(body string) string {
		path := filepath.Join(dir, "keys.json")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		return path
	}
	secret := base64.StdEncoding.EncodeToString(syncSecret)
	keys, err := LoadSnapshotSyncKeys(writeKeys(`{"version":1,"keys":[{"id":"edge-a","secret_base64":"` + secret + `"}]}`))
	require.NoError(t, err)
	require.Equal(t, syncSecret, keys["edge-a"])
	many := make([]string, maxSnapshotSyncKeys+1)
	for i := range many {
		many[i] = `{"id":"edge-` + strconv.Itoa(i) + `","secret_base64":"` + secret + `"}`
	}
	for name, body := range map[string]string{
		"version":       `{"version":2,"keys":[{"id":"edge-a","secret_base64":"` + secret + `"}]}`,
		"empty":         `{"version":1,"keys":[]}`,
		"too many":      `{"version":1,"keys":[` + strings.Join(many, ",") + `]}`,
		"duplicate":     `{"version":1,"keys":[{"id":"edge-a","secret_base64":"` + secret + `"},{"id":"edge-a","secret_base64":"` + secret + `"}]}`,
		"short secret":  `{"version":1,"keys":[{"id":"edge-a","secret_base64":"` + base64.StdEncoding.EncodeToString([]byte("short")) + `"}]}`,
		"bad id":        `{"version":1,"keys":[{"id":"edge a","secret_base64":"` + secret + `"}]}`,
		"unknown field": `{"version":1,"keys":[{"id":"edge-a","secret_base64":"` + secret + `","extra":1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadSnapshotSyncKeys(writeKeys(body))
			require.Error(t, err)
		})
	}
}
