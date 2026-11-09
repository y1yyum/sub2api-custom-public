//go:build integration

package repository

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

var hardInvalidationSecret = bytes.Repeat([]byte{4}, 32)

type hardInvalidationRig struct {
	server      *dispatch.SnapshotServer
	snapshots   *dispatch.SnapshotClient
	invalidates *dispatch.InvalidationClient
	selector    *dispatch.Selector
	feed        string
	sequence    int64
}

// startHardInvalidationRig 以 dispatchd 相同的组件启动 UDS 服务与只读 hold 轮询，Edge 侧直接使用 Selector。
func startHardInvalidationRig(t *testing.T, store *dispatch.Store, cfg dispatch.Config) *hardInvalidationRig {
	t.Helper()
	dir, err := os.MkdirTemp("", "b3i")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	server := *cfg.SnapshotServer
	server.MaxConcurrent, server.MaxRequestsPerSecond = 8, 1000
	keys := map[string][]byte{"edge-a": hardInvalidationSecret}
	snapshots, err := dispatch.NewSnapshotServer(cfg.CoreInstance, server, keys)
	require.NoError(t, err)
	feed, err := dispatch.NewInvalidationFeed(cfg.CoreInstance, server, keys)
	require.NoError(t, err)
	listener, err := dispatch.ListenSnapshotSocket(socket, 24)
	require.NoError(t, err)
	httpServer := dispatch.NewSnapshotHTTPServer(dispatch.NewSyncHandler(snapshots, feed))
	go func() { _ = httpServer.Serve(listener) }()
	ctx, cancel := context.WithCancel(context.Background())
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		dispatch.WatchHoldInvalidations(ctx, store, cfg.CoreInstance, 100*time.Millisecond, feed, func(err error) {
			if err != nil {
				t.Errorf("hold poll failed: %v", err)
			}
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-watched
		_ = httpServer.Close()
	})
	rig := &hardInvalidationRig{server: snapshots, selector: dispatch.NewSelector(cfg.CoreInstance),
		snapshots:   dispatch.NewSnapshotClient(socket, "edge-a", hardInvalidationSecret, cfg.CoreInstance),
		invalidates: dispatch.NewInvalidationClient(socket, "edge-a", hardInvalidationSecret, cfg.CoreInstance)}
	t.Cleanup(rig.snapshots.Close)
	t.Cleanup(rig.invalidates.Close)
	return rig
}

func (r *hardInvalidationRig) publish(t *testing.T, published dispatch.PublishedSnapshot) {
	t.Helper()
	require.NoError(t, r.server.Publish(published))
	fetch, err := r.snapshots.Fetch(t.Context(), "", 0, 0)
	require.NoError(t, err)
	require.NoError(t, r.selector.UpdateEpoch(fetch.Envelope.PublishLineage, fetch.Envelope.Snapshot, time.Now()))
}

func (r *hardInvalidationRig) pull(t *testing.T, wait time.Duration) dispatch.InvalidationEnvelope {
	t.Helper()
	fetch, err := r.invalidates.Fetch(t.Context(), r.feed, r.sequence, wait)
	require.NoError(t, err)
	require.False(t, fetch.NotModified, "expected an invalidation update")
	_, err = r.selector.ApplyInvalidations(fetch.Envelope.PublishLineage, fetch.Envelope.Entries)
	require.NoError(t, err)
	r.feed, r.sequence = fetch.Envelope.Feed, fetch.Envelope.Sequence
	return fetch.Envelope
}

func (r *hardInvalidationRig) waitReady(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := r.invalidates.Fetch(t.Context(), "", 0, 0)
		return err == nil
	}, 3*time.Second, 20*time.Millisecond)
	r.pull(t, 0)
}

func (r *hardInvalidationRig) selected(route dispatch.Route, rounds int) map[string]int {
	refs := map[string]int{}
	for range rounds {
		refs[r.selector.Select(route, time.Now()).CandidateRef]++
	}
	return refs
}

