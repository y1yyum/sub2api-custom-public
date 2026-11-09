//go:build integration

package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

const (
	topologyUserID   = 701
	topologyKeyID    = 702
	topologyGroupID  = 703
	topologyAccountA = 704
	topologyAccountB = 705
	topologyCoreKey  = "sk-topology-synthetic-core-only"
	topologyEdgeKey  = "topology-synthetic-edge-only"
	topologySource   = "topology-core"
	topologyConsumer = "topology-shadow"
)

// 此测试只在自身创建的容器中迁移、种子和注入锁，不使用 integrationDB。
// Core 始终由 ./cmd/server 构建；没有替代入口、链接注入或简化 Core helper。
func TestBridgeTopology(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("实际 SIGKILL 进程演练要求 Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	f := newBridgeTopology(t, ctx)
	f.core = f.startCore(t, true)
	f.build(t, "bridge-edge")
	f.edge = f.startEdge(t)
	f.relay = f.startRelay(t)

	t.Run("json_and_sse_use_only_bound_account", func(t *testing.T) {
		for index, stream := range []bool{false, true} {
			key := fmt.Sprintf("baseline-%t", stream)
			result := f.request(ctx, f.edgeURL, topologyEdgeKey, key, stream)
			require.NoError(t, result.err)
			require.Equal(t, http.StatusOK, result.status, result.body)
			require.Contains(t, result.body, "provider-A")
			if stream {
				require.Contains(t, result.contentType, "text/event-stream")
				require.Contains(t, result.body, "response.completed")
			} else {
				require.Contains(t, result.contentType, "application/json")
				require.True(t, json.Valid([]byte(result.body)))
			}
			f.waitReceipts(t, index+1)
		}
		f.waitDrained(t, 2)
		f.assertFacts(t, 2)
		require.Equal(t, 2, f.providerCount("A"))
		require.Zero(t, f.providerCount("B"), "B 的原生优先级更高，受信绑定仍只能发 A")
	})
	if t.Failed() {
		return
	}

	t.Run("kill_core_before_send_intent_commit", func(t *testing.T) {
		gate := f.gate(t, "before_send", `BEFORE UPDATE ON bridge.forward_attempt FOR EACH ROW WHEN (NEW.state = 'send_intent')`)
		before := f.providerCount("A")
		pending := f.requestAsync(ctx, "kill-before-send", false)
		gate.wait(t)
		attempt := f.lastAttempt(t)
		require.Equal(t, "received", attempt.State)
		require.False(t, attempt.SendIntentAt.Valid)
		require.Equal(t, before, f.providerCount("A"))
		oldPID := f.core.kill(t)
		gate.release(t)
		f.awaitRequest(t, pending)
		f.core = f.startCore(t, true)
		require.NotEqual(t, oldPID, f.core.cmd.Process.Pid)
		f.assertReplayRejected(t, "kill-before-send", false, before)
		// ClaimSend 是自动提交语句：进程死后 PostgreSQL 仍可能提交已接收的 UPDATE。
		// 两种持久事实都必须禁止重入；send_intent 不证明 Provider 收到过请求。
		recovered, err := custombridge.NewForwardAttemptRepository(f.source).Get(ctx, topologyKeyID, attempt.Envelope.AttemptID)
		require.NoError(t, err)
		require.Contains(t, []string{"received", "reconciliation_required"}, recovered.State)
		f.assertAttempt(t, attempt, recovered.State)
		f.assertFacts(t, 2)
		t.Logf("actual_cmd_server killed_pid=%d fresh_pid=%d state=%s provider_calls_added=0 automatic_resends=0", oldPID, f.core.cmd.Process.Pid, recovered.State)
	})
	if t.Failed() {
		return
	}

	t.Run("kill_core_after_provider_before_billing_commit", func(t *testing.T) {
		gate := f.gate(t, "before_receipt", `BEFORE INSERT ON bridge.receipt_outbox FOR EACH ROW`)
		before := f.providerCount("A")
		pending := f.requestAsync(ctx, "kill-before-billing-commit", true)
		gate.wait(t)
		attempt := f.lastAttempt(t)
		require.Equal(t, "reconciliation_required", attempt.State)
		require.Equal(t, before+1, f.providerCount("A"))
		// 阻塞在真实扣账事务的 receipt INSERT：扣账和 dedup 对其他连接都不可见。
		f.assertFacts(t, 2)
		oldPID := f.core.kill(t)
		gate.release(t)
		f.awaitRequest(t, pending)
		f.core = f.startCore(t, true)
		require.NotEqual(t, oldPID, f.core.cmd.Process.Pid)
		f.assertReplayRejected(t, "kill-before-billing-commit", true, before+1)
		f.assertAttempt(t, attempt, "reconciliation_required")
		f.assertFacts(t, 2)
		t.Logf("actual_cmd_server killed_pid=%d fresh_pid=%d uncommitted_billing_rolled_back=true provider_calls_added=1 automatic_resends=0", oldPID, f.core.cmd.Process.Pid)
	})
	if t.Failed() {
		return
	}

	t.Run("kill_core_and_relay_after_sink_commit_before_ack", func(t *testing.T) {
		gate := f.gate(t, "before_ack", `BEFORE UPDATE ON bridge.receipt_delivery FOR EACH ROW WHEN (NEW.state = 'acked')`)
		before := f.providerCount("A")
		result := f.request(ctx, f.edgeURL, topologyEdgeKey, "kill-before-relay-ack", false)
		require.NoError(t, result.err)
		require.Equal(t, http.StatusOK, result.status, result.body)
		f.waitReceipts(t, 3)
		gate.wait(t)
		attempt := f.lastAttempt(t)
		require.Equal(t, "core_committed", attempt.State)
		require.Equal(t, 3, f.count(t, f.sink, "bridge_shadow.shadow_receipt_ledger"))
		var state string
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT state FROM bridge.receipt_delivery WHERE receipt_id=$1`, attempt.ReceiptID).Scan(&state))
		require.Equal(t, "leased", state, "目标已提交，源 ACK 事务尚未提交")
		corePID, relayPID := f.core.kill(t), f.relay.kill(t)
		gate.release(t)
		f.core = f.startCore(t, true)
		f.relay = f.startRelay(t)
		require.NotEqual(t, corePID, f.core.cmd.Process.Pid)
		require.NotEqual(t, relayPID, f.relay.cmd.Process.Pid)
		f.assertReplayRejected(t, "kill-before-relay-ack", false, before+1)
		f.waitDrained(t, 3)
		f.assertFacts(t, 3)
		var generations, claims, acks int
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT lease_generation FROM bridge.receipt_delivery WHERE receipt_id=$1`, attempt.ReceiptID).Scan(&generations))
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE action='claimed'), COUNT(*) FILTER (WHERE action='acked') FROM bridge.receipt_delivery_audit WHERE receipt_id=$1`, attempt.ReceiptID).Scan(&claims, &acks))
		require.GreaterOrEqual(t, generations, 2)
		require.GreaterOrEqual(t, claims, 2)
		require.Equal(t, 1, acks)
		t.Logf("actual_cmd_server killed_pid=%d fresh_pid=%d actual_relay killed_pid=%d fresh_pid=%d claim_generations=%d shadow_effects=1 automatic_provider_resends=0", corePID, f.core.cmd.Process.Pid, relayPID, f.relay.cmd.Process.Pid, generations)
	})
	if t.Failed() {
		return
	}

	t.Run("drain_then_disable_bridge_preserves_native_route", func(t *testing.T) {
		f.edge.stop(t)
		f.waitDrained(t, 3)
		f.relay.stop(t)
		f.core.stop(t)
		// 只关闭测试夹具 A，使普通路由确定性选择一直可用的 B。
		_, err := f.source.ExecContext(ctx, `UPDATE accounts SET schedulable=false WHERE id=704`)
		require.NoError(t, err)
		f.core = f.startCore(t, false)
		response := f.request(ctx, f.coreURL, topologyCoreKey, "native-after-disable", false)
		require.NoError(t, response.err)
		require.Equal(t, http.StatusOK, response.status, response.body)
		require.Contains(t, response.body, "provider-B")
		require.Equal(t, 1, f.providerCount("B"))
		bridgeTopologyEventually(t, ctx, func() error {
			if f.count(t, f.source, "usage_billing_dedup") != 4 {
				return errors.New("等待普通路径原生扣账")
			}
			return nil
		})
		require.Equal(t, 3, f.count(t, f.source, "bridge.receipt_outbox"))
		require.Equal(t, 5, f.count(t, f.source, "bridge.forward_attempt"))
		require.Equal(t, 3, f.count(t, f.sink, "bridge_shadow.shadow_receipt_ledger"))
		var balance, quota, accountBQuota float64
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=701`).Scan(&balance))
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT quota_used FROM api_keys WHERE id=702`).Scan(&quota))
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT (extra->>'quota_used')::numeric FROM accounts WHERE id=705`).Scan(&accountBQuota))
		require.InDelta(t, 99.972, balance, 1e-8)
		require.InDelta(t, 0.028, quota, 1e-8)
		require.InDelta(t, 0.007, accountBQuota, 1e-8)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.coreURL+custombridge.ForwardPath, strings.NewReader(`{"model":"gpt-5.4","input":"disabled"}`))
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer "+topologyCoreKey)
		request.Header.Set("Content-Type", "application/json")
		resp, err := f.client.Do(request)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		t.Log("排空标准：所有 outbox 均有该消费者 ACK，pending/leased/dead_letter=0，独立 inbox/ledger 各 3；关闭桥后普通 JSON 与原生扣账成功且未新增桥记录")
	})
}

