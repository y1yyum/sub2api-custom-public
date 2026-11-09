//go:build integration

package repository

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/custombridge/edge"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const strictTopologyInput = "isolated strict topology text"

// 只连接夹具创建的 PG/Redis；Core、Edge 和 relay 均运行实际命令入口。
func TestBridgeStrictTopology(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("实际二进制和 stdout 管道验收要求 Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	f := newBridgeTopology(t, ctx)
	f.build(t, "bridge-edge")
	f.core = f.startCore(t, true)
	f.relay = f.startRelay(t)
	strict := &bridgeStrictTopology{f: f, snapshotPath: filepath.Join(f.dir, "strict-snapshot.json"), committed: make(map[string]int64)}
	permits := []dispatch.Permit{strictTopologyPermit("A"), strictTopologyPermit("B")}

	t.Run("json_and_sse_override_native_sticky_B", func(t *testing.T) {
		body := strictTopologyBody(false)
		warmup := strict.request(ctx, f.coreURL, topologyCoreKey, "native-sticky-B", body)
		require.NoError(t, warmup.err)
		require.Equal(t, http.StatusOK, warmup.status, warmup.body)
		require.Contains(t, warmup.body, "provider-B")
		redisClient := redis.NewClient(&redis.Options{Addr: f.redisAddress})
		t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
		ginContext := &gin.Context{Request: &http.Request{Header: make(http.Header)}}
		sessionHash := new(service.OpenAIGatewayService).GenerateSessionHash(ginContext, []byte(body))
		require.NotEmpty(t, sessionHash)
		stickyKey := fmt.Sprintf("sticky_session:%d:openai:%s", topologyGroupID, sessionHash)
		sticky, err := redisClient.Get(ctx, stickyKey).Int64()
		require.NoError(t, err, "普通请求必须先真实建立 B 的原生粘性绑定")
		require.Equal(t, int64(topologyAccountB), sticky)

		strict.publish(t, 30*time.Second, strictTopologyCandidate("A"))
		strict.configure(t, permits)
		f.edge = f.startEdge(t)
		for _, stream := range []bool{false, true} {
			key := fmt.Sprintf("strict-baseline-%t", stream)
			result := strict.request(ctx, f.edgeURL, topologyEdgeKey, key, strictTopologyBody(stream))
			strict.accept(t, result, "A")
			if stream {
				require.Contains(t, result.contentType, "text/event-stream")
				require.Contains(t, result.body, "response.completed")
			} else {
				require.Contains(t, result.contentType, "application/json")
				require.True(t, json.Valid([]byte(result.body)))
			}
		}
		require.Equal(t, 2, f.providerCount("A"))
		require.Equal(t, 1, f.providerCount("B"), "B 只有普通入口的预热调用")
		strict.assertDrained(t)
		t.Log("actual_cmd_server strict_json_and_sse=A native_sticky=B strict_receipts=A-only")
	})
	if t.Failed() {
		return
	}

	t.Run("runtime_snapshot_switch_and_restart_preserve_idempotency", func(t *testing.T) {
		original := f.lastAttempt(t)
		pid := f.edge.cmd.Process.Pid
		strict.publish(t, 30*time.Second, strictTopologyCandidate("B"))
		sequence := 0
		bridgeTopologyEventually(t, ctx, func() error {
			sequence++
			result := strict.request(ctx, f.edgeURL, topologyEdgeKey, fmt.Sprintf("strict-switch-%d", sequence), strictTopologyBody(false))
			label := "A"
			if strings.Contains(result.body, "provider-B") {
				label = "B"
			}
			strict.accept(t, result, label)
			if label != "B" {
				return errors.New("等待同一 Edge 的后台快照刷新")
			}
			return nil
		})
		require.Equal(t, pid, f.edge.cmd.Process.Pid)
		beforeA, beforeB := f.providerCount("A"), f.providerCount("B")
		beforeAttempts := f.count(t, f.source, "bridge.forward_attempt")
		replay := func() {
			result := strict.request(ctx, f.edgeURL, topologyEdgeKey, "strict-baseline-true", strictTopologyBody(true))
			require.NoError(t, result.err)
			require.Equal(t, http.StatusConflict, result.status, result.body)
			require.Contains(t, result.body, "bridge_attempt_exists")
			require.Equal(t, beforeAttempts, f.count(t, f.source, "bridge.forward_attempt"))
			require.Equal(t, beforeA, f.providerCount("A"))
			require.Equal(t, beforeB, f.providerCount("B"))
			current, err := custombridge.NewForwardAttemptRepository(f.source).Get(ctx, topologyKeyID, original.Envelope.AttemptID)
			require.NoError(t, err)
			require.Equal(t, original.Envelope, current.Envelope)
			require.Equal(t, original.CoreRequestID, current.CoreRequestID)
			require.Equal(t, int64(topologyAccountA), current.Envelope.AccountID)
		}
		replay()
		stats := strict.stop(t)
		require.Equal(t, strict.revision, stats.SnapshotRevision)
		f.edge = f.startEdge(t)
		require.NotEqual(t, pid, f.edge.cmd.Process.Pid)
		replay()
		strict.accept(t, strict.request(ctx, f.edgeURL, topologyEdgeKey, "strict-fresh-B-sse", strictTopologyBody(true)), "B")
		strict.stop(t)
		strict.assertDrained(t)
		t.Logf("actual_bridge_edge old_pid=%d fresh_pid=%d snapshot_switch=A-to-B repeated_attempt_original_account=A provider_resends=0", pid, f.edge.cmd.Process.Pid)
	})
	if t.Failed() {
		return
	}

	t.Run("snapshot_metadata_cannot_expand_permits_or_route", func(t *testing.T) {
		cases := []struct {
			name   string
			mutate func(*dispatch.Candidate)
		}{
			{"candidate_ref", func(c *dispatch.Candidate) { c.Ref = "unpermitted-reference" }},
			{"account_id", func(c *dispatch.Candidate) { c.AccountID = topologyAccountB }},
			{"credential_ref", func(c *dispatch.Candidate) { c.CredentialRef = "different-credential" }},
			{"credential_revision", func(c *dispatch.Candidate) { c.CredentialRevision++ }},
			{"provider_ref", func(c *dispatch.Candidate) { c.ProviderRef = "different-provider" }},
			{"pool", func(c *dispatch.Candidate) { c.PoolRef = "different-pool" }},
			{"policy", func(c *dispatch.Candidate) { c.PolicyRevision = "different-policy" }},
			{"model", func(c *dispatch.Candidate) { c.Model = "different-model" }},
			{"region", func(c *dispatch.Candidate) { c.Region = "different-region" }},
		}
		for _, tc := range cases {
			if !t.Run(tc.name, func(t *testing.T) {
				candidate := strictTopologyCandidate("A")
				tc.mutate(&candidate)
				strict.publish(t, 30*time.Second, candidate)
				allowed := append([]dispatch.Permit(nil), permits...)
				if tc.name == "model" {
					allowed[0].Model = candidate.Model
				}
				strict.configure(t, allowed)
				f.edge = f.startEdge(t)
				strict.rejectWithoutForward(t, "strict-isolation-"+tc.name, http.StatusServiceUnavailable, strictTopologyBody(false))
				strict.stop(t)
			}) {
				return
			}
		}
		strict.assertDrained(t)
	})
	if t.Failed() {
		return
	}

	t.Run("strict_request_boundary_rejects_before_core", func(t *testing.T) {
		strict.publish(t, 30*time.Second, strictTopologyCandidate("A"))
		strict.configure(t, permits)
		f.edge = f.startEdge(t)
		for index, body := range []string{
			`{"model":"gpt-5.4","input":"text"}`,
			`{"model":"gpt-5.4","input":"text","max_output_tokens":0}`,
			`{"model":"gpt-5.4","input":"text","max_output_tokens":4097}`,
			`{"model":"gpt-5.4","input":{"type":"input_image"},"max_output_tokens":16}`,
			`{"model":"gpt-5.4","input":"text","max_output_tokens":16,"previous_response_id":"synthetic"}`,
		} {
			strict.rejectWithoutForward(t, fmt.Sprintf("strict-body-%d", index), http.StatusBadRequest, body)
		}
		strict.stop(t)
	})
	if t.Failed() {
		return
	}

	t.Run("control_plane_loss_expires_without_fixed_grant_fallback", func(t *testing.T) {
		strict.publish(t, 2*time.Second, strictTopologyCandidate("A"))
		strict.configure(t, permits)
		f.edge = f.startEdge(t)
		strict.accept(t, strict.request(ctx, f.edgeURL, topologyEdgeKey, "strict-before-expiry", strictTopologyBody(false)), "A")
		require.NoError(t, os.Remove(strict.snapshotPath))
		beforeA, beforeB := f.providerCount("A"), f.providerCount("B")
		beforeAttempts := f.count(t, f.source, "bridge.forward_attempt")
		bridgeTopologyEventually(t, ctx, func() error {
			result := strict.request(ctx, f.edgeURL, topologyEdgeKey, "strict-before-expiry", strictTopologyBody(false))
			require.NoError(t, result.err)
			if result.status == http.StatusConflict {
				return errors.New("等待最后有效快照自然到期")
			}
			require.Equal(t, http.StatusServiceUnavailable, result.status, result.body)
			return nil
		})
		require.Equal(t, beforeAttempts, f.count(t, f.source, "bridge.forward_attempt"))
		require.Equal(t, beforeA, f.providerCount("A"))
		require.Equal(t, beforeB, f.providerCount("B"))
		strict.rejectWithoutForward(t, "strict-expired-fresh", http.StatusServiceUnavailable, strictTopologyBody(false))
		stats := strict.stop(t)
		require.Positive(t, stats.RefreshFailures)
		require.Positive(t, stats.SnapshotExpired)
		f.edge = f.startEdge(t)
		strict.rejectWithoutForward(t, "strict-no-snapshot-after-restart", http.StatusServiceUnavailable, strictTopologyBody(false))
		stats = strict.stop(t)
		require.Positive(t, stats.SnapshotUnavailable)
		strict.assertDrained(t)
	})
	if t.Failed() {
		return
	}

	t.Run("stream_cancel_releases_actual_edge_local_slot", func(t *testing.T) {
		strict.publish(t, 30*time.Second, strictTopologyCandidate("A"))
		strict.configure(t, permits)
		f.edge = f.startEdge(t)
		pid := f.edge.cmd.Process.Pid
		gate := make(chan struct{})
		releaseGate := sync.OnceFunc(func() { close(gate) })
		defer releaseGate()
		f.mu.Lock()
		f.providerStreamGate = gate
		f.mu.Unlock()
		defer func() {
			f.mu.Lock()
			f.providerStreamGate = nil
			f.mu.Unlock()
		}()
		streamCtx, cancelStream := context.WithCancel(ctx)
		defer cancelStream()
		request := strict.newRequest(t, streamCtx, f.edgeURL, topologyEdgeKey, "strict-cancel-stream", strictTopologyBody(true))
		response, err := f.client.Do(request)
		require.NoError(t, err)
		defer func() { _ = response.Body.Close() }()
		require.Equal(t, http.StatusOK, response.StatusCode)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), `"delta":"provider-A"`) {
				break
			}
		}
		require.NoError(t, scanner.Err())
		require.Contains(t, scanner.Text(), "provider-A")
		canceledAttempt := f.lastAttempt(t)
		strict.rejectWithoutForward(t, "strict-occupied", http.StatusServiceUnavailable, strictTopologyBody(false))
		cancelStream()
		require.NoError(t, response.Body.Close())
		var afterCancel bridgeTopologyResponse
		bridgeTopologyEventually(t, ctx, func() error {
			result := strict.request(ctx, f.edgeURL, topologyEdgeKey, "strict-after-cancel", strictTopologyBody(false))
			if result.status == http.StatusServiceUnavailable {
				return errors.New("等待取消释放本地占位")
			}
			afterCancel = result
			return nil
		})
		require.Equal(t, pid, f.edge.cmd.Process.Pid)
		// 本地槽位先释放；Core 仍须排空供应商响应并保留取消请求的原生计费。
		strict.accept(t, afterCancel, "A")
		releaseGate()
		stats := strict.stop(t)
		require.Equal(t, uint64(2), stats.Selected)
		require.Equal(t, uint64(2), stats.Released)
		require.Zero(t, stats.Inflight)
		require.Positive(t, stats.NoEligibleRoute)
		// 退出 Core 后再读取持久事实，避免把尚在排空中的计费当成最终缺失。
		f.core.stop(t)
		current, err := custombridge.NewForwardAttemptRepository(f.source).Get(ctx, topologyKeyID, canceledAttempt.Envelope.AttemptID)
		require.NoError(t, err)
		require.Equal(t, canceledAttempt.Envelope, current.Envelope)
		require.Equal(t, canceledAttempt.CoreRequestID, current.CoreRequestID)
		require.True(t, current.SendIntentAt.Valid)
		receipt, err := custombridge.NewReceiptRepository(f.source).Get(ctx, canceledAttempt.CoreRequestID, topologyKeyID)
		if errors.Is(err, sql.ErrNoRows) {
			require.Equal(t, "reconciliation_required", current.State)
			require.Empty(t, current.ReceiptID)
			strict.unknownCanceled++
			t.Logf("canceled_attempt=%s state=%s canceled_receipt=Unknown automatic_resends=0", current.Envelope.AttemptID, current.State)
		} else {
			require.NoError(t, err)
			require.Equal(t, "core_committed", current.State)
			require.Equal(t, receipt.ID, current.ReceiptID)
			require.Equal(t, int64(topologyAccountA), receipt.AccountID)
			require.Equal(t, canceledAttempt.Envelope.AttemptID, receipt.AttemptID)
			require.Equal(t, canceledAttempt.Envelope.ReservationID, receipt.ReservationID)
			require.NotContains(t, strict.committed, receipt.AttemptID)
			strict.committed[receipt.AttemptID] = receipt.AccountID
			t.Logf("canceled_attempt=%s state=%s canceled_receipt=%s automatic_resends=0", current.Envelope.AttemptID, current.State, receipt.ID)
		}
		strict.assertDrained(t)
		t.Log("actual_bridge_edge strict_max_inflight=1 concurrent_request=503 client_stream_cancel=true next_request=200 inflight=0 selected=released=2")
	})
	if t.Failed() {
		return
	}

	t.Run("native_disabled_selected_A_cannot_retry_B", func(t *testing.T) {
		_, err := f.source.ExecContext(ctx, `UPDATE accounts SET schedulable=false WHERE id=$1`, topologyAccountA)
		require.NoError(t, err)
		f.core = f.startCore(t, true)
		strict.publish(t, 30*time.Second, strictTopologyCandidate("A"))
		strict.configure(t, permits)
		f.edge = f.startEdge(t)
		beforeA, beforeB := f.providerCount("A"), f.providerCount("B")
		beforeReceipts := f.count(t, f.source, "bridge.receipt_outbox")
		beforeAttempts := f.count(t, f.source, "bridge.forward_attempt")
		replay := strict.request(ctx, f.edgeURL, topologyEdgeKey, "strict-cancel-stream", strictTopologyBody(true))
		require.NoError(t, replay.err)
		require.Equal(t, http.StatusConflict, replay.status, replay.body)
		require.Contains(t, replay.body, "bridge_attempt_exists")
		result := strict.request(ctx, f.edgeURL, topologyEdgeKey, "strict-native-veto", strictTopologyBody(false))
		require.NoError(t, result.err)
		require.GreaterOrEqual(t, result.status, 400, result.body)
		require.LessOrEqual(t, result.status, 599, result.body)
		require.Equal(t, beforeAttempts+1, f.count(t, f.source, "bridge.forward_attempt"))
		attempt := f.lastAttempt(t)
		require.Equal(t, int64(topologyAccountA), attempt.Envelope.AccountID)
		require.Equal(t, "rejected", attempt.State)
		require.False(t, attempt.SendIntentAt.Valid)
		require.Equal(t, beforeReceipts, f.count(t, f.source, "bridge.receipt_outbox"))
		require.Equal(t, beforeA, f.providerCount("A"))
		require.Equal(t, beforeB, f.providerCount("B"))
		stats := strict.stop(t)
		require.Equal(t, uint64(2), stats.Selected)
		require.Equal(t, uint64(2), stats.Released)
		strict.assertDrained(t)
		t.Logf("actual_cmd_server native_schedulable=false selected_account=A status=%d provider_A_added=0 provider_B_added=0", result.status)
	})
}

