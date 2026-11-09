//go:build integration

package repository

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 所有业务请求均经过真实 Core/Edge 命令；只读来源故障不切断计费和 relay。
func TestBridgeObservationTopology(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("真实二进制和 Redis 原生并发拓扑要求 Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	s := newBridgeObservationTopology(t, ctx)
	f := s.f
	targetA, targetB := strictTopologyCandidate("A").Target, strictTopologyCandidate("B").Target
	targetA.MaxInflight, targetB.MaxInflight = 2, 2
	related := targetA
	related.Ref, related.PoolRef = "observation-related-A", "related-pool"
	targets := []dispatch.Target{targetA, related, targetB}
	_, err := f.source.ExecContext(ctx, `UPDATE accounts SET schedulable=false WHERE id=705`)
	require.NoError(t, err)
	s.configureProducer(t, 1, targets)
	f.core = s.startCore(t)
	f.relay = f.startRelay(t)
	t.Logf("actual_cmd_bridge_relay pid=%d receipt_source=%s", f.relay.cmd.Process.Pid, topologySource)
	s.configureReaders(t)
	s.configureDispatch(t, 1, targets)
	initial := s.round(t, true)
	require.Zero(t, observationTopologyCandidate(t, initial, targetA.Ref).Active)
	strict := &bridgeStrictTopology{f: f, snapshotPath: s.snapshotPath}
	if !t.Run("native_slot_ttl_mismatch_preserves_snapshot", func(t *testing.T) {
		s.configureDispatch(t, 1, targets)
		encoded, err := os.ReadFile(s.configPath)
		require.NoError(t, err)
		var cfg map[string]any
		require.NoError(t, json.Unmarshal(encoded, &cfg))
		cfg["telemetry"].(map[string]any)["slot_ttl_seconds"] = 1800
		f.writeJSON(t, filepath.Base(s.configPath), cfg)
		s.round(t, false)
		s.configureDispatch(t, 1, targets)
		s.round(t, true)
	}) {
		return
	}

	if !t.Run("native_regular_and_live_load_fail_closed_then_release", func(t *testing.T) {
		gate := make(chan struct{})
		release := sync.OnceFunc(func() { close(gate) })
		defer release()
		f.mu.Lock()
		f.providerStreamGate = gate
		f.mu.Unlock()
		defer func() { f.mu.Lock(); f.providerStreamGate = nil; f.mu.Unlock() }()
		request := strict.newRequest(t, ctx, f.coreURL, topologyCoreKey, "observation-native-stream", strictTopologyBody(true))
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
		require.Contains(t, scanner.Text(), "provider-A", "普通请求须已到实际 Provider 并仍占用原生槽位")
		cache := NewConcurrencyCache(s.redis, 15, 30).(*concurrencyCache)
		acquired, err := cache.AcquireLiveLease(ctx, topologyAccountA, 16, topologyUserID, 16, topologyKeyID, "observation-native-live", false)
		require.NoError(t, err)
		require.True(t, acquired)
		defer func() {
			require.NoError(t, cache.ReleaseLiveLease(ctx, topologyAccountA, topologyUserID, topologyKeyID, "observation-native-live"))
		}()
		loaded := s.round(t, true)
		require.Equal(t, 2, observationTopologyCandidate(t, loaded, targetA.Ref).Active)
		require.Equal(t, 2, observationTopologyCandidate(t, loaded, related.Ref).Active, "同账号不同路由必须共享真实负载")
		permit := strictTopologyPermit("A")
		permit.MaxInflight = 2
		strict.configure(t, []dispatch.Permit{permit})
		s.requireLiveLoad(t)
		f.edge = f.startEdge(t)
		t.Logf("actual_cmd_bridge_edge strict_pid=%d require_live_load=true", f.edge.cmd.Process.Pid)
		strict.rejectWithoutForward(t, "observation-full", http.StatusServiceUnavailable, strictTopologyBody(false))
		for _, failure := range []struct {
			name  string
			proxy *bridgeObservationProxy
		}{
			{"core_observation_database", s.coreProxy}, {"native_redis", s.redisProxy},
		} {
			if !t.Run(failure.name, func(t *testing.T) {
				loaded = s.round(t, true)
				require.Equal(t, 2, observationTopologyCandidate(t, loaded, targetA.Ref).Active)
				failure.proxy.block(true)
				defer failure.proxy.block(false)
				s.round(t, false)
				bridgeTopologyEventually(t, ctx, func() error {
					if time.Now().Before(loaded.ExpiresAt) {
						return fmt.Errorf("等待旧真实负载快照自然到期")
					}
					return nil
				})
				strict.rejectWithoutForward(t, "observation-expired-"+failure.name, http.StatusServiceUnavailable, strictTopologyBody(false))
			}) {
				return
			}
		}
		require.NoError(t, cache.ReleaseLiveLease(ctx, topologyAccountA, topologyUserID, topologyKeyID, "observation-native-live"))
		release()
		remaining, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Contains(t, string(remaining), "response.completed")
		require.NoError(t, response.Body.Close())
		bridgeTopologyEventually(t, ctx, func() error {
			active, err := cache.GetAccountConcurrency(ctx, topologyAccountA)
			if err != nil {
				return err
			}
			if active != 0 {
				return fmt.Errorf("等待原生槽位释放: %d", active)
			}
			return nil
		})
		free := s.round(t, true)
		require.Zero(t, observationTopologyCandidate(t, free, targetA.Ref).Active)
		bridgeTopologyEventually(t, ctx, func() error {
			result := strict.request(ctx, f.edgeURL, topologyEdgeKey, "observation-after-release", strictTopologyBody(false))
			if result.status == http.StatusServiceUnavailable {
				return fmt.Errorf("等待同一 Edge 刷新空闲负载")
			}
			require.NoError(t, result.err)
			require.Equal(t, http.StatusOK, result.status, result.body)
			require.Contains(t, result.body, "provider-A")
			return nil
		})
		stats := strict.stop(t)
		require.Equal(t, uint64(1), stats.Selected)
		require.Equal(t, stats.Selected, stats.Released)
		require.Zero(t, stats.Inflight)
		s.configureFixedEdge(t, topologyAccountA)
		f.edge = f.startEdge(t)
		t.Logf("actual_cmd_bridge_edge fixed_pid=%d", f.edge.cmd.Process.Pid)
		t.Log("actual_regular_SSE=1 native_repository_live_lease=1 snapshot_active=2 strict_full=503 outbound_added=0 source_loss_preserves_file=true expired_admission=false released_active=0")
	}) {
		return
	}

	if !t.Run("untrusted_local_and_unknown_errors_do_not_trip", func(t *testing.T) {
		before := s.attributedFailures(t)
		calls := f.providerCount("A")
		events := f.count(t, f.source, "bridge.dispatch_observation")
		attempts := f.count(t, f.source, "bridge.forward_attempt")
		result := strict.request(ctx, f.coreURL, "synthetic-invalid-public-key", "observation-public-401", strictTopologyBody(false))
		require.NoError(t, result.err)
		require.Equal(t, http.StatusUnauthorized, result.status, result.body)
		require.Equal(t, calls, f.providerCount("A"))
		require.Equal(t, events, f.count(t, f.source, "bridge.dispatch_observation"), "普通公开入口错误不得采样为被动事件")
		require.Equal(t, attempts, f.count(t, f.source, "bridge.forward_attempt"))
		f.core.stop(t)
		_, err := f.source.ExecContext(ctx, `UPDATE accounts SET schedulable=false WHERE id=704`)
		require.NoError(t, err)
		f.core = s.startCore(t)
		result = strict.request(ctx, f.edgeURL, topologyEdgeKey, "observation-local-rejection", strictTopologyBody(false))
		require.NoError(t, result.err)
		require.Equal(t, http.StatusServiceUnavailable, result.status, result.body)
		require.Equal(t, calls, f.providerCount("A"))
		localAttempt := f.lastAttempt(t)
		require.Equal(t, "rejected", localAttempt.State)
		require.False(t, localAttempt.SendIntentAt.Valid)
		f.core.stop(t)
		_, err = f.source.ExecContext(ctx, `UPDATE accounts SET schedulable=true WHERE id=704`)
		require.NoError(t, err)
		f.core = s.startCore(t)
		s.setProvider(0, "", true)
		result = strict.request(ctx, f.edgeURL, topologyEdgeKey, "observation-transport-unknown", strictTopologyBody(false))
		require.NoError(t, result.err)
		require.GreaterOrEqual(t, result.status, 400, result.body)
		require.Equal(t, calls+1, f.providerCount("A"), "transport 结果未知时不能自动重试业务请求")
		unknownAttempt := f.lastAttempt(t)
		s.setProvider(0, "", false)
		bridgeTopologyEventually(t, ctx, func() error {
			var count int
			if err := f.source.QueryRowContext(ctx, `SELECT COUNT(*) FROM bridge.dispatch_observation WHERE attempt_id=$1 AND outcome='unknown' AND NOT provider_evidence`, unknownAttempt.Envelope.AttemptID).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("等待实际 transport unknown 观测: %d", count)
			}
			return nil
		})
		require.Equal(t, before, s.attributedFailures(t))
		snapshot := s.round(t, true)
		require.Equal(t, dispatch.Healthy, observationTopologyCandidate(t, snapshot, targetA.Ref).State)
		beforeCalls := f.providerCount("A")
		oldPID := f.core.kill(t)
		f.core = s.startCore(t)
		require.NotEqual(t, oldPID, f.core.cmd.Process.Pid)
		result = strict.request(ctx, f.edgeURL, topologyEdgeKey, "observation-transport-unknown", strictTopologyBody(false))
		require.NoError(t, result.err)
		require.Equal(t, http.StatusConflict, result.status, result.body)
		require.Equal(t, beforeCalls, f.providerCount("A"), "Core 重启不能重发结果未知的业务请求")
		s.round(t, true)
		t.Log("public_401=untrusted native_rejection=local transport_unknown=not_provider_failure restart_provider_resends=0")
	}) {
		return
	}
	if !t.Run("client_cancel_does_not_attribute_provider_failure", func(t *testing.T) {
		before := s.attributedFailures(t)
		gate := make(chan struct{})
		release := sync.OnceFunc(func() { close(gate) })
		defer release()
		f.mu.Lock()
		f.providerStreamGate = gate
		f.mu.Unlock()
		defer func() { f.mu.Lock(); f.providerStreamGate = nil; f.mu.Unlock() }()
		streamCtx, cancelStream := context.WithCancel(ctx)
		defer cancelStream()
		request := strict.newRequest(t, streamCtx, f.edgeURL, topologyEdgeKey, "observation-client-cancel", strictTopologyBody(true))
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
		attempt := f.lastAttempt(t)
		cancelStream()
		require.NoError(t, response.Body.Close())
		release()
		bridgeTopologyEventually(t, ctx, func() error {
			var outcome string
			var evidence bool
			if err := f.source.QueryRowContext(ctx, `SELECT outcome,provider_evidence FROM bridge.dispatch_observation WHERE attempt_id=$1`, attempt.Envelope.AttemptID).Scan(&outcome, &evidence); err != nil {
				return err
			}
			// Core 可能先完成排空与扣账，再观察到取消；成功仍须有同一 attempt 的真实回执。
			require.Contains(t, []string{"client_canceled", "unknown", "success"}, outcome)
			if outcome == "success" {
				require.True(t, evidence)
				receipt, err := custombridge.NewReceiptRepository(f.source).Get(ctx, attempt.CoreRequestID, topologyKeyID)
				require.NoError(t, err)
				require.Equal(t, attempt.Envelope.AttemptID, receipt.AttemptID)
				require.Equal(t, int64(topologyAccountA), receipt.AccountID)
			} else {
				require.False(t, evidence)
			}
			t.Logf("actual_client_cancel_outcome=%s provider_evidence=%t", outcome, evidence)
			return nil
		})
		require.Equal(t, before, s.attributedFailures(t))
		snapshot := s.round(t, true)
		require.Equal(t, dispatch.Healthy, observationTopologyCandidate(t, snapshot, targetA.Ref).State)
		t.Log("actual_SSE_client_cancel=not_provider_failure canceled_billing_facts_retained=true")
	}) {
		return
	}
	if !t.Run("mismatched_actual_credential_digest_stays_unknown", func(t *testing.T) {
		before := s.attributedFailures(t)
		f.core.stop(t)
		_, err := f.source.ExecContext(ctx, `UPDATE accounts SET credentials=credentials || '{"synthetic_rotation_marker":"rotated"}'::jsonb WHERE id=704`)
		require.NoError(t, err)
		f.core = s.startCore(t)
		result := strict.request(ctx, f.edgeURL, topologyEdgeKey, "observation-credential-mismatch", strictTopologyBody(false))
		require.NoError(t, result.err)
		require.Equal(t, http.StatusOK, result.status, result.body)
		attempt := f.lastAttempt(t)
		bridgeTopologyEventually(t, ctx, func() error {
			var count int
			if err := f.source.QueryRowContext(ctx, `SELECT COUNT(*) FROM bridge.dispatch_observation WHERE attempt_id=$1 AND outcome='unknown' AND NOT provider_evidence`, attempt.Envelope.AttemptID).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("等待凭据指纹失配的 unknown 事件: %d", count)
			}
			return nil
		})
		require.Equal(t, before, s.attributedFailures(t))
		snapshot := s.round(t, true)
		require.Equal(t, dispatch.Healthy, observationTopologyCandidate(t, snapshot, targetA.Ref).State)
		f.core.stop(t)
		_, err = f.source.ExecContext(ctx, `UPDATE accounts SET credentials=credentials-'synthetic_rotation_marker' WHERE id=704`)
		require.NoError(t, err)
		f.core = s.startCore(t)
	}) {
		return
	}

	if !t.Run("five_actual_provider_failures_trip_and_import_is_idempotent", func(t *testing.T) {
		s.setProvider(http.StatusInternalServerError, "", false)
		defer s.setProvider(0, "", false)
		beforeCalls := f.providerCount("A")
		for i := range 5 {
			// 原生 Core 有进程内模型冷却；逐次重启清除此独立状态，保留真实事件供 dispatch 累积。
			if i > 0 {
				f.core.stop(t)
				f.core = s.startCore(t)
			}
			result := strict.request(ctx, f.edgeURL, topologyEdgeKey, fmt.Sprintf("observation-5xx-%d", i), strictTopologyBody(false))
			require.NoError(t, result.err)
			require.GreaterOrEqual(t, result.status, 400, result.body)
			require.Equal(t, beforeCalls+i+1, f.providerCount("A"), "每次受信业务请求恰好实际出站一次")
		}
		bridgeTopologyEventually(t, ctx, func() error {
			var count int
			if err := f.source.QueryRowContext(ctx, `SELECT COUNT(*) FROM bridge.dispatch_observation WHERE outcome='upstream_failure' AND provider_evidence`).Scan(&count); err != nil {
				return err
			}
			if count != 5 {
				return fmt.Errorf("等待五条实际 Provider 5xx: %d", count)
			}
			return nil
		})
		snapshot := s.round(t, true)
		require.Equal(t, dispatch.Tripped, observationTopologyCandidate(t, snapshot, targetA.Ref).State)
		require.Equal(t, dispatch.Healthy, observationTopologyCandidate(t, snapshot, targetB.Ref).State)
		count := f.count(t, s.db, "dispatch.observations")
		before := s.health(t, targetA.Ref)
		s.round(t, true)
		require.Equal(t, count, f.count(t, s.db, "dispatch.observations"), "每轮使用新 dispatchd PID，历史事件不得重复入账")
		require.Equal(t, before, s.health(t, targetA.Ref), "重启导入不能累计第二次健康失败")
		t.Log("actual_provider_5xx_events=5 health=tripped separate_dispatchd_process_imports=2 duplicate_health_effects=0")
	}) {
		return
	}

	if !t.Run("provider_429_retry_after_and_credential_401_isolation", func(t *testing.T) {
		for i := range targets {
			if targets[i].CredentialRef == targetA.CredentialRef {
				targets[i].CredentialRevision = 2
			}
		}
		f.core.stop(t)
		s.configureDispatch(t, 2, targets)
		s.round(t, false)
		s.configureProducer(t, 2, targets)
		f.core = s.startCore(t)
		s.configureDispatch(t, 2, targets)
		snapshot := s.round(t, true)
		require.Equal(t, "credential_rotated", observationTopologyCandidate(t, snapshot, targetA.Ref).Reason)
		rotated := s.health(t, targetA.Ref)
		require.Equal(t, int64(2), rotated.CredentialRevision)
		require.Zero(t, rotated.ConsecutiveFailures, "旧凭据/配置的 5xx 不能污染新代次")
		require.Zero(t, rotated.Failures)
		require.False(t, rotated.AuthBlocked)
		s.round(t, true)
		require.Equal(t, rotated, s.health(t, targetA.Ref), "重复读取旧事件不能改变新代次的待探活状态")
		s.setProvider(http.StatusTooManyRequests, "120", false)
		result := strict.request(ctx, f.edgeURL, topologyEdgeKey, "obs429", strictTopologyBody(false))
		require.NoError(t, result.err)
		require.GreaterOrEqual(t, result.status, 400, result.body)
		s.waitOutcome(t, "upstream_rate_limit", 2, 1)
		snapshot = s.round(t, true)
		require.Equal(t, "rate_limited", observationTopologyCandidate(t, snapshot, targetA.Ref).Reason)
		health := s.health(t, targetA.Ref)
		require.True(t, health.CoolUntil.After(time.Now().Add(105*time.Second)), "实际 Provider Retry-After=120 必须进入健康冷却")
		require.Equal(t, dispatch.Healthy, observationTopologyCandidate(t, snapshot, targetB.Ref).State)
		f.core.stop(t)
		// 清除上一场景在这个合成账号上留下的原生 429 冷却，使下一场景真正出站。
		_, err := f.source.ExecContext(ctx, `UPDATE accounts SET rate_limited_at=NULL,rate_limit_reset_at=NULL,overload_until=NULL,temp_unschedulable_until=NULL,temp_unschedulable_reason=NULL WHERE id=704`)
		require.NoError(t, err)
		for i := range targets {
			if targets[i].CredentialRef == targetA.CredentialRef {
				targets[i].CredentialRevision = 3
			}
		}
		s.configureProducer(t, 3, targets)
		f.core = s.startCore(t)
		s.configureDispatch(t, 3, targets)
		snapshot = s.round(t, true)
		require.Equal(t, "credential_rotated", observationTopologyCandidate(t, snapshot, targetA.Ref).Reason)
		require.False(t, s.health(t, targetA.Ref).AuthBlocked)
		s.setProvider(http.StatusUnauthorized, "", false)
		result = strict.request(ctx, f.edgeURL, topologyEdgeKey, "observation-provider-401", strictTopologyBody(false))
		require.NoError(t, result.err)
		require.GreaterOrEqual(t, result.status, 400, result.body)
		s.waitOutcome(t, "upstream_auth", 3, 1)
		snapshot = s.round(t, true)
		for _, ref := range []string{targetA.Ref, related.Ref} {
			require.Equal(t, "credential_rejected", observationTopologyCandidate(t, snapshot, ref).Reason)
			require.True(t, s.health(t, ref).AuthBlocked)
		}
		require.Equal(t, dispatch.Healthy, observationTopologyCandidate(t, snapshot, targetB.Ref).State)
		s.setProvider(0, "", false)
		t.Log("actual_provider_429_retry_after=120 same_credential_revision_401=isolated unrelated_credential=healthy stale_previous_revision=ignored")
	}) {
		return
	}

	f.edge.stop(t)
	f.core.stop(t)
	s.assertReceipts(t)
	t.Log("仅合成凭据；实际 WebSocket live 会话、长时间窗口恢复与跨主机故障=Unknown；live 使用原生 repository lease API，非手工 ZADD")
}

