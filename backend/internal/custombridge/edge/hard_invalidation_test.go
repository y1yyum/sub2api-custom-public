package edge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

type hardSubscriptionServer struct {
	subscriptionServer
	feed *dispatch.InvalidationFeed
}

func startHardSubscriptionServer(t *testing.T, path string) hardSubscriptionServer {
	t.Helper()
	cfg := dispatch.SnapshotServerConfig{MaxConcurrent: 8, MaxRequestsPerSecond: 1000,
		HardInvalidation: &dispatch.HardInvalidationConfig{PollIntervalMilliseconds: 500}}
	keys := map[string][]byte{"edge-a": subscriptionSecret}
	snapshots, err := dispatch.NewSnapshotServer("core-fixture", cfg, keys)
	require.NoError(t, err)
	feed, err := dispatch.NewInvalidationFeed("core-fixture", cfg, keys)
	require.NoError(t, err)
	listener, err := dispatch.ListenSnapshotSocket(path, 24)
	require.NoError(t, err)
	httpServer := dispatch.NewSnapshotHTTPServer(dispatch.NewSyncHandler(snapshots, feed))
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
	return hardSubscriptionServer{subscriptionServer{snapshots, stop}, feed}
}

func subscribeHard(settings *ShadowDispatchConfig, socket string) {
	subscribe(settings, socket, subscriptionSecret)
	settings.SnapshotSubscription.HardInvalidation = &HardInvalidationSubscriptionConfig{WaitMilliseconds: 200}
}

func credentialHold(revision int64) dispatch.HealthHoldReport {
	scope := dispatch.HealthHoldScope{Kind: "credential", ProviderRef: "provider-fixture", CredentialRef: "credential-a"}
	return dispatch.HealthHoldReport{ID: dispatch.HealthHoldID("core-fixture", dispatch.FailureCredentialBanned, scope),
		Revision: revision, Class: dispatch.FailureCredentialBanned, Scope: scope, Active: true}
}

func observeHolds(t *testing.T, server hardSubscriptionServer, lineage string, revision int64, holds ...dispatch.HealthHoldReport) {
	t.Helper()
	require.NoError(t, server.feed.Observe(dispatch.HoldInvalidationState{PublishLineage: lineage, SnapshotRevision: revision, Holds: holds}))
}

func TestEdgeHardInvalidationRemovesTargetWithinTTLAndSurvivesReconnect(t *testing.T) {
	socket := subscriptionSocket(t)
	server := startHardSubscriptionServer(t, socket)
	publishSubscription(t, server.subscriptionServer, "lineage-a", 10)
	observeHolds(t, server, "lineage-a", 10)
	cfg := shadowConfig(t, "https://core.test")
	subscribeHard(cfg.ShadowDispatch, socket)
	handler, err := NewHandler(cfg)
	require.NoError(t, err)
	defer handler.Close()
	stats := handler.ShadowStats()
	require.True(t, stats.InvalidationRefreshOK, "the initial invalidation fetch is synchronous")
	require.Equal(t, int64(1), stats.InvalidationSequence)
	grant := cfg.Grants[0]
	handler.shadow.observe(grant, "fixture-model", time.Now())
	require.Zero(t, handler.ShadowStats().Unavailable)

	started := time.Now()
	observeHolds(t, server, "lineage-a", 10, credentialHold(1))
	stats = waitShadowStats(t, handler, func(s ShadowStats) bool { return s.InvalidatedCandidates == 1 })
	require.Less(t, time.Since(started), 2*time.Second)
	require.True(t, time.Now().Before(stats.SnapshotExpiresAt), "removal happens inside the snapshot TTL")
	require.Equal(t, int64(10), stats.SnapshotRevision, "no new snapshot was needed")
	require.Equal(t, uint64(1), stats.InvalidationsApplied)
	handler.shadow.observe(grant, "fixture-model", time.Now())
	require.Equal(t, uint64(1), handler.ShadowStats().NoEligibleRoute)

	server.stop()
	stats = waitShadowStats(t, handler, func(s ShadowStats) bool { return !s.InvalidationRefreshOK && s.InvalidationFailures > 0 })
	require.Equal(t, 1, stats.InvalidatedCandidates, "a disconnect never loosens the overlay")

	restarted := startHardSubscriptionServer(t, socket)
	publishSubscription(t, restarted.subscriptionServer, "lineage-a", 10)
	observeHolds(t, restarted, "lineage-a", 10)
	stats = waitShadowStats(t, handler, func(s ShadowStats) bool { return s.InvalidationRefreshOK && s.InvalidationSequence == 1 })
	require.Equal(t, 1, stats.InvalidatedCandidates, "a reset from a restarted feed does not revive removed targets")
	handler.shadow.observe(grant, "fixture-model", time.Now())
	require.Equal(t, uint64(2), handler.ShadowStats().NoEligibleRoute)

	publishSubscription(t, restarted.subscriptionServer, "lineage-a", 11)
	stats = waitShadowStats(t, handler, func(s ShadowStats) bool { return s.SnapshotRevision == 11 })
	require.Zero(t, stats.InvalidatedCandidates, "a snapshot past the floor takes over")
	require.Zero(t, stats.InvalidationEntries)
	handler.shadow.observe(grant, "fixture-model", time.Now())
	require.Equal(t, uint64(2), handler.ShadowStats().Unavailable, "the target is selectable again")
}

