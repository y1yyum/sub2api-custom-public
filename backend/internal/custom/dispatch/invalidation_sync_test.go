package dispatch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func hardServerConfig() SnapshotServerConfig {
	return SnapshotServerConfig{SocketPath: "/run/dispatch.sock", KeysFile: "/run/keys.json", MaxConcurrent: 8, MaxRequestsPerSecond: 1000,
		HardInvalidation: &HardInvalidationConfig{PollIntervalMilliseconds: 500}}
}

func startInvalidationFeed(t *testing.T, cfg SnapshotServerConfig) (*InvalidationFeed, *SnapshotServer, string) {
	t.Helper()
	keys := map[string][]byte{"edge-a": syncSecret}
	snapshots, err := NewSnapshotServer("core-fixture", cfg, keys)
	require.NoError(t, err)
	feed, err := NewInvalidationFeed("core-fixture", cfg, keys)
	require.NoError(t, err)
	return feed, snapshots, serveSyncHandler(t, NewSyncHandler(snapshots, feed))
}

func invalidationClient(t *testing.T, path string, secret []byte) *InvalidationClient {
	t.Helper()
	client := NewInvalidationClient(path, "edge-a", secret, "core-fixture")
	t.Cleanup(client.Close)
	return client
}

func activeHold(credential string, revision int64) HealthHoldReport {
	scope := HealthHoldScope{Kind: "credential", ProviderRef: "provider", CredentialRef: credential}
	return HealthHoldReport{ID: healthHoldID("core-fixture", FailureCredentialBanned, scope), Revision: revision,
		Class: FailureCredentialBanned, Scope: scope, Active: true}
}

func holdState(lineage string, revision int64, holds ...HealthHoldReport) HoldInvalidationState {
	return HoldInvalidationState{PublishLineage: lineage, SnapshotRevision: revision, Holds: holds}
}

func signedInvalidationRequest(keyID string, secret []byte, timestamp time.Time, query invalidationQuery) *http.Request {
	request := httptest.NewRequest(http.MethodGet, InvalidationSyncPath+"?"+query.values().Encode(), nil)
	ts := strconv.FormatInt(timestamp.Unix(), 10)
	nonce := strings.Repeat("ab", 16)
	request.Header.Set(headerSnapshotKey, keyID)
	request.Header.Set(headerSnapshotTime, ts)
	request.Header.Set(headerSnapshotNonce, nonce)
	request.Header.Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(invalidationRequestMAC(secret, keyID, ts, nonce, query)))
	return request
}

