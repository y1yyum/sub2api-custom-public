package edge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
)

func shadowConfig(t *testing.T, origin string) Config {
	t.Helper()
	cfg := testConfig(origin)
	cfg.Grants[0].Region = "region-a"
	cfg.ShadowDispatch = &ShadowDispatchConfig{
		SnapshotFile: filepath.Join(t.TempDir(), "snapshot.json"), CoreInstance: "core-fixture",
		PollIntervalMilliseconds: 30000,
	}
	return cfg
}

func shadowSnapshot(now time.Time) dispatch.Snapshot {
	return dispatch.Snapshot{
		Version: 1, CoreInstance: "core-fixture", Revision: 10,
		GeneratedAt: now.Add(-time.Second), ExpiresAt: now.Add(20 * time.Second),
		Candidates: []dispatch.Candidate{{
			Target: dispatch.Target{
				Ref: "candidate-a", PoolRef: "pool-fixture", PolicyRevision: "policy-1",
				Model: "fixture-model", Region: "region-a", CredentialRef: "credential-a", CredentialRevision: 1,
				ProviderRef: "provider-fixture", AccountID: 7, BaseWeight: 1, MaxInflight: 10, Enabled: true,
			},
			State: dispatch.Healthy, EffectiveWeight: 1, Reason: "healthy",
		}},
	}
}

func writeShadowSnapshot(t *testing.T, path string, snapshot dispatch.Snapshot) {
	t.Helper()
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	temporary := path + ".next"
	if err := os.WriteFile(temporary, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func waitShadowStats(t *testing.T, handler *Handler, condition func(ShadowStats) bool) ShadowStats {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		stats := handler.ShadowStats()
		if condition(stats) {
			return stats
		}
		select {
		case <-deadline.C:
			t.Fatalf("影子统计未达到预期: %+v", stats)
		case <-ticker.C:
		}
	}
}

func TestEdgeShadowDefaultDisabled(t *testing.T) {
	cfg := testConfig("https://core.test")
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	if handler.shadow != nil || handler.ShadowStats() != (ShadowStats{}) {
		t.Fatal("默认开启了影子调度")
	}
}

func TestEdgeShadowRefreshKeepsLastValidUntilExpiry(t *testing.T) {
	now := time.Now()
	cfg := shadowConfig(t, "https://core.test")
	snapshot := shadowSnapshot(now)
	writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, snapshot)
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	grant := cfg.Grants[0]
	handler.shadow.observe(grant, "fixture-model", now)
	for _, name := range []string{"missing", "corrupt", "rollback", "same_revision_conflict", "expired", "wrong_core", "same_revision"} {
		t.Run(name, func(t *testing.T) {
			before := handler.ShadowStats()
			bad := shadowSnapshot(now)
			switch name {
			case "missing":
				if err := os.Remove(cfg.ShadowDispatch.SnapshotFile); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(cfg.ShadowDispatch.SnapshotFile, []byte(`{"bearer":"must-not-log"`), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				switch name {
				case "rollback":
					bad.Revision--
				case "same_revision_conflict":
					bad.Candidates[0].AccountID = grant.AccountID
				case "expired":
					bad.Revision++
					bad.ExpiresAt = now
				case "wrong_core":
					bad.Revision++
					bad.CoreInstance = "other-core"
				}
				writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, bad)
			}
			handler.shadow.refresh(now)
			handler.shadow.observe(grant, "fixture-model", now)
			after := handler.ShadowStats()
			if after.SnapshotRevision != snapshot.Revision || after.Mismatches != before.Mismatches+1 || after.Unavailable != 0 {
				t.Fatalf("失败刷新替换了最后有效快照: %+v", after)
			}
			if name == "same_revision" {
				if !after.LastRefreshOK || after.RefreshSuccesses != before.RefreshSuccesses+1 {
					t.Fatal("拒绝了内容相同的同版本快照")
				}
			} else if after.LastRefreshOK || after.RefreshFailures != before.RefreshFailures+1 {
				t.Fatal("无效快照没有计入失败")
			}
		})
	}
	handler.shadow.observe(grant, "fixture-model", snapshot.ExpiresAt)
	stats := handler.ShadowStats()
	if stats.Unavailable != 1 || stats.SnapshotExpired != 1 {
		t.Fatalf("过期快照仍选中了账号: %+v", stats)
	}
	next := shadowSnapshot(now)
	next.Revision++
	next.Candidates[0].AccountID = grant.AccountID
	writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, next)
	handler.shadow.refresh(now)
	handler.shadow.observe(grant, "fixture-model", now)
	if stats := handler.ShadowStats(); stats.SnapshotRevision != next.Revision || stats.Matches != 1 {
		t.Fatalf("新版本未恢复选号: %+v", stats)
	}
}