type bridgeObservationTopology struct {
	f                  *bridgeTopology
	db                 *sql.DB
	dsnFile            string
	coreReaderFile     string
	redisReaderFile    string
	snapshotPath       string
	configPath         string
	redis              *redis.Client
	coreProxy          *bridgeObservationProxy
	redisProxy         *bridgeObservationProxy
	mu                 sync.Mutex
	providerStatus     int
	providerRetryAfter string
	providerDisconnect bool
}

func newBridgeObservationTopology(t *testing.T, ctx context.Context) *bridgeObservationTopology {
	t.Helper()
	f := newBridgeTopology(t, ctx)
	configuration, err := os.ReadFile(filepath.Join(f.dir, "config.yaml"))
	require.NoError(t, err)
	configuration = append(configuration, []byte("\ngateway:\n  concurrency_slot_ttl_minutes: 15\n")...)
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "config.yaml"), configuration, 0600))
	s := &bridgeObservationTopology{f: f, snapshotPath: filepath.Join(f.dir, "observation-snapshot.json"), configPath: filepath.Join(f.dir, "observation-dispatch.json")}
	f.build(t, "bridge-edge")
	f.build(t, "dispatchd")
	s.db, s.dsnFile = bridgeTopologyDatabase(t, ctx, "observation_dispatch")
	require.NoError(t, dispatch.ApplySchema(ctx, s.db))
	dsn := s.dsnFile
	s.dsnFile = filepath.Join(f.dir, "observation-dispatch-dsn")
	require.NoError(t, os.WriteFile(s.dsnFile, []byte(dsn), 0600))
	s.redis = redis.NewClient(&redis.Options{Addr: f.redisAddress})
	t.Cleanup(func() { require.NoError(t, s.redis.Close()) })
	provider := httptest.NewServer(http.HandlerFunc(s.provider))
	t.Cleanup(provider.Close)
	_, err = f.source.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{base_url}',to_jsonb($1::text)) WHERE id IN (704,705)`, provider.URL+"/v1")
	require.NoError(t, err)
	coreURL, err := url.Parse(f.sourceDSN)
	require.NoError(t, err)
	s.coreProxy = newBridgeObservationProxy(t, coreURL.Host)
	s.redisProxy = newBridgeObservationProxy(t, f.redisAddress)
	return s
}