func TestDispatchInvalidationFeedSequenceDeltaAndReset(t *testing.T) {
	feed, snapshots, path := startInvalidationFeed(t, hardServerConfig())
	client := invalidationClient(t, path, syncSecret)
	ctx := context.Background()
	_, err := client.Fetch(ctx, "", 0, 0)
	require.ErrorContains(t, err, "status 503", "not ready before a publish lineage exists")
	require.NoError(t, feed.Observe(HoldInvalidationState{}))
	_, err = client.Fetch(ctx, "", 0, 0)
	require.ErrorContains(t, err, "status 503")

	require.NoError(t, feed.Observe(holdState("lineage-a", 10)))
	first, err := client.Fetch(ctx, "", 0, 0)
	require.NoError(t, err)
	require.True(t, first.Envelope.Reset)
	require.Equal(t, int64(1), first.Envelope.Sequence)
	require.Empty(t, first.Envelope.Entries)
	feedID := first.Envelope.Feed

	woke := make(chan InvalidationFetch, 1)
	go func() {
		fetch, err := client.Fetch(ctx, feedID, 1, 5*time.Second)
		if err == nil {
			woke <- fetch
		}
		close(woke)
	}()
	time.Sleep(50 * time.Millisecond)
	started := time.Now()
	require.NoError(t, feed.Observe(holdState("lineage-a", 12, activeHold("credential-a", 1))))
	delta, ok := <-woke
	require.True(t, ok)
	require.Less(t, time.Since(started), 2*time.Second, "the long poll wakes on a new hold")
	require.False(t, delta.Envelope.Reset)
	require.Equal(t, int64(2), delta.Envelope.Sequence)
	require.Len(t, delta.Envelope.Entries, 1)
	require.Equal(t, Invalidation{HoldID: activeHold("credential-a", 1).ID, HoldRevision: 1, Class: FailureCredentialBanned,
		Scope: activeHold("credential-a", 1).Scope, PublishLineage: "lineage-a", FloorRevision: 12, Sequence: 2}, delta.Envelope.Entries[0])

	require.NoError(t, feed.Observe(holdState("lineage-a", 13, activeHold("credential-a", 1))))
	current, err := client.Fetch(ctx, feedID, 2, 0)
	require.NoError(t, err)
	require.True(t, current.NotModified, "an unchanged hold is idempotent")

	require.NoError(t, feed.Observe(holdState("lineage-a", 14, activeHold("credential-a", 2), activeHold("credential-b", 1))))
	delta, err = client.Fetch(ctx, feedID, 2, 0)
	require.NoError(t, err)
	require.Equal(t, int64(4), delta.Envelope.Sequence)
	require.Len(t, delta.Envelope.Entries, 2)
	require.Equal(t, int64(2), delta.Envelope.Entries[0].HoldRevision, "a re-triggered hold is a new entry")

	for name, position := range map[string]struct {
		feed     string
		sequence int64
	}{"other feed": {"feed-from-restart", 3}, "ahead of server": {feedID, 99}} {
		t.Run(name, func(t *testing.T) {
			reset, err := client.Fetch(ctx, position.feed, position.sequence, 0)
			require.NoError(t, err)
			require.True(t, reset.Envelope.Reset)
			require.Len(t, reset.Envelope.Entries, 2, "a reset resends every retained entry")
		})
	}

	require.NoError(t, feed.Observe(holdState("lineage-b", 3, activeHold("credential-a", 2), activeHold("credential-b", 1))))
	rotated, err := client.Fetch(ctx, feedID, 4, 0)
	require.NoError(t, err)
	require.Equal(t, "lineage-b", rotated.Envelope.PublishLineage)
	require.Equal(t, int64(7), rotated.Envelope.Sequence)
	require.Equal(t, []Invalidation{
		credentialInvalidation("credential-a", "lineage-b", 3, 6, 2),
		credentialInvalidation("credential-b", "lineage-b", 3, 7, 1),
	}, rotated.Envelope.Entries)
	require.NoError(t, feed.Observe(holdState("lineage-b", 4, activeHold("credential-a", 2), activeHold("credential-b", 1))))
	current, err = client.Fetch(ctx, feedID, 7, 0)
	require.NoError(t, err)
	require.True(t, current.NotModified, "updated floors are emitted once per lineage")

	require.NoError(t, snapshots.Publish(publishedFixture("lineage-b", 3)))
	snapshot, err := syncClient(t, path, "edge-a", syncSecret, "core-fixture").Fetch(ctx, "", 0, 0)
	require.NoError(t, err)
	require.Equal(t, int64(3), snapshot.Envelope.Snapshot.Revision, "the snapshot path is unchanged on the shared listener")
}

func TestDispatchInvalidationFeedRetentionAndRelease(t *testing.T) {
	feed, err := NewInvalidationFeed("core-fixture", hardServerConfig(), map[string][]byte{"edge-a": syncSecret})
	require.NoError(t, err)
	clock := time.Now()
	feed.now = func() time.Time { return clock }
	require.NoError(t, feed.Observe(holdState("lineage-a", 10, activeHold("credential-a", 1))))
	clock = clock.Add(InvalidationRetention - time.Millisecond)
	require.NoError(t, feed.Observe(holdState("lineage-a", 11, activeHold("credential-a", 1))))
	require.Len(t, feed.records, 1)
	clock = clock.Add(time.Millisecond)
	require.NoError(t, feed.Observe(holdState("lineage-a", 11, activeHold("credential-a", 1))))
	require.Empty(t, feed.records, "entries older than every live snapshot TTL are dropped")
	require.NoError(t, feed.Observe(holdState("lineage-a", 12)))
	require.NoError(t, feed.Observe(holdState("lineage-a", 13, activeHold("credential-a", 1))))
	require.Len(t, feed.records, 1, "a released then reactivated hold is reported again")
	require.Equal(t, int64(13), feed.records[activeHold("credential-a", 1).ID].entry.FloorRevision)
	require.Error(t, feed.Observe(HoldInvalidationState{PublishLineage: "bad lineage"}))
}