func TestEdgeShadowBackgroundRefreshAndOutage(t *testing.T) {
	cfg := shadowConfig(t, "https://core.test")
	cfg.ShadowDispatch.PollIntervalMilliseconds = 10
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	if stats := handler.ShadowStats(); stats.SnapshotRevision != 0 || stats.LastRefreshOK || stats.RefreshFailures != 1 {
		t.Fatalf("无文件启动没有记录不可用: %+v", stats)
	}
	handler.shadow.observe(cfg.Grants[0], "fixture-model", time.Now())
	if handler.ShadowStats().SnapshotUnavailable != 1 {
		t.Fatal("无快照选择未记为 unavailable")
	}
	snapshot := shadowSnapshot(time.Now())
	writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, snapshot)
	waitShadowStats(t, handler, func(s ShadowStats) bool { return s.SnapshotRevision == 10 && s.LastRefreshOK })
	if err := os.Remove(cfg.ShadowDispatch.SnapshotFile); err != nil {
		t.Fatal(err)
	}
	waitShadowStats(t, handler, func(s ShadowStats) bool { return !s.LastRefreshOK })
	handler.shadow.observe(cfg.Grants[0], "fixture-model", time.Now())
	if stats := handler.ShadowStats(); stats.Mismatches != 1 || stats.SnapshotRevision != 10 {
		t.Fatal("控制面文件失联立即丢弃了有效快照")
	}
	if err := os.WriteFile(cfg.ShadowDispatch.SnapshotFile, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	before := handler.ShadowStats().RefreshFailures
	waitShadowStats(t, handler, func(s ShadowStats) bool { return s.RefreshFailures > before })
	snapshot.Revision++
	snapshot.Candidates[0].AccountID = cfg.Grants[0].AccountID
	writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, snapshot)
	waitShadowStats(t, handler, func(s ShadowStats) bool { return s.SnapshotRevision == 11 && s.LastRefreshOK })
	handler.shadow.observe(cfg.Grants[0], "fixture-model", time.Now())
	if handler.ShadowStats().Matches != 1 {
		t.Fatal("文件恢复后未刷新")
	}
}

func TestEdgeShadowCloseJoinsRefreshAndFreezesConfiguration(t *testing.T) {
	cfg := shadowConfig(t, "https://core.test")
	cfg.ShadowDispatch.PollIntervalMilliseconds = 10
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.Close)
	path := cfg.ShadowDispatch.SnapshotFile
	cfg.ShadowDispatch.SnapshotFile, cfg.ShadowDispatch.CoreInstance = "absent", "different-core"
	cfg.ShadowDispatch.PollIntervalMilliseconds = -1
	writeShadowSnapshot(t, path, shadowSnapshot(time.Now()))
	waitShadowStats(t, handler, func(s ShadowStats) bool { return s.SnapshotRevision == 10 })
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(handler.Close)
	}
	workers.Wait()
	select {
	case <-handler.shadow.done:
	default:
		t.Fatal("Close 返回时刷新 goroutine 未退出")
	}
	before := handler.ShadowStats()
	snapshot := shadowSnapshot(time.Now())
	snapshot.Revision++
	writeShadowSnapshot(t, path, snapshot)
	time.Sleep(30 * time.Millisecond)
	if handler.ShadowStats() != before {
		t.Fatal("Close 返回后仍在刷新")
	}
}