func TestBridgeTopologyDispatchProbe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("实际二进制拓扑验收要求 Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	f := newBridgeTopology(t, ctx)
	f.core = f.startCore(t, true)
	f.relay = f.startRelay(t)
	f.build(t, "dispatchd")
	dispatchDB, dispatchDSN := bridgeTopologyDatabase(t, ctx, "dispatch")
	require.NoError(t, dispatch.ApplySchema(ctx, dispatchDB))
	dispatchFile := filepath.Join(f.dir, "dispatch-dsn")
	require.NoError(t, os.WriteFile(dispatchFile, []byte(dispatchDSN), 0600))

	// relay 持有目标库写权限；dispatchd 只得到独立、只读的 shadow 身份。
	_, err := f.sink.ExecContext(ctx, `CREATE ROLE topology_shadow_reader LOGIN PASSWORD 'topology-synthetic-reader-only';
		ALTER ROLE topology_shadow_reader SET default_transaction_read_only=on;
		GRANT USAGE ON SCHEMA bridge_shadow TO topology_shadow_reader;
		GRANT SELECT ON bridge_shadow.identity,bridge_shadow.inbox,bridge_shadow.shadow_receipt_ledger TO topology_shadow_reader`)
	require.NoError(t, err)
	shadowURL, err := url.Parse(f.sinkDSN)
	require.NoError(t, err)
	shadowURL.User = url.UserPassword("topology_shadow_reader", "topology-synthetic-reader-only")
	shadowFile := filepath.Join(f.dir, "shadow-reader-dsn")
	require.NoError(t, os.WriteFile(shadowFile, []byte(shadowURL.String()), 0600))
	reader, err := sql.Open("postgres", shadowURL.String())
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	var readOnly string
	require.NoError(t, reader.QueryRowContext(ctx, `SHOW default_transaction_read_only`).Scan(&readOnly))
	require.Equal(t, "on", readOnly)
	_, err = reader.ExecContext(ctx, `DELETE FROM bridge_shadow.shadow_receipt_ledger`)
	require.Error(t, err, "Reader 不能写入或清理影子账")

	credentialsFile := "dispatch-service-credentials.json"
	f.writeJSON(t, credentialsFile, map[string]any{
		"kid": "topology-kid", "secret_base64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{57}, 32)),
		"core_api_key": topologyCoreKey, "internal_key_id": topologyKeyID, "group_id": topologyGroupID,
	})
	snapshotPath := filepath.Join(f.dir, "dispatch-snapshot.json")
	f.writeJSON(t, "dispatch.json", map[string]any{
		"version": 1, "config_revision": 1, "core_instance": topologySource, "snapshot_file": snapshotPath,
		"targets": []any{map[string]any{
			"ref": "topology-target-A", "account_id": topologyAccountA, "credential_ref": "synthetic-credential-A", "credential_revision": 1,
			"provider_ref": "synthetic-provider", "pool_ref": "topology-pool", "policy_revision": "p1", "model": "gpt-5.4", "region": "fixture-region", "base_weight": 100, "max_inflight": 16, "enabled": true,
		}},
		"probe": map[string]any{"core_origin": f.coreURL, "audience": topologySource, "allow_loopback_http": true,
			"credentials_file": filepath.Join(f.dir, credentialsFile), "receipt_source": topologySource},
	})
	p := f.start(t, "dispatchd", []string{"DISPATCH_SHADOW_DSN_FILE=" + shadowFile}, "--once", "--config", filepath.Join(f.dir, "dispatch.json"), "--dsn-file", dispatchFile)
	select {
	case <-p.done:
		require.NoError(t, p.err, "实际 dispatchd --once 退出失败")
	case <-time.After(35 * time.Second):
		t.Fatal("实际 dispatchd --once 未在探测与回执界限内退出")
	}
	output, err := os.ReadFile(p.logPath)
	require.NoError(t, err)
	var result dispatch.RoundResult
	require.NoError(t, json.Unmarshal(output, &result))
	require.Equal(t, 1, result.Probes)
	require.Zero(t, result.Unknown)
	require.Equal(t, 1, result.Candidates)
	require.Positive(t, result.Revision)
	encodedSnapshot, err := os.ReadFile(snapshotPath)
	require.NoError(t, err)
	var snapshot dispatch.Snapshot
	require.NoError(t, json.Unmarshal(encodedSnapshot, &snapshot))
	require.NoError(t, snapshot.Validate(time.Now()))
	require.Equal(t, result.Revision, snapshot.Revision)
	require.Len(t, snapshot.Candidates, 1)
	require.Equal(t, int64(topologyAccountA), snapshot.Candidates[0].AccountID)
	require.Equal(t, dispatch.Healthy, snapshot.Candidates[0].State)
	require.Positive(t, snapshot.Candidates[0].EffectiveWeight)
	var probes, finished, daily, providerRPM int
	require.NoError(t, dispatchDB.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE finished_at IS NOT NULL),
		COUNT(*) FILTER (WHERE reserved_at::date=CURRENT_DATE),COUNT(*) FILTER (WHERE provider_ref='synthetic-provider' AND reserved_at>clock_timestamp()-interval '1 minute') FROM dispatch.probes`).Scan(&probes, &finished, &daily, &providerRPM))
	require.Equal(t, 1, probes)
	require.Equal(t, 1, finished)
	require.Equal(t, 1, daily)
	require.Equal(t, 1, providerRPM)
	var attemptID, reservationID, outcome string
	var providerEvidence bool
	require.NoError(t, dispatchDB.QueryRowContext(ctx, `SELECT p.attempt_id,p.reservation_id,o.payload->>'outcome',(o.payload->>'provider_evidence')::boolean
		FROM dispatch.probes p JOIN dispatch.observations o ON o.core_instance=p.core_instance AND o.event_id=p.attempt_id`).Scan(&attemptID, &reservationID, &outcome, &providerEvidence))
	require.Equal(t, string(dispatch.Success), outcome)
	require.True(t, providerEvidence)
	// 用只读 Reader 再按同一 reservation/attempt 读取真实 relay 产生的证据。
	shadowReader, err := dispatch.NewShadowReceiptReader(ctx, reader, topologySource)
	require.NoError(t, err)
	receipt, err := shadowReader.ReadReceipt(ctx, reservationID, attemptID)
	require.NoError(t, err)
	require.Equal(t, int64(topologyAccountA), receipt.AccountID)
	require.Equal(t, int64(topologyKeyID), receipt.InternalKeyID)
	f.waitDrained(t, 1)
	f.assertFacts(t, 1)
	require.Equal(t, 1, f.providerCount("A"))
	require.Zero(t, f.providerCount("B"))
	for _, sensitive := range []string{dispatchDSN, shadowURL.String(), topologyCoreKey, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{57}, 32))} {
		require.False(t, strings.Contains(string(output), sensitive), "dispatchd 输出不能包含运行时 DSN 或密钥")
		require.False(t, strings.Contains(string(encodedSnapshot), sensitive), "调度快照不能包含运行时 DSN 或密钥")
	}
	t.Logf("actual_dispatchd pid=%d signed_probe=1 outcome=success shadow_reader=read_only daily_budget_used=1 provider_rpm_used=1 bound_provider_A_calls=1 B_calls=0", p.cmd.Process.Pid)
}

type bridgeTopology struct {
	owner                          *testing.T
	ctx                            context.Context
	dir, backend, coreURL, edgeURL string
	sourceDSN, sinkDSN             string
	redisAddress                   string
	source, sink                   *sql.DB
	env                            []string
	core, edge, relay              *bridgeTopologyProcess
	client                         *http.Client
	mu                             sync.Mutex
	providerCalls                  map[string]int
	providerStreamGate             <-chan struct{}
}

func newBridgeTopology(t *testing.T, ctx context.Context) *bridgeTopology {
	t.Helper()
	f := &bridgeTopology{owner: t, ctx: ctx, dir: t.TempDir(), providerCalls: map[string]int{}}
	var err error
	f.backend, err = filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	for _, command := range []string{"server", "bridge-relay"} {
		f.build(t, command)
	}
	f.source, f.sourceDSN = bridgeTopologyDatabase(t, ctx, "core")
	f.sink, f.sinkDSN = bridgeTopologyDatabase(t, ctx, "shadow")
	redis, err := tcredis.Run(ctx, redisImageTag)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, redis.Terminate(cleanup))
	})
	redisHost, err := redis.Host(ctx)
	require.NoError(t, err)
	redisPort, err := redis.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	f.redisAddress = net.JoinHostPort(redisHost, redisPort.Port())
	f.coreURL, f.edgeURL = bridgeTopologyAddress(t), bridgeTopologyAddress(t)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	f.client = &http.Client{Transport: transport, Timeout: 30 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	provider := httptest.NewServer(http.HandlerFunc(f.provider))
	t.Cleanup(provider.Close)
	// 不继承操作者的数据库、代理、桥配置或生产凭据环境变量。
	f.env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + f.dir, "TMPDIR=" + os.TempDir(), "GOMAXPROCS=2", "TZ=UTC", "GIN_MODE=release",
		"CONFIG_FILE=" + filepath.Join(f.dir, "config.yaml"), "DATA_DIR=" + f.dir, "SKIP_SETUP=true", "AUTO_SETUP=false",
		"GEMINI_CLI_OAUTH_CLIENT_ID=topology-synthetic-only"}
	u, err := url.Parse(f.sourceDSN)
	require.NoError(t, err)
	password, _ := u.User.Password()
	_, corePort, err := net.SplitHostPort(strings.TrimPrefix(f.coreURL, "http://"))
	require.NoError(t, err)
	configuration := fmt.Sprintf(`run_mode: standard
timezone: UTC
server:
  host: 127.0.0.1
  port: %s
  mode: release
database:
  host: %s
  port: %s
  user: %s
  password: %s
  dbname: %s
  sslmode: disable
  max_open_conns: 16
  max_idle_conns: 4
redis:
  host: %s
  port: %d
jwt:
  secret: %s
totp:
  encryption_key: %s
pricing:
  remote_url: ""
  hash_url: ""
  data_dir: %s/pricing
  fallback_file: %s/prices.json
  override_file: %s/prices.json
security:
  url_allowlist:
    enabled: false
    allow_private_hosts: true
    allow_insecure_http: true
log:
  level: warn
  output:
    to_stdout: true
    to_file: false
`, corePort, u.Hostname(), u.Port(), u.User.Username(), password, strings.TrimPrefix(u.Path, "/"), redisHost, redisPort.Int(), strings.Repeat("t", 40), strings.Repeat("3a", 32), f.dir, f.dir, f.dir)
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "config.yaml"), []byte(configuration), 0600))
	prices := `{"gpt-5.4":{"input_cost_per_token":0.001,"output_cost_per_token":0.002,"litellm_provider":"openai","mode":"chat"}}`
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "prices.json"), []byte(prices), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(f.dir, "pricing"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "pricing", "model_pricing.json"), []byte(prices), 0600))
	command := exec.CommandContext(ctx, filepath.Join(f.dir, "server"), "--migrate-only")
	command.Dir, command.Env, command.WaitDelay = f.dir, f.env, 3*time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		// migration CLI 对外隐藏配置错误细节；实际 server 的启动诊断便于定位夹具。
		diagnosticCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		diagnostic := exec.CommandContext(diagnosticCtx, filepath.Join(f.dir, "server"))
		diagnostic.Dir, diagnostic.Env, diagnostic.WaitDelay = f.dir, f.env, time.Second
		details, _ := diagnostic.CombinedOutput()
		t.Logf("实际 Core 配置诊断: %s", details)
	}
	require.NoError(t, err, "实际 Core migration CLI: %s", output)
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, f.source))
	require.NoError(t, custombridge.ApplyForwardSchema(ctx, f.source))
	require.NoError(t, custombridge.ApplyRelaySchema(ctx, f.source))
	require.NoError(t, custombridge.ApplyInboxSchema(ctx, f.sink))
	seed := []string{
		`INSERT INTO users(id,email,password_hash,balance,concurrency,status) VALUES (701,'topology@example.invalid','synthetic-unusable-hash',100,16,'active')`,
		`INSERT INTO groups(id,name,platform,subscription_type,rate_multiplier) VALUES (703,'topology','openai','standard',1)`,
		`INSERT INTO api_keys(id,user_id,key,name,group_id,quota,rate_limit_5h,rate_limit_1d,rate_limit_7d) VALUES (702,701,'sk-topology-synthetic-core-only','topology',703,100,100,100,100)`,
		`INSERT INTO settings(key,value) VALUES ('openai_codex_version_auto_sync_enabled','false'),('claude_code_version_auto_sync_enabled','false') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`,
	}
	for _, query := range seed {
		_, err := f.source.ExecContext(ctx, query)
		require.NoError(t, err)
	}
	for _, account := range []struct {
		id       int
		label    string
		priority int
	}{{topologyAccountA, "A", 100}, {topologyAccountB, "B", 1}} {
		credentials, err := json.Marshal(map[string]any{"api_key": "topology-synthetic-provider-" + account.label, "base_url": provider.URL + "/v1"})
		require.NoError(t, err)
		_, err = f.source.ExecContext(ctx, `INSERT INTO accounts(id,name,platform,type,credentials,extra,concurrency,priority) VALUES ($1,$2,'openai','apikey',$3::jsonb,'{"openai_responses_supported":true,"quota_limit":100}',16,$4)`, account.id, account.label, string(credentials), account.priority)
		require.NoError(t, err)
		_, err = f.source.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES ($1,703,$2)`, account.id, account.priority)
		require.NoError(t, err)
	}
	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{57}, 32))
	key := map[string]any{"id": "topology-kid", "secret_base64": secret, "internal_key_id": topologyKeyID, "group_id": topologyGroupID}
	f.writeJSON(t, "core-bridge.json", map[string]any{"schema_version": 1, "audience": topologySource, "allow_loopback_http": true, "keys": []any{key}})
	digest := sha256.Sum256([]byte(topologyEdgeKey))
	f.writeJSON(t, "edge.json", map[string]any{
		"version": 1, "listen": strings.TrimPrefix(f.edgeURL, "http://"), "core_origin": f.coreURL, "audience": topologySource, "allow_loopback_http": true,
		"signature_ttl_seconds": 10, "request_timeout_seconds": 25, "response_header_timeout_seconds": 25, "signing_keys": []any{key},
		"grants": []any{map[string]any{"grant_id": "topology-grant", "bearer_sha256": hex.EncodeToString(digest[:]), "signing_key_id": "topology-kid", "account_id": topologyAccountA,
			"core_key_id": topologyKeyID, "group_id": topologyGroupID, "core_api_key": topologyCoreKey, "pool_ref": "topology-pool", "policy_revision": "p1", "payer_ref": "synthetic-payer", "actor_ref": "synthetic-actor"}},
	})
	return f
}