func TestDispatchInvalidationFeedRejectsUnauthenticatedRequests(t *testing.T) {
	feed, _, path := startInvalidationFeed(t, hardServerConfig())
	require.NoError(t, feed.Observe(holdState("lineage-a", 1)))
	ctx := context.Background()
	_, err := invalidationClient(t, path, bytes.Repeat([]byte{8}, 32)).Fetch(ctx, "", 0, 0)
	require.ErrorContains(t, err, "status 401")
	query := invalidationQuery{core: "core-fixture"}
	tests := map[string]func(*http.Request){
		"stale timestamp": func(r *http.Request) {
			*r = *signedInvalidationRequest("edge-a", syncSecret, time.Now().Add(-31*time.Second), query)
		},
		"future timestamp": func(r *http.Request) {
			*r = *signedInvalidationRequest("edge-a", syncSecret, time.Now().Add(31*time.Second), query)
		},
		"unknown key": func(r *http.Request) { *r = *signedInvalidationRequest("edge-b", syncSecret, time.Now(), query) },
		"tampered signature": func(r *http.Request) {
			r.Header.Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
		},
		"signature over another position": func(r *http.Request) {
			r.URL.RawQuery = invalidationQuery{core: "core-fixture", wait: time.Millisecond}.values().Encode()
		},
		"snapshot domain signature": func(r *http.Request) {
			ts, nonce := r.Header.Get(headerSnapshotTime), r.Header.Get(headerSnapshotNonce)
			r.Header.Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(
				snapshotRequestMAC(syncSecret, "edge-a", ts, nonce, snapshotQuery{core: "core-fixture"})))
		},
		"missing nonce": func(r *http.Request) { r.Header.Del(headerSnapshotNonce) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := signedInvalidationRequest("edge-a", syncSecret, time.Now(), query)
			mutate(request)
			recorder := httptest.NewRecorder()
			feed.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.Empty(t, recorder.Body.Bytes())
			require.Empty(t, recorder.Header().Get(headerSnapshotSignature))
		})
	}
	for name, raw := range map[string]string{
		"feed without sequence": invalidationQuery{core: "core-fixture", feed: "feed-a"}.values().Encode(),
		"sequence without feed": invalidationQuery{core: "core-fixture", sequence: 2}.values().Encode(),
		"extra parameter":       query.values().Encode() + "&extra=1",
		"wait above limit":      invalidationQuery{core: "core-fixture", wait: MaxSnapshotSyncWait + time.Millisecond}.values().Encode(),
	} {
		t.Run(name, func(t *testing.T) {
			request := signedInvalidationRequest("edge-a", syncSecret, time.Now(), query)
			request.URL.RawQuery = raw
			recorder := httptest.NewRecorder()
			feed.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
	recorder := httptest.NewRecorder()
	feed.ServeHTTP(recorder, signedInvalidationRequest("edge-a", syncSecret, time.Now(), invalidationQuery{core: "core-other"}))
	require.Equal(t, http.StatusNotFound, recorder.Code)
	recorder = httptest.NewRecorder()
	feed.ServeHTTP(recorder, signedInvalidationRequest("edge-a", syncSecret, time.Now(), query))
	require.Equal(t, http.StatusOK, recorder.Code, "the unmodified request is accepted")
}

func TestDispatchInvalidationRateAndSlotsAreIndependent(t *testing.T) {
	cfg := hardServerConfig()
	cfg.MaxRequestsPerSecond, cfg.MaxConcurrent = 1, 1
	feed, snapshots, _ := startInvalidationFeed(t, cfg)
	fixed := time.Now()
	feed.now = func() time.Time { return fixed }
	require.NoError(t, feed.Observe(holdState("lineage-a", 1)))
	require.NoError(t, snapshots.Publish(publishedFixture("lineage-a", 1)))
	for range 2 {
		snapshots.ServeHTTP(httptest.NewRecorder(), signedSyncRequest(t, "edge-a", syncSecret, time.Now(), snapshotQuery{core: "core-fixture"}))
	}
	recorder := httptest.NewRecorder()
	feed.ServeHTTP(recorder, signedInvalidationRequest("edge-a", syncSecret, fixed, invalidationQuery{core: "core-fixture"}))
	require.Equal(t, http.StatusOK, recorder.Code, "an exhausted snapshot rate window does not block invalidations")
	recorder = httptest.NewRecorder()
	feed.ServeHTTP(recorder, signedInvalidationRequest("edge-a", syncSecret, fixed, invalidationQuery{core: "core-fixture"}))
	require.Equal(t, http.StatusTooManyRequests, recorder.Code, "the invalidation path keeps its own rate limit")
}

func TestDispatchInvalidationClientRejectsForgedResponses(t *testing.T) {
	envelope := func(mutate func(*InvalidationEnvelope)) []byte {
		value := InvalidationEnvelope{Version: InvalidationEnvelopeVersion, CoreInstance: "core-fixture", Feed: "feed-a", Sequence: 3,
			PublishLineage: "lineage-a", Entries: []Invalidation{credentialInvalidation("credential-a", "lineage-a", 1, 3, 1)}}
		mutate(&value)
		body, err := json.Marshal(value)
		require.NoError(t, err)
		return body
	}
	signed := func(body []byte) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			writeSignedInvalidation(w, syncSecret, r.Header.Get(headerSnapshotNonce), http.StatusOK, body)
		}
	}
	valid := envelope(func(*InvalidationEnvelope) {})
	tests := map[string]struct {
		handler func(http.ResponseWriter, *http.Request)
		message string
	}{
		"unsigned": {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(valid) }, "signature invalid"},
		"replayed for another nonce": {func(w http.ResponseWriter, _ *http.Request) {
			writeSignedInvalidation(w, syncSecret, strings.Repeat("cd", 16), http.StatusOK, valid)
		}, "signature invalid"},
		"snapshot domain signature": {func(w http.ResponseWriter, r *http.Request) {
			writeSignedSnapshot(w, syncSecret, r.Header.Get(headerSnapshotNonce), http.StatusOK, valid)
		}, "signature invalid"},
		"204 signature replayed as 200": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(headerSnapshotSignature, base64.StdEncoding.EncodeToString(
				invalidationResponseMAC(syncSecret, r.Header.Get(headerSnapshotNonce), http.StatusNoContent, nil)))
			_, _ = w.Write(valid)
		}, "signature invalid"},
		"core mismatch": {signed(envelope(func(e *InvalidationEnvelope) {
			e.CoreInstance = "core-other"
			e.Entries = []Invalidation{}
		})), "core identity"},
		"delta from another feed": {signed(envelope(func(e *InvalidationEnvelope) { e.Feed = "feed-b" })), "out of order"},
		"sequence regression":     {signed(envelope(func(e *InvalidationEnvelope) { e.Sequence, e.Entries = 1, []Invalidation{} })), "out of order"},
		"stale entry in delta": {signed(envelope(func(e *InvalidationEnvelope) {
			e.Entries[0].Sequence = 2
		})), "out of order"},
		"entry beyond envelope": {signed(envelope(func(e *InvalidationEnvelope) { e.Entries[0].Sequence = 4 })), "invalid or duplicate"},
		"duplicate entries": {signed(envelope(func(e *InvalidationEnvelope) {
			e.Sequence = 4
			e.Entries = append(e.Entries, credentialInvalidation("credential-a", "lineage-a", 1, 4, 2))
		})), "invalid or duplicate"},
		"unknown entry field":   {signed(bytes.Replace(valid, []byte(`"floor_revision":1`), []byte(`"future":1,"floor_revision":1`), 1)), "JSON"},
		"duplicate entry field": {signed(bytes.Replace(valid, []byte(`"floor_revision":1`), []byte(`"floor_revision":0,"floor_revision":1`), 1)), "JSON"},
		"case alias in entry":   {signed(bytes.Replace(valid, []byte(`"floor_revision":1`), []byte(`"Floor_Revision":1`), 1)), "JSON"},
		"missing floor":         {signed(bytes.Replace(valid, []byte(`"floor_revision":1,`), nil, 1)), "JSON"},
		"null floor":            {signed(bytes.Replace(valid, []byte(`"floor_revision":1`), []byte(`"floor_revision":null`), 1)), "JSON"},
		"wrong hold id":         {signed(envelope(func(e *InvalidationEnvelope) { e.Entries[0].HoldID = "hold:forged" })), "invalid or duplicate"},
		"nil entries":           {signed([]byte(`{"version":1,"core_instance":"core-fixture","feed":"feed-a","sequence":3,"publish_lineage":"lineage-a","reset":false,"entries":null}`)), "identity"},
		"unknown field":         {signed([]byte(`{"version":1,"core_instance":"core-fixture","feed":"feed-a","sequence":3,"publish_lineage":"lineage-a","reset":false,"entries":[],"extra":1}`)), "JSON"},
		"case alias":            {signed([]byte(`{"version":1,"Core_Instance":"core-fixture","feed":"feed-a","sequence":3,"publish_lineage":"lineage-a","reset":false,"entries":[]}`)), "JSON"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			path := serveSyncHandler(t, http.HandlerFunc(tc.handler))
			_, err := invalidationClient(t, path, syncSecret).Fetch(context.Background(), "feed-a", 2, 0)
			require.ErrorContains(t, err, tc.message)
		})
	}
	zeroFloor := envelope(func(e *InvalidationEnvelope) { e.Entries[0].FloorRevision = 0 })
	path := serveSyncHandler(t, http.HandlerFunc(signed(zeroFloor)))
	fetch, err := invalidationClient(t, path, syncSecret).Fetch(context.Background(), "feed-a", 2, 0)
	require.NoError(t, err)
	require.Len(t, fetch.Envelope.Entries, 1)
	_, err = invalidationClient(t, path, syncSecret).Fetch(context.Background(), "feed-a", 0, 0)
	require.ErrorContains(t, err, "position")
}