type bridgeStrictTopology struct {
	f               *bridgeTopology
	snapshotPath    string
	revision        int64
	committed       map[string]int64
	unknownCanceled int
}

func strictTopologyCandidate(label string) dispatch.Candidate {
	account := int64(topologyAccountA)
	if label == "B" {
		account = topologyAccountB
	}
	return dispatch.Candidate{Target: dispatch.Target{
		Ref: "strict-" + label, AccountID: account, CredentialRef: "synthetic-credential-" + label, CredentialRevision: 1,
		ProviderRef: "synthetic-provider-" + label, PoolRef: "topology-pool", PolicyRevision: "p1", Model: "gpt-5.4", Region: "fixture-region",
		BaseWeight: 100, MaxInflight: 1, Enabled: true,
	}, State: dispatch.Healthy, EffectiveWeight: 100, Reason: "synthetic"}
}

func strictTopologyPermit(label string) dispatch.Permit {
	c := strictTopologyCandidate(label)
	return dispatch.Permit{CandidateRef: c.Ref, AccountID: c.AccountID, CredentialRef: c.CredentialRef, CredentialRevision: c.CredentialRevision,
		ProviderRef: c.ProviderRef, Model: c.Model, MaxInflight: c.MaxInflight}
}

func (s *bridgeStrictTopology) publish(t *testing.T, ttl time.Duration, candidates ...dispatch.Candidate) {
	t.Helper()
	s.revision++
	now := time.Now()
	require.NoError(t, dispatch.WriteSnapshot(s.snapshotPath, dispatch.Snapshot{
		Version: dispatch.SnapshotVersion, CoreInstance: topologySource, Revision: s.revision,
		GeneratedAt: now, ExpiresAt: now.Add(ttl), Candidates: candidates,
	}, now))
}