func (f *bridgeTopology) build(t *testing.T, command string) {
	t.Helper()
	build := exec.CommandContext(f.ctx, "go", "build", "-p", "2", "-o", filepath.Join(f.dir, command), "./cmd/"+command)
	build.Dir, build.WaitDelay = f.backend, 3*time.Second
	output, err := build.CombinedOutput()
	require.NoError(t, err, "构建实际 cmd/%s: %s", command, output)
}

func bridgeTopologyDatabase(t *testing.T, ctx context.Context, side string) (*sql.DB, string) {
	t.Helper()
	container, err := tcpostgres.Run(ctx, selectDockerImage(ctx, postgresImageTag), tcpostgres.WithDatabase("topology_"+side),
		tcpostgres.WithUsername("topology"), tcpostgres.WithPassword("topology-synthetic-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, container.Terminate(cleanup))
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC", "connect_timeout=5")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(ctx))
	return db, dsn
}

func bridgeTopologyAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := "http://" + listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

func (f *bridgeTopology) writeJSON(t *testing.T, name string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, name), encoded, 0600))
}

type bridgeTopologyProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	err     error
	logPath string
}

func (f *bridgeTopology) start(t *testing.T, name string, env []string, args ...string) *bridgeTopologyProcess {
	t.Helper()
	logFile, err := os.CreateTemp(f.dir, name+"-*.log")
	require.NoError(t, err)
	p := &bridgeTopologyProcess{cmd: exec.CommandContext(f.ctx, filepath.Join(f.dir, name), args...), done: make(chan struct{}), logPath: logFile.Name()}
	var baseEnv []string
	for _, entry := range f.env {
		if name != "server" && (strings.HasPrefix(entry, "CONFIG_FILE=") || strings.HasPrefix(entry, "DATA_DIR=") || strings.HasPrefix(entry, "SKIP_SETUP=") || strings.HasPrefix(entry, "AUTO_SETUP=")) {
			continue
		}
		baseEnv = append(baseEnv, entry)
	}
	p.cmd.Dir, p.cmd.Env, p.cmd.WaitDelay = f.dir, append(baseEnv, env...), 3*time.Second
	p.cmd.Stdout, p.cmd.Stderr = logFile, logFile
	var stdoutRead, stdoutWrite *os.File
	var stdoutDone chan error
	if name == "bridge-edge" {
		stdoutRead, stdoutWrite, err = os.Pipe()
		require.NoError(t, err)
		p.cmd.Stdout = stdoutWrite
		stdoutDone = make(chan error, 1)
		go func() {
			_, copyErr := io.Copy(logFile, stdoutRead)
			_ = stdoutRead.Close()
			stdoutDone <- copyErr
		}()
	}
	if err := p.cmd.Start(); err != nil {
		if stdoutWrite != nil {
			_ = stdoutWrite.Close()
			<-stdoutDone
		}
		_ = logFile.Close()
		t.Fatalf("启动实际 %s: %v", name, err)
	}
	if stdoutWrite != nil {
		_ = stdoutWrite.Close()
	}
	go func() {
		p.err = p.cmd.Wait()
		if stdoutDone != nil {
			p.err = errors.Join(p.err, <-stdoutDone)
		}
		_ = logFile.Close()
		close(p.done)
	}()
	f.owner.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				f.owner.Errorf("子进程未退出: %s pid=%d", name, p.cmd.Process.Pid)
			}
		}
		if f.owner.Failed() {
			body, _ := os.ReadFile(p.logPath)
			if len(body) > 24000 {
				body = body[len(body)-24000:]
			}
			f.owner.Logf("%s pid=%d log:\n%s", name, p.cmd.Process.Pid, body)
		}
	})
	return p
}