func (s *bridgeObservationTopology) configureProducer(t *testing.T, revision int64, targets []dispatch.Target) {
	t.Helper()
	cfg := custombridge.TelemetryConfig{SchemaVersion: 1, CoreInstance: topologySource, ConfigRevision: revision}
	for _, target := range targets {
		var raw []byte
		require.NoError(t, s.f.source.QueryRowContext(s.f.ctx, `SELECT credentials FROM accounts WHERE id=$1`, target.AccountID).Scan(&raw))
		var credentials map[string]any
		require.NoError(t, json.Unmarshal(raw, &credentials))
		digest, err := custombridge.TelemetryCredentialSHA256(credentials)
		require.NoError(t, err)
		cfg.Targets = append(cfg.Targets, custombridge.TelemetryTarget{TargetRef: target.Ref, AccountID: target.AccountID,
			CredentialRef: target.CredentialRef, CredentialRevision: target.CredentialRevision, CredentialSHA256: digest,
			ProviderRef: target.ProviderRef, PoolRef: target.PoolRef, PolicyRevision: target.PolicyRevision, Model: target.Model, Region: target.Region})
	}
	require.NoError(t, cfg.Validate())
	require.NoError(t, custombridge.ApplyTelemetrySchema(s.f.ctx, s.f.source))
	s.f.writeJSON(t, "observation-producer.json", cfg)
}