func TestEdgeShadowConcurrentRefreshAndObservation(t *testing.T) {
	cfg := shadowConfig(t, "https://core.test")
	cfg.ShadowDispatch.PollIntervalMilliseconds = 10
	writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, shadowSnapshot(time.Now()))
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for i := range 200 {
				model := "fixture-model"
				if worker%2 == 0 {
					model = strings.Repeat("x", i+1)
				}
				handler.shadow.observe(cfg.Grants[0], model, time.Now())
				_ = handler.ShadowStats()
			}
		})
	}
	for revision := int64(11); revision <= 30; revision++ {
		snapshot := shadowSnapshot(time.Now())
		snapshot.Revision = revision
		writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, snapshot)
		handler.shadow.refresh(time.Now())
	}
	workers.Wait()
	stats := handler.ShadowStats()
	if stats.Observations != 1600 || stats.Mismatches != 800 || stats.NoEligibleRoute != 800 || stats.Unavailable != 800 {
		t.Fatalf("并发观察丢失计数或路由污染: %+v", stats)
	}
	assertShadowStatsJSON(t, stats)
	raw, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{testBearer, testCoreKey, testSecret, "actor-fixture", "payer-fixture", "candidate-a", "fixture-model", "pool-fixture", "region-a"} {
		if strings.Contains(string(raw), value) {
			t.Fatalf("聚合统计包含请求或高基数字段: %s", value)
		}
	}
}

func TestEdgeRunCancellationWithShadow(t *testing.T) {
	cfg := shadowConfig(t, "https://core.test")
	cfg.Listen = "127.0.0.1:0"
	cfg.ShadowDispatch.PollIntervalMilliseconds = 10
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

type shadowStatsRecorder struct {
	mu         sync.Mutex
	records    []shadowStatsRecord
	raw        []json.RawMessage
	active     atomic.Int32
	concurrent atomic.Bool
}

func (w *shadowStatsRecorder) SetWriteDeadline(time.Time) error { return nil }

func (w *shadowStatsRecorder) Write(body []byte) (int, error) {
	if w.active.Add(1) != 1 {
		w.concurrent.Store(true)
	}
	defer w.active.Add(-1)
	var record shadowStatsRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return 0, err
	}
	w.mu.Lock()
	w.records = append(w.records, record)
	w.raw = append(w.raw, append(json.RawMessage(nil), body...))
	w.mu.Unlock()
	return len(body), nil
}

func (w *shadowStatsRecorder) snapshot() []shadowStatsRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]shadowStatsRecord(nil), w.records...)
}

func (w *shadowStatsRecorder) rawSnapshot() []json.RawMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]json.RawMessage(nil), w.raw...)
}

type shadowStatsTestRun struct {
	url    string
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func (r *shadowStatsTestRun) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	select {
	case <-r.done:
		if r.err != nil {
			t.Fatal(r.err)
		}
	case <-time.After(edgeShutdownTimeout + 2*shadowStatsWriteTimeout + 2*time.Second):
		t.Fatal("统计运行循环超过 HTTP 关闭、最终输出及调度余量预算")
	}
}

func startShadowStatsRun(t *testing.T, cfg Config, output StatsWriter, interval time.Duration) *shadowStatsTestRun {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Listen = listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &shadowStatsTestRun{url: "http://" + cfg.Listen, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(run.done)
		run.err = runWithStats(ctx, cfg, output, interval)
	}()
	t.Cleanup(func() { run.stop(t) })
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 200 * time.Millisecond, Transport: transport}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := client.Get(run.url + "/missing")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != 404 {
				t.Fatal("服务探测响应错误")
			}
			return run
		}
		select {
		case <-run.done:
			t.Fatalf("统计服务提前退出: %v", run.err)
		case <-deadline.C:
			t.Fatal("统计服务未监听")
		case <-ticker.C:
		}
	}
}