func (p *bridgeTopologyProcess) kill(t *testing.T) int {
	t.Helper()
	require.NoError(t, p.cmd.Process.Kill())
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("SIGKILL 后子进程未退出")
	}
	var exit *exec.ExitError
	require.ErrorAs(t, p.err, &exit)
	status, ok := exit.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	require.Equal(t, syscall.SIGKILL, status.Signal())
	return p.cmd.Process.Pid
}

func (p *bridgeTopologyProcess) stop(t *testing.T) {
	t.Helper()
	require.NoError(t, p.cmd.Process.Signal(syscall.SIGTERM))
	select {
	case <-p.done:
		require.NoError(t, p.err)
	case <-time.After(12 * time.Second):
		t.Fatal("SIGTERM 后子进程未在界限内退出")
	}
}

func (f *bridgeTopology) startCore(t *testing.T, enabled bool) *bridgeTopologyProcess {
	t.Helper()
	configPath := filepath.Join(f.dir, "core-bridge.json")
	if !enabled {
		configPath = filepath.Join(f.dir, "must-not-be-read.json")
	}
	p := f.start(t, "server", []string{"SUB2API_BRIDGE_ENABLED=" + strconv.FormatBool(enabled), "SUB2API_BRIDGE_CONFIG=" + configPath})
	f.waitHTTP(t, p, f.coreURL+"/health", http.StatusOK)
	return p
}