func (s *bridgeObservationTopology) startCore(t *testing.T) *bridgeTopologyProcess {
	t.Helper()
	p := s.f.start(t, "server", []string{"SUB2API_BRIDGE_ENABLED=true", "SUB2API_BRIDGE_CONFIG=" + filepath.Join(s.f.dir, "core-bridge.json"),
		"SUB2API_BRIDGE_TELEMETRY_CONFIG=" + filepath.Join(s.f.dir, "observation-producer.json")})
	s.f.waitHTTP(t, p, s.f.coreURL+"/health", http.StatusOK)
	var revision int64
	var generation string
	var slotTTL int
	require.NoError(t, s.f.source.QueryRowContext(s.f.ctx, `SELECT config_revision,producer_generation,slot_ttl_seconds FROM bridge.dispatch_telemetry_source WHERE singleton`).Scan(&revision, &generation, &slotTTL))
	require.Equal(t, 900, slotTTL)
	t.Logf("actual_cmd_server pid=%d telemetry_config_revision=%d producer_generation=%s native_slot_ttl_seconds=%d", p.cmd.Process.Pid, revision, generation, slotTTL)
	return p
}

func (s *bridgeObservationTopology) configureReaders(t *testing.T) {
	t.Helper()
	_, err := s.f.source.ExecContext(s.f.ctx, `CREATE ROLE topology_telemetry_reader LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT PASSWORD 'synthetic-observation-reader-only';
		ALTER ROLE topology_telemetry_reader SET default_transaction_read_only=on;
		GRANT USAGE ON SCHEMA bridge TO topology_telemetry_reader;
		GRANT SELECT ON bridge.dispatch_telemetry_source,bridge.dispatch_observation TO topology_telemetry_reader`)
	require.NoError(t, err)
	u, err := url.Parse(s.f.sourceDSN)
	require.NoError(t, err)
	u.User = url.UserPassword("topology_telemetry_reader", "synthetic-observation-reader-only")
	u.Host = s.coreProxy.listener.Addr().String()
	s.coreReaderFile = filepath.Join(s.f.dir, "observation-core-reader-dsn")
	require.NoError(t, os.WriteFile(s.coreReaderFile, []byte(u.String()), 0600))
	reader, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	require.NoError(t, reader.PingContext(s.f.ctx))
	_, err = reader.ExecContext(s.f.ctx, `DELETE FROM bridge.dispatch_observation`)
	require.Error(t, err, "观测 Reader 必须不能删除观测")
	var mayWrite, mayCreate bool
	require.NoError(t, reader.QueryRowContext(s.f.ctx, `SELECT has_table_privilege(current_user,'bridge.dispatch_observation','INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER'),has_schema_privilege(current_user,'bridge','CREATE')`).Scan(&mayWrite, &mayCreate))
	require.False(t, mayWrite)
	require.False(t, mayCreate)
	require.NoError(t, s.redis.Do(s.f.ctx, "ACL", "SETUSER", "topology_load_reader", "reset", "on", ">synthetic-load-reader-only",
		"~concurrency:account:*", "~concurrency:live:account:*", "-@all", "+time", "+zcount", "+hello", "+auth", "+select", "+client|setinfo", "+client|setname").Err())
	redisURL := &url.URL{Scheme: "redis", Host: s.redisProxy.listener.Addr().String(), User: url.UserPassword("topology_load_reader", "synthetic-load-reader-only"), Path: "/0"}
	s.redisReaderFile = filepath.Join(s.f.dir, "observation-redis-reader-url")
	require.NoError(t, os.WriteFile(s.redisReaderFile, []byte(redisURL.String()), 0600))
	options, err := redis.ParseURL(redisURL.String())
	require.NoError(t, err)
	options.DisableIdentity = true
	readonly := redis.NewClient(options)
	defer func() { require.NoError(t, readonly.Close()) }()
	require.NoError(t, readonly.Time(s.f.ctx).Err())
	require.NoError(t, readonly.ZCount(s.f.ctx, "concurrency:account:704", "-inf", "+inf").Err())
	require.Error(t, readonly.Del(s.f.ctx, "concurrency:account:704").Err(), "负载 Reader 不能删除原生占位")
	require.Error(t, readonly.ZCount(s.f.ctx, "unrelated:secret", "-inf", "+inf").Err(), "负载 Reader 不能读取其他 namespace")
}

