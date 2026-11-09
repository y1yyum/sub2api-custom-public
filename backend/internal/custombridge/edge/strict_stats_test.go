package edge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type strictStatsRecorder struct {
	mu      sync.Mutex
	records []strictStatsRecord
}

func (w *strictStatsRecorder) SetWriteDeadline(time.Time) error { return nil }
func (w *strictStatsRecorder) Write(body []byte) (int, error) {
	var record strictStatsRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.records = append(w.records, record)
	return len(body), nil
}
func (w *strictStatsRecorder) snapshot() []strictStatsRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]strictStatsRecord(nil), w.records...)
}

func TestEdgeStrictStatsRuntimeLifecycle(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "strict-ok")
	}))
	defer core.Close()
	cfg := strictConfig(t, core.URL)
	writeShadowSnapshot(t, cfg.StrictDispatch.SnapshotFile, shadowSnapshot(time.Now()))
	output := &strictStatsRecorder{}
	run := startShadowStatsRun(t, cfg, output, 5*time.Millisecond)
	if result := readBody(t, edgePost(t, run.url, strictBody, nil)); result != "strict-ok" {
		t.Fatal("strict 输出影响转发")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		found := false
		for _, record := range output.snapshot() {
			if record.Phase == "periodic" && record.Stats.Selected == 1 && record.Stats.Released == 1 {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("周期输出未包含实际 strict 选择")
		}
		time.Sleep(5 * time.Millisecond)
	}
	run.stop(t)
	records := output.snapshot()
	if len(records) < 3 || records[0].Phase != "startup" || records[len(records)-1].Phase != "shutdown" {
		t.Fatal("strict 生命周期输出缺失")
	}
	last := records[len(records)-1].Stats
	if last.Acquisitions != 1 || last.Selected != 1 || last.Released != 1 || last.Inflight != 0 || !last.Enabled {
		t.Fatalf("strict 最终统计错误: %+v", last)
	}
	for _, record := range records {
		if record.Type != "bridge_edge_strict_stats" {
			t.Fatal("strict 与 shadow 统计类型混用")
		}
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{testBearer, testCoreKey, testSecret, "candidate-a", "credential-a", "fixture-model", "pool-fixture", "payer-fixture", "actor-fixture", "region-a", cfg.Grants[0].BearerSHA256} {
		if strings.Contains(string(raw), value) {
			t.Fatal("strict 输出包含身份或高基数字段")
		}
	}
	time.Sleep(15 * time.Millisecond)
	if len(output.snapshot()) != len(records) {
		t.Fatal("退出后继续写统计")
	}
}

func TestEdgeStrictStatsFailureDoesNotRetry(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "short"}[short], func(t *testing.T) {
			output := &shadowStatsFailWriter{short: short}
			run := startShadowStatsRun(t, strictConfig(t, "https://core.test"), output, 5*time.Millisecond)
			response := edgePost(t, run.url, strictBody, nil)
			_ = readBody(t, response)
			if response.StatusCode != 503 {
				t.Fatal("统计失败改变 strict 失败封闭语义")
			}
			time.Sleep(20 * time.Millisecond)
			run.stop(t)
			if output.calls.Load() != 1 {
				t.Fatal("短写或失败后重试统计")
			}
		})
	}
}

func TestEdgeStrictStatsDeadlineCapability(t *testing.T) {
	cfg := strictConfig(t, "https://core.test")
	cfg.Listen = "127.0.0.1:0"
	output := &unsupportedStatsWriter{}
	if err := RunWithStats(context.Background(), cfg, output); err == nil || output.writes.Load() != 0 {
		t.Fatal("不支持写期限仍启动或尝试写入")
	}
	if err := RunWithStats(context.Background(), cfg, nil); err == nil {
		t.Fatal("strict 统计接受空 writer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestEdgeStrictStatsFullPipeCancellation(t *testing.T) {
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
	run := startShadowStatsRun(t, strictConfig(t, "https://core.test"), writer, 5*time.Millisecond)
	response := edgePost(t, run.url, strictBody, nil)
	_ = readBody(t, response)
	if response.StatusCode != 503 {
		t.Fatal("阻塞输出影响请求热路径")
	}
	started := time.Now()
	run.stop(t)
	if time.Since(started) > time.Second+shadowStatsWriteTimeout {
		t.Fatal("满 pipe 下取消未受期限约束")
	}
	if _, err := writer.Stat(); err != nil {
		t.Fatal("关闭了调用方文件")
	}
}
