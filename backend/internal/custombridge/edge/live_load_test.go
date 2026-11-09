package edge

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEdgeStrictRequiresFreshLiveLoad(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer core.Close()
	cfg := strictConfig(t, core.URL)
	cfg.StrictDispatch.RequireLiveLoad = true
	now := time.Now()
	snapshot := shadowSnapshot(now)
	writeShadowSnapshot(t, cfg.StrictDispatch.SnapshotFile, snapshot)
	handler, server := newTestEdge(t, cfg)
	handler.strict.cache.close()
	response := edgePost(t, server.URL, strictBody, nil)
	readBody(t, response)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing load status=%d", response.StatusCode)
	}
	snapshot.Revision++
	snapshot.LoadObservedAt = now
	snapshot.LoadExpiresAt = now.Add(5 * time.Second)
	snapshot.ExpiresAt = snapshot.LoadExpiresAt
	writeShadowSnapshot(t, cfg.StrictDispatch.SnapshotFile, snapshot)
	handler.strict.cache.refresh(now)
	response = edgePost(t, server.URL, strictBody, nil)
	readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fresh load status=%d", response.StatusCode)
	}
	decision, release := handler.strict.acquire(cfg.Grants[0], "fixture-model", handler.permits[cfg.Grants[0].ID], now.Add(6*time.Second))
	if release != nil {
		release()
		t.Fatal("expired load was admitted")
	}
	if decision.Reason != "snapshot_expired" {
		t.Fatalf("expired load reason=%s", decision.Reason)
	}
}