func (s *bridgeStrictTopology) configure(t *testing.T, permits []dispatch.Permit) {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join(s.f.dir, "edge.json"))
	require.NoError(t, err)
	var cfg edge.Config
	require.NoError(t, json.Unmarshal(encoded, &cfg))
	cfg.StrictDispatch = &edge.ShadowDispatchConfig{SnapshotFile: s.snapshotPath, CoreInstance: topologySource, PollIntervalMilliseconds: 10}
	// 固定旧 grant 故意指向 B；strict 不可用时回退会被供应商计数抓到。
	cfg.Grants[0].AccountID = topologyAccountB
	cfg.Grants[0].Region = "fixture-region"
	cfg.Grants[0].StrictTargets = append([]dispatch.Permit(nil), permits...)
	s.f.writeJSON(t, "edge.json", cfg)
}

func strictTopologyBody(stream bool) string {
	return fmt.Sprintf(`{"model":"gpt-5.4","input":%q,"max_output_tokens":16,"stream":%t}`, strictTopologyInput, stream)
}

func (s *bridgeStrictTopology) newRequest(t *testing.T, ctx context.Context, origin, token, key, body string) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/v1/responses", strings.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	return request
}

func (s *bridgeStrictTopology) request(ctx context.Context, origin, token, key, body string) bridgeTopologyResponse {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/v1/responses", strings.NewReader(body))
	if err != nil {
		return bridgeTopologyResponse{err: err}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response, err := s.f.client.Do(request)
	if err != nil {
		return bridgeTopologyResponse{err: err}
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return bridgeTopologyResponse{response.StatusCode, string(encoded), response.Header.Get("Content-Type"), err}
}

func (s *bridgeStrictTopology) accept(t *testing.T, result bridgeTopologyResponse, label string) {
	t.Helper()
	require.NoError(t, result.err)
	require.Equal(t, http.StatusOK, result.status, result.body)
	require.Contains(t, result.body, "provider-"+label)
	other := "A"
	if label == "A" {
		other = "B"
	}
	require.NotContains(t, result.body, "provider-"+other)
	attempt := s.f.lastAttempt(t)
	require.Equal(t, strictTopologyCandidate(label).AccountID, attempt.Envelope.AccountID)
	var receipt *custombridge.Receipt
	bridgeTopologyEventually(t, s.f.ctx, func() error {
		var err error
		receipt, err = custombridge.NewReceiptRepository(s.f.source).Get(s.f.ctx, attempt.CoreRequestID, topologyKeyID)
		return err
	})
	current, err := custombridge.NewForwardAttemptRepository(s.f.source).Get(s.f.ctx, topologyKeyID, attempt.Envelope.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "core_committed", current.State)
	require.Equal(t, attempt.Envelope.AccountID, receipt.AccountID)
	require.Equal(t, attempt.Envelope.AttemptID, receipt.AttemptID)
	require.Equal(t, attempt.Envelope.ReservationID, receipt.ReservationID)
	require.Equal(t, "gpt-5.4", receipt.Billing.Model)
	require.NotContains(t, s.committed, receipt.AttemptID)
	s.committed[receipt.AttemptID] = receipt.AccountID
}

func (s *bridgeStrictTopology) rejectWithoutForward(t *testing.T, key string, status int, body string) {
	t.Helper()
	f := s.f
	beforeA, beforeB := f.providerCount("A"), f.providerCount("B")
	beforeAttempts := f.count(t, f.source, "bridge.forward_attempt")
	beforeReceipts := f.count(t, f.source, "bridge.receipt_outbox")
	result := s.request(f.ctx, f.edgeURL, topologyEdgeKey, key, body)
	require.NoError(t, result.err)
	require.Equal(t, status, result.status, result.body)
	if status == http.StatusServiceUnavailable {
		require.Contains(t, result.body, "strict_dispatch_unavailable")
	}
	require.Equal(t, beforeA, f.providerCount("A"))
	require.Equal(t, beforeB, f.providerCount("B"))
	require.Equal(t, beforeAttempts, f.count(t, f.source, "bridge.forward_attempt"))
	require.Equal(t, beforeReceipts, f.count(t, f.source, "bridge.receipt_outbox"))
}

func (s *bridgeStrictTopology) stop(t *testing.T) edge.StrictStats {
	t.Helper()
	p := s.f.edge
	p.stop(t)
	encoded, err := os.ReadFile(p.logPath)
	require.NoError(t, err)
	scanner := bufio.NewScanner(strings.NewReader(string(encoded)))
	var final edge.StrictStats
	startup, shutdown := false, false
	for scanner.Scan() {
		var record struct {
			Type  string           `json:"type"`
			Phase string           `json:"phase"`
			Stats edge.StrictStats `json:"stats"`
		}
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record), "实际 stdout 必须是完整 JSONL")
		require.Equal(t, "bridge_edge_strict_stats", record.Type)
		require.True(t, record.Stats.Enabled)
		startup = startup || record.Phase == "startup"
		if record.Phase == "shutdown" {
			shutdown, final = true, record.Stats
		}
	}
	require.NoError(t, scanner.Err())
	require.True(t, startup)
	require.True(t, shutdown)
	require.Zero(t, final.Inflight)
	require.Equal(t, final.Selected, final.Released)
	return final
}