func TestEdgeShadowStatsRuntimePeriodicAndShutdown(t *testing.T) {
	verifier := fixtureVerifier(t)
	var calls atomic.Int64
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
		if err != nil || envelope.AccountID != 42 {
			t.Error("统计输出改变了固定账号或签名")
			w.WriteHeader(401)
			return
		}
		calls.Add(1)
		_, _ = io.WriteString(w, "fixed-account-b")
	}))
	defer core.Close()
	cfg := shadowConfig(t, core.URL)
	cfg.ShadowDispatch.SelectionSeed = &dispatch.SelectionSeed{GatewayInstance: "private-gateway-seed", ShardRef: "private-shard-seed"}
	writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, shadowSnapshot(time.Now()))
	output := &shadowStatsRecorder{}
	run := startShadowStatsRun(t, cfg, output, 5*time.Millisecond)
	for range 3 {
		if body := readBody(t, edgePost(t, run.url, textBody, nil)); body != "fixed-account-b" {
			t.Fatal("真实请求被观测输出干扰")
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		found := false
		for _, record := range output.snapshot() {
			if record.Phase == "periodic" && record.Stats.Mismatches == 3 {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("周期输出未包含实际影子观察")
		}
		time.Sleep(5 * time.Millisecond)
	}
	run.stop(t)
	records := output.snapshot()
	if len(records) < 3 || records[0].Phase != "startup" || records[len(records)-1].Phase != "shutdown" {
		t.Fatalf("生命周期统计缺失: %+v", records)
	}
	final := records[len(records)-1].Stats
	if final.Observations != 3 || final.Mismatches != 3 || final.Unavailable != 0 || calls.Load() != 3 {
		t.Fatalf("退出统计不完整或发生重试: %+v", final)
	}
	for _, record := range records {
		if record.Type != "bridge_edge_shadow_stats" || (record.Phase != "startup" && record.Phase != "periodic" && record.Phase != "shutdown") {
			t.Fatalf("出现非固定事件: %+v", record)
		}
		assertShadowStatsJSON(t, record.Stats)
	}
	for _, body := range output.rawSnapshot() {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		assertShadowJSONKeys(t, fields, []string{"type", "phase", "stats"})
		assertShadowStatsJSONBody(t, fields["stats"])
		assertShadowStatsPrivacy(t, body)
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{testBearer, testCoreKey, testSecret, textBody, "actor-fixture", "payer-fixture", "grant-fixture", "candidate-a", "fixture-model", "pool-fixture", "region-a", cfg.Grants[0].BearerSHA256} {
		if strings.Contains(string(raw), value) {
			t.Fatal("运行时统计泄漏敏感或高基数字段")
		}
	}
	time.Sleep(20 * time.Millisecond)
	if len(output.snapshot()) != len(records) || output.concurrent.Load() {
		t.Fatal("退出后仍写输出或存在并发 writer")
	}
}

func TestEdgeShadowStatsRuntimeDisabledIsSilent(t *testing.T) {
	output := &shadowStatsRecorder{}
	run := startShadowStatsRun(t, testConfig("https://core.test"), output, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	run.stop(t)
	if len(output.snapshot()) != 0 {
		t.Fatal("未配置 shadow 时有新增输出")
	}
}

func TestEdgeShadowStatsFullPipeCancellationIsBounded(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	defer core.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	if err := writer.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(make([]byte, 1<<20)); !os.IsTimeout(err) {
		t.Fatalf("未填满真实 pipe: %v", err)
	}
	if err := writer.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	run := startShadowStatsRun(t, shadowConfig(t, core.URL), writer, 5*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, run.url+"/v1/responses", strings.NewReader(textBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		t.Fatalf("聚合输出阻塞了请求热路径: %v", err)
	}
	if readBody(t, response) != "ok" {
		t.Fatal("聚合输出改变了请求结果")
	}
	started := time.Now()
	run.stop(t)
	if elapsed := time.Since(started); elapsed > time.Second+shadowStatsWriteTimeout {
		t.Fatalf("满 pipe 取消未及时返回: %s", elapsed)
	}
	if _, err := writer.Stat(); err != nil {
		t.Fatal("关闭了调用方输出文件")
	}
}

type callbackDeadlineWriter struct {
	net.Conn
	writing chan struct{}
	started chan struct{}
	release chan struct{}
	reset   chan struct{}
	once    sync.Once
}

func (w *callbackDeadlineWriter) Write(body []byte) (int, error) {
	close(w.writing)
	return w.Conn.Write(body)
}

func (w *callbackDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	err := w.Conn.SetWriteDeadline(deadline)
	if deadline.IsZero() {
		close(w.reset)
	} else if !deadline.After(time.Now()) {
		w.once.Do(func() { close(w.started); <-w.release })
	}
	return err
}

func TestEdgeShadowStatsCancellationJoinsCallbackBeforeReset(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = left.Close(); _ = right.Close() }()
	writer := &callbackDeadlineWriter{Conn: left, writing: make(chan struct{}), started: make(chan struct{}), release: make(chan struct{}), reset: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(writer.release) }) }
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reporter := shadowStatsReporter{output: writer}
	done := make(chan struct{})
	go func() { defer close(done); reporter.write(ctx, "periodic", ShadowStats{Enabled: true}) }()
	select {
	case <-writer.writing:
	case <-time.After(time.Second):
		t.Fatal("未进入真实阻塞 Write")
	}
	cancel()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("取消未设置写期限")
	}
	select {
	case <-writer.reset:
		t.Fatal("回调未结束就复位期限")
	default:
	}
	select {
	case <-done:
		t.Fatal("回调未结束就返回")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("写入或取消回调未回收")
	}
	select {
	case <-writer.reset:
	default:
		t.Fatal("回调结束后没有复位期限")
	}
}