func (s *bridgeObservationTopology) configureFixedEdge(t *testing.T, accountID int64) {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join(s.f.dir, "edge.json"))
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(encoded, &cfg))
	delete(cfg, "strict_dispatch")
	delete(cfg, "shadow_dispatch")
	grants := cfg["grants"].([]any)
	grant := grants[0].(map[string]any)
	delete(grant, "strict_targets")
	grant["account_id"] = accountID
	s.f.writeJSON(t, "edge.json", cfg)
}

func (s *bridgeObservationTopology) requireLiveLoad(t *testing.T) {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join(s.f.dir, "edge.json"))
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(encoded, &cfg))
	cfg["strict_dispatch"].(map[string]any)["require_live_load"] = true
	s.f.writeJSON(t, "edge.json", cfg)
}

func (s *bridgeObservationTopology) attributedFailures(t *testing.T) int {
	t.Helper()
	var count int
	require.NoError(t, s.f.source.QueryRowContext(s.f.ctx, `SELECT COUNT(*) FROM bridge.dispatch_observation WHERE provider_evidence AND outcome<>'success'`).Scan(&count))
	return count
}

func (s *bridgeObservationTopology) health(t *testing.T, ref string) dispatch.Health {
	t.Helper()
	var encoded []byte
	require.NoError(t, s.db.QueryRowContext(s.f.ctx, `SELECT health FROM dispatch.targets WHERE core_instance=$1 AND target_ref=$2`, topologySource, ref).Scan(&encoded))
	var health dispatch.Health
	require.NoError(t, json.Unmarshal(encoded, &health))
	return health
}