func (f *bridgeTopology) startEdge(t *testing.T) *bridgeTopologyProcess {
	t.Helper()
	p := f.start(t, "bridge-edge", nil, "--run", "--config", filepath.Join(f.dir, "edge.json"))
	f.waitHTTP(t, p, f.edgeURL+"/", http.StatusNotFound)
	return p
}

func (f *bridgeTopology) startRelay(t *testing.T) *bridgeTopologyProcess {
	t.Helper()
	p := f.start(t, "bridge-relay", []string{"BRIDGE_RELAY_SOURCE_DSN=" + f.sourceDSN, "BRIDGE_RELAY_SINK_DSN=" + f.sinkDSN},
		"--run", "--source", topologySource, "--consumer", topologyConsumer, "--poll", "20ms", "--lease", "3s", "--delivery-timeout", "2s")
	bridgeTopologyEventually(t, f.ctx, func() error {
		select {
		case <-p.done:
			return fmt.Errorf("relay 提前退出: %w", p.err)
		default:
		}
		var count int
		if err := f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM bridge.receipt_consumers WHERE consumer_id=$1`, topologyConsumer).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("等待 relay 注册")
		}
		return nil
	})
	return p
}

func (f *bridgeTopology) waitHTTP(t *testing.T, p *bridgeTopologyProcess, address string, status int) {
	t.Helper()
	bridgeTopologyEventually(t, f.ctx, func() error {
		select {
		case <-p.done:
			return fmt.Errorf("进程提前退出: %w", p.err)
		default:
		}
		ctx, cancel := context.WithTimeout(f.ctx, 300*time.Millisecond)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		response, err := f.client.Do(request)
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		if response.StatusCode != status {
			return fmt.Errorf("启动探针 HTTP %d", response.StatusCode)
		}
		return nil
	})
}

func bridgeTopologyEventually(t *testing.T, ctx context.Context, check func() error) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	var last error
	for {
		if last = check(); last == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("整链验收超时: %v; last=%v", ctx.Err(), last)
		case <-deadline.C:
			t.Fatalf("等待状态超时: %v", last)
		case <-time.After(30 * time.Millisecond):
		}
	}
}

type bridgeTopologyResponse struct {
	status            int
	body, contentType string
	err               error
}

func (f *bridgeTopology) request(ctx context.Context, origin, token, key string, stream bool) bridgeTopologyResponse {
	body := fmt.Sprintf(`{"model":"gpt-5.4","input":%q,"stream":%t}`, key, stream)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/v1/responses", strings.NewReader(body))
	if err != nil {
		return bridgeTopologyResponse{err: err}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response, err := f.client.Do(request)
	if err != nil {
		return bridgeTopologyResponse{err: err}
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return bridgeTopologyResponse{response.StatusCode, string(encoded), response.Header.Get("Content-Type"), err}
}

func (f *bridgeTopology) requestAsync(ctx context.Context, key string, stream bool) <-chan bridgeTopologyResponse {
	done := make(chan bridgeTopologyResponse, 1)
	go func() { done <- f.request(ctx, f.edgeURL, topologyEdgeKey, key, stream) }()
	return done
}

func (f *bridgeTopology) awaitRequest(t *testing.T, done <-chan bridgeTopologyResponse) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Core 被杀后 Edge 请求未终止")
	}
}

func (f *bridgeTopology) provider(w http.ResponseWriter, r *http.Request) {
	label := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer topology-synthetic-provider-")
	if r.URL.Path != "/v1/responses" || (label != "A" && label != "B") {
		http.Error(w, "unexpected synthetic provider request", http.StatusBadRequest)
		return
	}
	var request struct {
		Stream bool `json:"stream"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		http.Error(w, "invalid synthetic request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.providerCalls[label]++
	call := f.providerCalls[label]
	streamGate := f.providerStreamGate
	f.mu.Unlock()
	w.Header().Set("X-Request-ID", fmt.Sprintf("provider-%s-%d", label, call))
	response := fmt.Sprintf(`{"id":"resp_%s_%d","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"provider-%s"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`, label, call, label)
	if !request.Stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_%s_%d\",\"status\":\"in_progress\",\"model\":\"gpt-5.4\"}}\n\n", label, call)
	_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"provider-%s\"}\n\n", label)
	_ = http.NewResponseController(w).Flush()
	if streamGate != nil {
		select {
		case <-r.Context().Done():
			return
		case <-streamGate:
		}
	}
	_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
}

func (f *bridgeTopology) providerCount(label string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.providerCalls[label]
}

func (f *bridgeTopology) count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM "+table).Scan(&count))
	return count
}