func TestDispatchHardInvalidationConfig(t *testing.T) {
	base := SnapshotServerConfig{SocketPath: "/run/dispatch.sock", KeysFile: "/run/keys.json"}
	disabled, err := normalizeSnapshotServerConfig(base)
	require.NoError(t, err)
	require.Nil(t, disabled.HardInvalidation)
	body, err := json.Marshal(disabled)
	require.NoError(t, err)
	require.Equal(t, `{"socket_path":"/run/dispatch.sock","keys_file":"/run/keys.json","max_concurrent":8,"max_requests_per_second":20}`, string(body),
		"the disabled configuration encodes exactly as before")

	base.HardInvalidation = &HardInvalidationConfig{}
	enabled, err := normalizeSnapshotServerConfig(base)
	require.NoError(t, err)
	require.Equal(t, 500, enabled.HardInvalidation.PollIntervalMilliseconds)
	for _, interval := range []int{99, 5001, -1} {
		base.HardInvalidation = &HardInvalidationConfig{PollIntervalMilliseconds: interval}
		_, err := normalizeSnapshotServerConfig(base)
		require.Error(t, err, interval)
	}
	for _, valid := range []int{100, 5000} {
		base.HardInvalidation = &HardInvalidationConfig{PollIntervalMilliseconds: valid}
		_, err := normalizeSnapshotServerConfig(base)
		require.NoError(t, err, valid)
	}
	for name, raw := range map[string]string{
		"unknown":    `{"socket_path":"/s","keys_file":"k","hard_invalidation":{"poll_interval_milliseconds":500,"extra":1}}`,
		"duplicate":  `{"socket_path":"/s","keys_file":"k","hard_invalidation":{"poll_interval_milliseconds":500,"poll_interval_milliseconds":600}}`,
		"case alias": `{"socket_path":"/s","keys_file":"k","hard_invalidation":{"Poll_Interval_Milliseconds":500}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var cfg SnapshotServerConfig
			require.Error(t, decodeJSON([]byte(raw), &cfg))
		})
	}
	_, path := startSyncServer(t, SnapshotServerConfig{})
	_, err = invalidationClient(t, path, syncSecret).Fetch(t.Context(), "", 0, 0)
	require.ErrorContains(t, err, "status 404", "without the section the snapshot server does not serve invalidations")
	_, err = NewInvalidationFeed("core-fixture", SnapshotServerConfig{MaxConcurrent: 1, MaxRequestsPerSecond: 1}, map[string][]byte{"edge-a": syncSecret})
	require.Error(t, err, "a feed requires the hard invalidation section")
}