func (s *bridgeObservationTopology) waitOutcome(t *testing.T, outcome string, configRevision int64, expected int) {
	t.Helper()
	bridgeTopologyEventually(t, s.f.ctx, func() error {
		var count int
		if err := s.f.source.QueryRowContext(s.f.ctx, `SELECT COUNT(*) FROM bridge.dispatch_observation WHERE outcome=$1 AND config_revision=$2 AND provider_evidence`, outcome, configRevision).Scan(&count); err != nil {
			return err
		}
		if count != expected {
			return fmt.Errorf("等待受信 %s config=%d count=%d want=%d", outcome, configRevision, count, expected)
		}
		return nil
	})
}

func (s *bridgeObservationTopology) assertReceipts(t *testing.T) {
	t.Helper()
	f := s.f
	receipts := f.count(t, f.source, "bridge.receipt_outbox")
	require.Positive(t, receipts, "负载释放后须有真实 strict 成功回执")
	f.waitDrained(t, receipts)
	require.Equal(t, receipts+1, f.count(t, f.source, "usage_billing_dedup"), "另有普通 SSE 请求的原生扣账")
	var unmatched int
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM bridge.receipt_outbox o
		LEFT JOIN usage_billing_dedup d ON d.request_id=o.core_request_id AND d.api_key_id=o.internal_key_id AND d.request_fingerprint=o.request_fingerprint
		LEFT JOIN bridge.forward_attempt a ON a.attempt_id=o.attempt_id AND a.internal_key_id=o.internal_key_id AND a.reservation_id=o.reservation_id
		WHERE d.id IS NULL OR a.attempt_id IS NULL OR a.account_id<>o.account_id OR a.state<>'send_intent'
		OR o.account_id<>704 OR o.billing->>'model'<>'gpt-5.4'`).Scan(&unmatched))
	require.Zero(t, unmatched)
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM bridge.dispatch_observation o
		LEFT JOIN bridge.forward_attempt a ON a.attempt_id=o.attempt_id AND a.internal_key_id=o.internal_key_id
		WHERE a.attempt_id IS NULL OR a.account_id<>o.account_id OR a.core_request_id<>o.core_request_id
		OR a.envelope->>'pool_ref'<>o.pool_ref OR a.envelope->>'policy_revision'<>o.policy_revision
		OR a.envelope->>'canonical_model'<>o.model OR (o.provider_evidence AND a.send_intent_at IS NULL)`).Scan(&unmatched))
	require.Zero(t, unmatched, "所有被动事件须对应实际受信 attempt 身份和出站事实")
	var balance, quota, receiptCost float64
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT balance FROM users WHERE id=701`).Scan(&balance))
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT quota_used FROM api_keys WHERE id=702`).Scan(&quota))
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COALESCE(SUM((billing->>'balance_cost')::numeric),0) FROM bridge.receipt_outbox`).Scan(&receiptCost))
	require.InDelta(t, receiptCost+0.007, quota, 1e-8)
	require.InDelta(t, 100-quota, balance, 1e-8)
	rows, err := f.sink.QueryContext(f.ctx, `SELECT event_id,receipt_raw,payload_digest FROM bridge_shadow.shadow_receipt_ledger WHERE source=$1`, topologySource)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw []byte
		var eventID, digest string
		require.NoError(t, rows.Scan(&eventID, &raw, &digest))
		var shadow custombridge.Receipt
		require.NoError(t, json.Unmarshal(raw, &shadow))
		receipt, err := custombridge.NewReceiptRepository(f.source).Get(f.ctx, shadow.CoreRequestID, shadow.InternalKeyID)
		require.NoError(t, err)
		attempt, err := custombridge.NewForwardAttemptRepository(f.source).Get(f.ctx, receipt.InternalKeyID, receipt.AttemptID)
		require.NoError(t, err)
		require.Equal(t, "core_committed", attempt.State)
		require.Equal(t, receipt.ID, attempt.ReceiptID)
		require.True(t, attempt.SendIntentAt.Valid)
		encoded, err := json.Marshal(receipt)
		require.NoError(t, err)
		normalized, err := json.Marshal(shadow)
		require.NoError(t, err)
		require.JSONEq(t, string(encoded), string(normalized))
		require.Equal(t, receipt.ID, eventID)
		hash := sha256.Sum256(raw)
		require.Equal(t, hex.EncodeToString(hash[:]), digest)
		var inbox int
		require.NoError(t, f.sink.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM bridge_shadow.inbox WHERE source=$1 AND event_id=$2 AND payload_digest=$3`, topologySource, eventID, digest).Scan(&inbox))
		require.Equal(t, 1, inbox)
	}
	require.NoError(t, rows.Err())
	require.Zero(t, f.count(t, f.sink, "bridge_shadow.inbox_conflicts"))
	require.Zero(t, f.providerCount("B"))
	var sends int
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM bridge.forward_attempt WHERE send_intent_at IS NOT NULL`).Scan(&sends))
	require.Equal(t, sends+1, f.providerCount("A"), "另有普通 SSE 出站；业务重启和观测导入不能新增 Provider 调用")
	f.relay.stop(t)
	t.Logf("actual_Core_Edge_dispatchd_relay bridge_receipts=%d native_only_billing=1 total_billing=%d source_observations=%d bridge_attempts=%d actual_provider_A_calls=%d B_calls=0 unfinished_delivery=0 inbox_conflicts=0", receipts, f.count(t, f.source, "usage_billing_dedup"), f.count(t, f.source, "bridge.dispatch_observation"), f.count(t, f.source, "bridge.forward_attempt"), f.providerCount("A"))
}

func (s *bridgeObservationTopology) configureDispatch(t *testing.T, revision int64, targets []dispatch.Target) {
	t.Helper()
	s.f.writeJSON(t, filepath.Base(s.configPath), map[string]any{
		"version": 1, "core_instance": topologySource, "config_revision": revision,
		"snapshot_file": s.snapshotPath, "snapshot_ttl_seconds": 10, "poll_interval_milliseconds": 100,
		"targets":   targets,
		"telemetry": map[string]any{"slot_ttl_seconds": 900, "load_max_age_seconds": 2, "max_observations": 10000},
	})
}

func (s *bridgeObservationTopology) round(t *testing.T, success bool) dispatch.Snapshot {
	t.Helper()
	before, beforeErr := os.ReadFile(s.snapshotPath)
	p := s.f.start(t, "dispatchd", []string{"DISPATCH_CORE_DSN_FILE=" + s.coreReaderFile, "DISPATCH_REDIS_URL_FILE=" + s.redisReaderFile},
		"--once", "--config", s.configPath, "--dsn-file", s.dsnFile)
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
		t.Fatal("真实 dispatchd --once 未在来源读取界限内退出")
	}
	output, err := os.ReadFile(p.logPath)
	require.NoError(t, err)
	t.Logf("actual_cmd_dispatchd pid=%d once_success=%t result=%s", p.cmd.Process.Pid, p.err == nil, strings.TrimSpace(string(output)))
	if !success {
		require.Error(t, p.err, "失联来源不能发布成功快照: %s", output)
		after, err := os.ReadFile(s.snapshotPath)
		require.NoError(t, beforeErr)
		require.NoError(t, err)
		require.Equal(t, before, after, "读取失败必须原样保留旧快照，不能把 active 刷新为 0 或延长期限")
		return dispatch.Snapshot{}
	}
	require.NoError(t, p.err, "真实 dispatchd 工作轮次: %s", output)
	encoded, err := os.ReadFile(s.snapshotPath)
	require.NoError(t, err)
	var snapshot dispatch.Snapshot
	require.NoError(t, json.Unmarshal(encoded, &snapshot))
	require.NoError(t, snapshot.Validate(time.Now()))
	var freshness struct {
		ObservedAt time.Time `json:"load_observed_at"`
		ExpiresAt  time.Time `json:"load_expires_at"`
	}
	require.NoError(t, json.Unmarshal(encoded, &freshness))
	require.False(t, freshness.ObservedAt.IsZero(), "真实负载快照必须带采样时间")
	require.True(t, freshness.ExpiresAt.After(freshness.ObservedAt))
	require.False(t, snapshot.ExpiresAt.After(freshness.ExpiresAt), "快照不能活过其负载样本")
	return snapshot
}

func (s *bridgeObservationTopology) provider(w http.ResponseWriter, r *http.Request) {
	label := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer topology-synthetic-provider-")
	if r.URL.Path != "/v1/responses" || (label != "A" && label != "B") {
		http.Error(w, "unexpected synthetic observation provider request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	status, retryAfter, disconnect := s.providerStatus, s.providerRetryAfter, s.providerDisconnect
	s.mu.Unlock()
	if status == 0 && !disconnect {
		s.f.provider(w, r)
		return
	}
	s.f.mu.Lock()
	s.f.providerCalls[label]++
	s.f.mu.Unlock()
	if disconnect {
		connection, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = connection.Close()
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", "observation-synthetic-provider-error")
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"type":"synthetic_provider_error","message":"synthetic provider status %d"}}`, status)
}