func (f *bridgeTopology) waitReceipts(t *testing.T, expected int) {
	t.Helper()
	bridgeTopologyEventually(t, f.ctx, func() error {
		if count := f.count(t, f.source, "bridge.receipt_outbox"); count != expected {
			return fmt.Errorf("outbox=%d want=%d", count, expected)
		}
		return nil
	})
}

func (f *bridgeTopology) waitDrained(t *testing.T, expected int) {
	t.Helper()
	bridgeTopologyEventually(t, f.ctx, func() error {
		var total, unfinished, acked int
		err := f.source.QueryRowContext(f.ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE d.state IS DISTINCT FROM 'acked'), COUNT(*) FILTER (WHERE d.state='acked')
			FROM bridge.receipt_outbox o LEFT JOIN bridge.receipt_delivery d ON d.receipt_id=o.receipt_id AND d.consumer_id=$1`, topologyConsumer).Scan(&total, &unfinished, &acked)
		if err != nil {
			return err
		}
		if total != expected || unfinished != 0 || acked != expected || f.count(t, f.sink, "bridge_shadow.inbox") != expected || f.count(t, f.sink, "bridge_shadow.shadow_receipt_ledger") != expected {
			return fmt.Errorf("drain outbox=%d unfinished=%d acked=%d want=%d", total, unfinished, acked, expected)
		}
		return nil
	})
}

func (f *bridgeTopology) assertFacts(t *testing.T, expected int) {
	t.Helper()
	require.Equal(t, expected, f.count(t, f.source, "usage_billing_dedup"))
	require.Equal(t, expected, f.count(t, f.source, "bridge.receipt_outbox"))
	var balance, quota, rate, accountQuota, balanceSum, quotaSum, rateSum, accountSum float64
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT balance FROM users WHERE id=701`).Scan(&balance))
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT quota_used,usage_5h FROM api_keys WHERE id=702`).Scan(&quota, &rate))
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COALESCE((extra->>'quota_used')::numeric,0) FROM accounts WHERE id=704`).Scan(&accountQuota))
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COALESCE(SUM((billing->>'balance_cost')::numeric),0),COALESCE(SUM((billing->>'api_key_quota_cost')::numeric),0),COALESCE(SUM((billing->>'api_key_rate_limit_cost')::numeric),0),COALESCE(SUM((billing->>'account_quota_cost')::numeric),0) FROM bridge.receipt_outbox`).Scan(&balanceSum, &quotaSum, &rateSum, &accountSum))
	require.InDelta(t, float64(expected)*0.007, balanceSum, 1e-8)
	require.InDelta(t, 100-balanceSum, balance, 1e-8)
	require.InDelta(t, quotaSum, quota, 1e-8)
	require.InDelta(t, rateSum, rate, 1e-8)
	require.InDelta(t, accountSum, accountQuota, 1e-8)
	var unmatched int
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM bridge.receipt_outbox o
		LEFT JOIN usage_billing_dedup d ON d.request_id=o.core_request_id AND d.api_key_id=o.internal_key_id AND d.request_fingerprint=o.request_fingerprint
		LEFT JOIN bridge.forward_attempt a ON a.attempt_id=o.attempt_id AND a.reservation_id=o.reservation_id AND a.core_request_id=o.core_request_id AND a.internal_key_id=o.internal_key_id
		WHERE d.id IS NULL OR a.attempt_id IS NULL OR o.account_id<>704 OR o.user_id<>701 OR o.internal_key_id<>702 OR o.raw_usage->>'input_tokens'<>'3' OR o.raw_usage->>'output_tokens'<>'2'`).Scan(&unmatched))
	require.Zero(t, unmatched)
	rows, err := f.sink.QueryContext(f.ctx, `SELECT event_id,receipt_raw,payload_digest FROM bridge_shadow.shadow_receipt_ledger WHERE source=$1`, topologySource)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, raw, digest string
		require.NoError(t, rows.Scan(&id, &raw, &digest))
		var shadow custombridge.Receipt
		require.NoError(t, json.Unmarshal([]byte(raw), &shadow))
		receipt, err := custombridge.NewReceiptRepository(f.source).Get(f.ctx, shadow.CoreRequestID, shadow.InternalKeyID)
		require.NoError(t, err)
		encoded, err := json.Marshal(receipt)
		require.NoError(t, err)
		normalizedShadow, err := json.Marshal(shadow)
		require.NoError(t, err)
		require.Equal(t, id, receipt.ID)
		require.JSONEq(t, string(encoded), string(normalizedShadow))
		hash := sha256.Sum256([]byte(raw))
		require.Equal(t, hex.EncodeToString(hash[:]), digest)
		var matchingInbox int
		require.NoError(t, f.sink.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM bridge_shadow.inbox WHERE source=$1 AND event_id=$2 AND payload_digest=$3`, topologySource, id, digest).Scan(&matchingInbox))
		require.Equal(t, 1, matchingInbox)
	}
	require.NoError(t, rows.Err())
	require.Zero(t, f.count(t, f.sink, "bridge_shadow.inbox_conflicts"))
	require.Zero(t, f.providerCount("B"))
}

func (f *bridgeTopology) lastAttempt(t *testing.T) *custombridge.ForwardAttemptStatus {
	t.Helper()
	var id string
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT attempt_id FROM bridge.forward_attempt ORDER BY received_at DESC LIMIT 1`).Scan(&id))
	attempt, err := custombridge.NewForwardAttemptRepository(f.source).Get(f.ctx, topologyKeyID, id)
	require.NoError(t, err)
	return attempt
}