type shadowStatsFailWriter struct {
	short bool
	calls atomic.Int64
}

func (w *shadowStatsFailWriter) SetWriteDeadline(time.Time) error { return nil }

func (w *shadowStatsFailWriter) Write(body []byte) (int, error) {
	w.calls.Add(1)
	if w.short {
		return len(body) - 1, nil
	}
	return 0, errors.New("sensitive-fixture-output-error-must-not-log")
}

func TestEdgeShadowStatsOutputFailureDoesNotRetryOrBlockTraffic(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "short_write"}[short], func(t *testing.T) {
			verifier := fixtureVerifier(t)
			var calls atomic.Int64
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
				if err != nil || envelope.AccountID != 42 || envelope.ActorRef != "actor-fixture" || envelope.PayerRef != "payer-fixture" {
					t.Error("输出失败改变了授权")
					w.WriteHeader(401)
					return
				}
				calls.Add(1)
				_, _ = io.WriteString(w, "ok")
			}))
			defer core.Close()
			output := &shadowStatsFailWriter{short: short}
			run := startShadowStatsRun(t, shadowConfig(t, core.URL), output, 5*time.Millisecond)
			if readBody(t, edgePost(t, run.url, textBody, nil)) != "ok" {
				t.Fatal("统计失败影响实际请求")
			}
			time.Sleep(20 * time.Millisecond)
			run.stop(t)
			if calls.Load() != 1 || output.calls.Load() != 1 {
				t.Fatal("输出失败后重试了输出或 Core 请求")
			}
		})
	}
}

func TestEdgeShadowStatsReportingWithConcurrentClose(t *testing.T) {
	cfg := shadowConfig(t, "https://core.test")
	cfg.ShadowDispatch.PollIntervalMilliseconds = 10
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	output := &shadowStatsRecorder{}
	reporter := shadowStatsReporter{output: output}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(handler.Close)
	}
	workers.Go(func() {
		for range 200 {
			handler.shadow.observe(cfg.Grants[0], "fixture-model", time.Now())
		}
	})
	for range 200 {
		reporter.write(context.Background(), "periodic", handler.ShadowStats())
	}
	workers.Wait()
	reporter.write(context.Background(), "shutdown", handler.ShadowStats())
	records := output.snapshot()
	if len(records) != 201 || records[200].Stats.Observations != 200 || output.concurrent.Load() {
		t.Fatal("并发关闭导致统计丢失、并发写或输出异常")
	}
}

type unsupportedStatsWriter struct{ writes atomic.Int64 }

func (w *unsupportedStatsWriter) Write(body []byte) (int, error) {
	w.writes.Add(1)
	return len(body), nil
}
func (w *unsupportedStatsWriter) SetWriteDeadline(time.Time) error { return os.ErrNoDeadline }

func TestEdgeShadowStatsUnsupportedOutputFailsBeforeServing(t *testing.T) {
	cfg := shadowConfig(t, "https://core.test")
	cfg.Listen = "127.0.0.1:0"
	output := &unsupportedStatsWriter{}
	if err := RunWithStats(context.Background(), cfg, output); err == nil {
		t.Fatal("不支持写期限时静默丢弃统计")
	}
	if output.writes.Load() != 0 {
		t.Fatal("调用了不支持取消的 writer")
	}
	if err := RunWithStats(context.Background(), cfg, nil); err == nil {
		t.Fatal("启用统计却接受空输出")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, cfg); err != nil {
		t.Fatal("兼容的无统计 Run 路径失效")
	}
}

func TestEdgeShutdownWithUnstartedConnectionIsBounded(t *testing.T) {
	run := startShadowStatsRun(t, testConfig("https://core.test"), nil, 5*time.Millisecond)
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(run.url, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get(run.url + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	started := time.Now()
	run.stop(t)
	elapsed := time.Since(started)
	t.Logf("保留未发送首个 HTTP 字节的 TCP 连接，Run 关闭耗时 %s", elapsed)
	if elapsed < 3*time.Second || elapsed > edgeShutdownTimeout+2*time.Second {
		t.Fatalf("未复现 StateNew 等待或关闭失去上界: %s", elapsed)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("关闭后未收回未开始请求的 TCP 连接")
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("Run 已返回但 TCP 连接仍未关闭")
	}
}