func (s *bridgeObservationTopology) setProvider(status int, retryAfter string, disconnect bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providerStatus, s.providerRetryAfter, s.providerDisconnect = status, retryAfter, disconnect
}

func observationTopologyCandidate(t *testing.T, snapshot dispatch.Snapshot, ref string) dispatch.Candidate {
	t.Helper()
	for _, candidate := range snapshot.Candidates {
		if candidate.Ref == ref {
			return candidate
		}
	}
	t.Fatalf("真实 dispatchd 快照缺少候选 %s", ref)
	return dispatch.Candidate{}
}

// 只断开 telemetry reader 的网络，Core/relay 的原连接不受测试故障影响。
type bridgeObservationProxy struct {
	listener net.Listener
	upstream string
	done     chan struct{}
	mu       sync.Mutex
	blocked  bool
	clients  map[net.Conn]struct{}
	workers  sync.WaitGroup
}

func newBridgeObservationProxy(t *testing.T, upstream string) *bridgeObservationProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &bridgeObservationProxy{listener: listener, upstream: upstream, done: make(chan struct{}), clients: make(map[net.Conn]struct{})}
	go p.accept()
	t.Cleanup(func() {
		require.NoError(t, p.listener.Close())
		<-p.done
		p.block(true)
		joined := make(chan struct{})
		go func() { p.workers.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("观测网络代理连接未全部退出")
		}
	})
	return p
}

func (p *bridgeObservationProxy) accept() {
	defer close(p.done)
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		if p.blocked {
			p.mu.Unlock()
			_ = client.Close()
			continue
		}
		p.clients[client] = struct{}{}
		p.workers.Add(1)
		p.mu.Unlock()
		go p.forward(client)
	}
}

func (p *bridgeObservationProxy) forward(client net.Conn) {
	defer p.workers.Done()
	defer func() {
		_ = client.Close()
		p.mu.Lock()
		delete(p.clients, client)
		p.mu.Unlock()
	}()
	upstream, err := net.DialTimeout("tcp", p.upstream, time.Second)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	joined := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
		close(joined)
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	<-joined
}

func (p *bridgeObservationProxy) block(blocked bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked = blocked
	if blocked {
		for client := range p.clients {
			_ = client.Close()
		}
	}
}