func hardInvalidationConfig(t *testing.T, targets ...dispatch.Target) dispatch.Config {
	t.Helper()
	cfg := failureConfig(t, targets, false)
	cfg.SnapshotServer = &dispatch.SnapshotServerConfig{SocketPath: "/run/dispatchd/snapshot.sock", KeysFile: "unused.json",
		HardInvalidation: &dispatch.HardInvalidationConfig{PollIntervalMilliseconds: 100}}
	return cfg
}

var hardRoute = dispatch.Route{PoolRef: "pool", PolicyRevision: "policy-1", Model: "model", Region: "region"}

func TestDispatchHardInvalidationPropagatesWithinTTLAndConverges(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b := dispatchTarget("a", 1), dispatchTarget("b", 2)
	cfg := hardInvalidationConfig(t, a, b)
	first := lineageRound(t, store, cfg)
	rig := startHardInvalidationRig(t, store, cfg)
	rig.publish(t, first)
	rig.waitReady(t)
	require.Equal(t, map[string]int{"a": 2, "b": 2}, rig.selected(hardRoute, 4))

	ban := failureEvent(t, db, a, "ban-a", dispatch.FailureCredentialBanned)
	started := time.Now()
	inserted, err := store.Observe(t.Context(), "core", ban)
	require.NoError(t, err)
	require.True(t, inserted)
	envelope := rig.pull(t, 5*time.Second)
	require.Less(t, time.Since(started), 2*time.Second, "the hold reaches the edge without a new snapshot")
	require.Len(t, envelope.Entries, 1)
	entry := envelope.Entries[0]
	require.Equal(t, first.Lineage, entry.PublishLineage)
	require.Equal(t, first.Snapshot.Revision, entry.FloorRevision, "the floor is the latest committed snapshot revision")
	require.Equal(t, map[string]int{"b": 4}, rig.selected(hardRoute, 4), "only the banned credential is removed")
	require.True(t, time.Now().Before(first.Snapshot.ExpiresAt))

	duplicate, err := store.Observe(t.Context(), "core", ban)
	require.NoError(t, err)
	require.False(t, duplicate)
	fetch, err := rig.invalidates.Fetch(t.Context(), rig.feed, rig.sequence, 300*time.Millisecond)
	require.NoError(t, err)
	require.True(t, fetch.NotModified, "a duplicate event does not emit a new invalidation")

	second := lineageRound(t, store, cfg)
	require.Greater(t, second.Snapshot.Revision, entry.FloorRevision)
	rig.publish(t, second)
	entries, candidates := rig.selector.InvalidationCounts()
	require.Equal(t, [2]int{0, 0}, [2]int{entries, candidates}, "the next snapshot takes over")
	require.Equal(t, map[string]int{"b": 4}, rig.selected(hardRoute, 4), "the snapshot itself vetoes the hold")
}

func TestDispatchHardInvalidationLineageRotationAndRestoreIdentity(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b := dispatchTarget("a", 1), dispatchTarget("b", 2)
	cfg := hardInvalidationConfig(t, a, b)
	first := lineageRound(t, store, cfg)
	rig := startHardInvalidationRig(t, store, cfg)
	rig.publish(t, first)
	rig.waitReady(t)

	rotated, err := store.RotatePublishLineage(t.Context())
	require.NoError(t, err)
	envelope := rig.pull(t, 5*time.Second)
	require.Equal(t, rotated, envelope.PublishLineage)
	require.Empty(t, envelope.Entries)
	require.Equal(t, map[string]int{"a": 2, "b": 2}, rig.selected(hardRoute, 4), "a lineage change alone blocks nothing")

	_, err = store.Observe(t.Context(), "core", failureEvent(t, db, b, "ban-b", dispatch.FailureCredentialBanned))
	require.NoError(t, err)
	envelope = rig.pull(t, 5*time.Second)
	require.Equal(t, rotated, envelope.Entries[0].PublishLineage)
	require.Equal(t, map[string]int{"a": 4}, rig.selected(hardRoute, 4), "an entry from the new lineage blocks the edge still on the old one")

	next := lineageRound(t, store, cfg)
	require.Equal(t, rotated, next.Lineage)
	rig.publish(t, next)
	entries, _ := rig.selector.InvalidationCounts()
	require.Zero(t, entries)
	require.Equal(t, map[string]int{"a": 4}, rig.selected(hardRoute, 4))

	dispatchExec(t, db, `UPDATE dispatch.publish_lineage SET database_oid=database_oid+1`)
	state, err := store.HoldInvalidationState(t.Context(), "core")
	require.NoError(t, err)
	require.Empty(t, state.PublishLineage, "a lineage recorded for another database is not trusted")
	require.Empty(t, state.Holds)
}