func TestEdgeHardInvalidationSameHoldAcrossLineages(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer core.Close()
	socket := subscriptionSocket(t)
	server := startHardSubscriptionServer(t, socket)
	publishSubscription(t, server.subscriptionServer, "lineage-a", 10)
	hold := credentialHold(1)
	observeHolds(t, server, "lineage-a", 10, hold)
	cfg := strictConfig(t, core.URL)
	subscribeHard(cfg.StrictDispatch, socket)
	handler, edge := newTestEdge(t, cfg)
	checkRequest := func(status int) {
		t.Helper()
		response := edgePost(t, edge.URL, strictBody, nil)
		_ = readBody(t, response)
		require.Equal(t, status, response.StatusCode)
	}
	checkRequest(http.StatusServiceUnavailable)
	oldSequence := handler.StrictStats().InvalidationSequence
	observeHolds(t, server, "lineage-b", 3, hold)
	waitStrictStats(t, handler, func(s StrictStats) bool { return s.InvalidationSequence > oldSequence })
	publishSubscription(t, server.subscriptionServer, "lineage-a", 11)
	waitStrictStats(t, handler, func(s StrictStats) bool { return s.SnapshotRevision == 11 })
	checkRequest(http.StatusServiceUnavailable)
	publishSubscription(t, server.subscriptionServer, "lineage-b", 3)
	waitStrictStats(t, handler, func(s StrictStats) bool { return s.SnapshotLineage == "lineage-b" })
	checkRequest(http.StatusServiceUnavailable)
	observeHolds(t, server, "lineage-b", 3)
	publishSubscription(t, server.subscriptionServer, "lineage-b", 4)
	stats := waitStrictStats(t, handler, func(s StrictStats) bool { return s.SnapshotRevision == 4 })
	require.Zero(t, stats.InvalidationEntries)
	checkRequest(http.StatusOK)
}

func TestEdgeHardInvalidationKeepsInflightStream(t *testing.T) {
	finish := make(chan struct{})
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		<-finish
		_, _ = io.WriteString(w, "data: done\n\n")
	}))
	defer core.Close()
	socket := subscriptionSocket(t)
	server := startHardSubscriptionServer(t, socket)
	publishSubscription(t, server.subscriptionServer, "lineage-a", 10)
	observeHolds(t, server, "lineage-a", 10)
	cfg := strictConfig(t, core.URL)
	cfg.Grants[0].StrictTargets[0].MaxInflight = 2
	subscribeHard(cfg.StrictDispatch, socket)
	handler, edgeServer := newTestEdge(t, cfg)

	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, edgeServer.URL+"/v1/responses",
		strings.NewReader(strings.TrimSuffix(strictBody, "}")+`,"stream":true}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: first\n", line)

	observeHolds(t, server, "lineage-a", 10, credentialHold(1))
	waitStrictStats(t, handler, func(s StrictStats) bool { return s.InvalidatedCandidates == 1 })
	blocked := edgePost(t, edgeServer.URL, strictBody, nil)
	require.Equal(t, http.StatusServiceUnavailable, blocked.StatusCode, "new requests no longer select the target")
	_ = readBody(t, blocked)
	require.Equal(t, uint64(1), handler.StrictStats().Inflight)

	close(finish)
	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(rest), "data: done", "the in-flight stream completes")
	stats := waitStrictStats(t, handler, func(s StrictStats) bool { return s.Inflight == 0 && s.Released == 1 })
	require.Equal(t, uint64(1), stats.Selected)
}

func TestEdgeHardInvalidationDisabledIsUnchanged(t *testing.T) {
	socket := subscriptionSocket(t)
	server := startSubscriptionServer(t, socket)
	publishSubscription(t, server, "lineage-a", 10)
	cfg := shadowConfig(t, "https://core.test")
	subscribe(cfg.ShadowDispatch, socket, subscriptionSecret)
	normalized, err := normalizeConfig(cfg)
	require.NoError(t, err)
	encoded, err := json.Marshal(normalized.ShadowDispatch.SnapshotSubscription)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "hard_invalidation")
	handler, err := NewHandler(cfg)
	require.NoError(t, err)
	defer handler.Close()
	require.Nil(t, handler.shadow.hard)
	encoded, err = json.Marshal(handler.ShadowStats())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "invalidat", "disabled stats keep their previous fields")
	require.Equal(t, ShadowStats{}.InvalidationStats, handler.ShadowStats().InvalidationStats)
}

func TestEdgeHardInvalidationConfig(t *testing.T) {
	cfg := shadowConfig(t, "https://core.test")
	subscribeHard(cfg.ShadowDispatch, "/run/dispatchd/s.sock")
	cfg.ShadowDispatch.SnapshotSubscription.HardInvalidation.WaitMilliseconds = 0
	normalized, err := normalizeConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, 10000, normalized.ShadowDispatch.SnapshotSubscription.HardInvalidation.WaitMilliseconds)
	for _, wait := range []int{-1, 25001} {
		cfg.ShadowDispatch.SnapshotSubscription.HardInvalidation.WaitMilliseconds = wait
		_, err := normalizeConfig(cfg)
		require.Error(t, err, wait)
	}
	cfg.ShadowDispatch.SnapshotSubscription.HardInvalidation.WaitMilliseconds = 1
	normalized, err = normalizeConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, 1, normalized.ShadowDispatch.SnapshotSubscription.HardInvalidation.WaitMilliseconds)
	cfg.ShadowDispatch.SnapshotSubscription.HardInvalidation.WaitMilliseconds = 200
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	section := `"hard_invalidation":{"wait_milliseconds":200}`
	require.Contains(t, string(body), section)
	for name, replacement := range map[string]string{
		"valid":      section,
		"unknown":    `"hard_invalidation":{"wait_milliseconds":200,"extra":1}`,
		"duplicate":  `"hard_invalidation":{"wait_milliseconds":200,"wait_milliseconds":300}`,
		"case alias": `"hard_invalidation":{"Wait_Milliseconds":200}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "edge.json")
			require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(body), section, replacement, 1)), 0o600))
			_, err := LoadConfig(path)
			if name == "valid" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}