func (f *bridgeTopology) assertAttempt(t *testing.T, original *custombridge.ForwardAttemptStatus, state string) {
	t.Helper()
	attempt, err := custombridge.NewForwardAttemptRepository(f.source).Get(f.ctx, topologyKeyID, original.Envelope.AttemptID)
	require.NoError(t, err)
	require.Equal(t, state, attempt.State)
	require.Equal(t, original.CoreRequestID, attempt.CoreRequestID)
	require.Equal(t, original.ReceiptID, attempt.ReceiptID)
}

func (f *bridgeTopology) assertReplayRejected(t *testing.T, key string, stream bool, calls int) {
	t.Helper()
	for range 2 {
		result := f.request(f.ctx, f.edgeURL, topologyEdgeKey, key, stream)
		require.NoError(t, result.err)
		require.Equal(t, http.StatusConflict, result.status, result.body)
	}
	require.Equal(t, calls, f.providerCount("A"))
	require.Zero(t, f.providerCount("B"))
}

type bridgeTopologyGate struct {
	f    *bridgeTopology
	conn *sql.Conn
	name string
	key  int64
	pid  int
	once sync.Once
}

func (f *bridgeTopology) gate(t *testing.T, name, trigger string) *bridgeTopologyGate {
	t.Helper()
	key := int64(718201)
	conn, err := f.source.Conn(f.ctx)
	require.NoError(t, err)
	g := &bridgeTopologyGate{f: f, conn: conn, name: name, key: key}
	require.NoError(t, conn.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&g.pid))
	_, err = conn.ExecContext(f.ctx, `SELECT pg_advisory_lock($1)`, key)
	if err != nil {
		_ = conn.Close()
	}
	require.NoError(t, err)
	// 固定名称仅用于这个测试专属容器，不向 Core 注入任何自定义代码。
	_, err = f.source.ExecContext(f.ctx, fmt.Sprintf(`CREATE FUNCTION bridge.topology_%s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$;
		CREATE TRIGGER topology_%s %s EXECUTE FUNCTION bridge.topology_%s()`, name, key, name, trigger, name))
	require.NoError(t, err)
	t.Cleanup(func() { g.release(t) })
	return g
}

func (g *bridgeTopologyGate) wait(t *testing.T) {
	t.Helper()
	bridgeTopologyEventually(t, g.f.ctx, func() error {
		var count int
		if err := g.f.source.QueryRowContext(g.f.ctx, `SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>$1 AND wait_event_type='Lock' AND wait_event='advisory' AND $1=ANY(pg_blocking_pids(pid))`, g.pid).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("等待实际进程到达 %s 屏障，blocked=%d", g.name, count)
		}
		return nil
	})
}

func (g *bridgeTopologyGate) release(t *testing.T) {
	t.Helper()
	g.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := g.conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, g.key)
		if err != nil {
			t.Errorf("释放测试屏障: %v", err)
		}
		if err := g.conn.Close(); err != nil {
			t.Errorf("关闭屏障连接: %v", err)
		}
		_, err = g.f.source.ExecContext(ctx, "DROP FUNCTION bridge.topology_"+g.name+"() CASCADE")
		if err != nil {
			t.Errorf("清理测试屏障: %v", err)
		}
	})
}