func TestDispatchHardInvalidationPollDoesNotWaitForStoreLock(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := hardInvalidationConfig(t, dispatchTarget("a", 1))
	lineageRound(t, store, cfg)
	_, err := store.Observe(t.Context(), "core", failureEvent(t, db, dispatchTarget("a", 1), "ban-a", dispatch.FailureCredentialBanned))
	require.NoError(t, err)
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock($1)`, dispatchStoreLock)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	started := time.Now()
	state, err := store.HoldInvalidationState(ctx, "core")
	require.NoError(t, err)
	require.Less(t, time.Since(started), time.Second)
	require.Len(t, state.Holds, 1)
}

// 并发发布、触发与轮询下验证 floor 规则：同 lineage 中 revision 大于读到 floor 的快照必须已否决读到的每个 hold。
func TestDispatchHardInvalidationFloorHoldsUnderConcurrency(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	targets := make([]dispatch.Target, 12)
	for i := range targets {
		targets[i] = dispatchTarget(fmt.Sprintf("t%d", i), int64(i+1))
	}
	cfg := hardInvalidationConfig(t, targets...)
	lineageRound(t, store, cfg)
	var (
		mu        sync.Mutex
		published []dispatch.PublishedSnapshot
		states    []dispatch.HoldInvalidationState
		workers   sync.WaitGroup
	)
	done := make(chan struct{})
	workers.Go(func() {
		for range 8 {
			result, err := dispatch.RunRound(t.Context(), cfg, store, nil)
			if err != nil {
				t.Error(err)
				return
			}
			snapshot, _ := result.Published()
			mu.Lock()
			published = append(published, snapshot)
			mu.Unlock()
		}
	})
	events := make([]dispatch.Observation, len(targets))
	for i, target := range targets {
		events[i] = failureEvent(t, db, target, fmt.Sprintf("ban-%d", i), dispatch.FailureCredentialBanned)
	}
	workers.Go(func() {
		for _, event := range events {
			if _, err := store.Observe(t.Context(), "core", event); err != nil {
				t.Error(err)
				return
			}
		}
	})
	polled := make(chan struct{})
	go func() {
		defer close(polled)
		for {
			select {
			case <-done:
				return
			default:
			}
			state, err := store.HoldInvalidationState(context.Background(), "core")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			states = append(states, state)
			mu.Unlock()
		}
	}()
	workers.Wait()
	close(done)
	<-polled
	published = append(published, lineageRound(t, store, cfg))
	checked := 0
	for _, state := range states {
		for _, snapshot := range published {
			if snapshot.Lineage != state.PublishLineage || snapshot.Snapshot.Revision <= state.SnapshotRevision {
				continue
			}
			for _, candidate := range snapshot.Snapshot.Candidates {
				for _, hold := range state.Holds {
					if hold.Scope.CredentialRef == candidate.CredentialRef {
						require.Zero(t, candidate.EffectiveWeight, "snapshot %d after floor %d must veto %s",
							snapshot.Snapshot.Revision, state.SnapshotRevision, candidate.Ref)
						checked++
					}
				}
			}
		}
	}
	require.Positive(t, checked)
	require.NotEmpty(t, states)
}