func (s *bridgeStrictTopology) assertDrained(t *testing.T) {
	t.Helper()
	f := s.f
	receipts := len(s.committed)
	f.waitDrained(t, receipts)
	require.Equal(t, receipts+1+s.unknownCanceled, f.providerCount("A")+f.providerCount("B"), "仅有已核对回执、取消结果 Unknown 和一次普通 B 预热调用")
	require.Equal(t, receipts+1, f.count(t, f.source, "usage_billing_dedup"), "另有一次普通 B 预热扣账")
	var unmatched int
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM bridge.receipt_outbox r
		LEFT JOIN bridge.forward_attempt a ON a.attempt_id=r.attempt_id AND a.internal_key_id=r.internal_key_id
		LEFT JOIN usage_billing_dedup d ON d.request_id=r.core_request_id AND d.api_key_id=r.internal_key_id AND d.request_fingerprint=r.request_fingerprint
		WHERE a.account_id IS DISTINCT FROM r.account_id OR a.reservation_id IS DISTINCT FROM r.reservation_id OR d.id IS NULL
		OR r.user_id<>701 OR r.internal_key_id<>702 OR r.billing->>'model'<>'gpt-5.4'`).Scan(&unmatched))
	require.Zero(t, unmatched)
	for _, label := range []string{"A", "B"} {
		accountID := strictTopologyCandidate(label).AccountID
		var billed, receipts float64
		require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COALESCE((extra->>'quota_used')::numeric,0) FROM accounts WHERE id=$1`, accountID).Scan(&billed))
		require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COALESCE(SUM((billing->>'account_quota_cost')::numeric),0) FROM bridge.receipt_outbox WHERE account_id=$1`, accountID).Scan(&receipts))
		if label == "B" {
			receipts += 0.007
		}
		require.InDelta(t, receipts, billed, 1e-8)
	}
	rows, err := f.sink.QueryContext(f.ctx, `SELECT event_id,receipt_raw,payload_digest FROM bridge_shadow.shadow_receipt_ledger WHERE source=$1`, topologySource)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	delivered := make(map[string]int64)
	for rows.Next() {
		var id, raw, digest string
		require.NoError(t, rows.Scan(&id, &raw, &digest))
		var shadow custombridge.Receipt
		require.NoError(t, json.Unmarshal([]byte(raw), &shadow))
		require.NotContains(t, delivered, shadow.AttemptID, "一个 attempt 不能产生重复影子效果")
		delivered[shadow.AttemptID] = shadow.AccountID
		receipt, err := custombridge.NewReceiptRepository(f.source).Get(f.ctx, shadow.CoreRequestID, shadow.InternalKeyID)
		require.NoError(t, err)
		encoded, err := json.Marshal(receipt)
		require.NoError(t, err)
		normalizedShadow, err := json.Marshal(shadow)
		require.NoError(t, err)
		require.JSONEq(t, string(encoded), string(normalizedShadow))
		require.Equal(t, id, receipt.ID)
		hash := sha256.Sum256([]byte(raw))
		require.Equal(t, hex.EncodeToString(hash[:]), digest)
		var inbox int
		require.NoError(t, f.sink.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM bridge_shadow.inbox WHERE source=$1 AND event_id=$2 AND payload_digest=$3`, topologySource, id, digest).Scan(&inbox))
		require.Equal(t, 1, inbox)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, s.committed, delivered, "所有已确认 attempt 的 outbox、ACK、inbox 与 ledger 必须一一对应")
	require.Zero(t, f.count(t, f.sink, "bridge_shadow.inbox_conflicts"))
	t.Logf("strict_receipts=%d canceled_unknown=%d native_B_warmup=1 A_calls=%d B_calls=%d relay_pending_or_leased_or_dead_letter=0", receipts, s.unknownCanceled, f.providerCount("A"), f.providerCount("B"))
}
