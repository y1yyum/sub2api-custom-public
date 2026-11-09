package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"golang.org/x/net/netutil"
)

const (
	retentionInterval    = time.Minute
	maintenanceTimeout   = 30 * time.Second
	metricsMinInterval   = 100 * time.Millisecond
	metricsMaxConns      = 4
	metricsShutdownGrace = 2 * time.Second
)

// monitor 在工作轮次之后执行显式启用的聚合、清理和现状读取；失败只计数并报告，不影响快照发布。
type monitor struct {
	cfg           dispatch.MonitoringConfig
	core          string
	revision      int64
	store         *dispatch.Store
	metrics       *dispatch.ProcessMetrics
	lastRetention time.Time
	maintain      func(context.Context, io.Writer) error
}

func newMonitor(cfg dispatch.Config, store *dispatch.Store) *monitor {
	if cfg.Monitoring == nil {
		return nil
	}
	m := &monitor{cfg: *cfg.Monitoring, core: cfg.CoreInstance, revision: cfg.ConfigRevision, store: store, metrics: dispatch.NewProcessMetrics()}
	m.maintain = m.afterRound
	return m
}

// runRounds 执行工作轮次。--once 同步维护并以失败退出 1；持续模式下维护在独立 goroutine 单飞执行，
// 慢维护只合并后续触发，不推迟下一轮发布。round 返回 done=true 时以其退出码结束。
func runRounds(ctx context.Context, once bool, poll time.Duration, m *monitor, stderr io.Writer, round func() (int, bool)) int {
	trigger := func() {}
	if m != nil && !once {
		var stop func()
		trigger, stop = m.start(ctx, stderr)
		defer stop()
	}
	for {
		if code, done := round(); done {
			return code
		}
		if m != nil && ctx.Err() == nil {
			if once {
				if m.maintain(ctx, stderr) != nil {
					return 1
				}
			} else {
				trigger()
			}
		}
		if once {
			return 0
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0
		case <-timer.C:
		}
	}
}

func (m *monitor) start(ctx context.Context, stderr io.Writer) (func(), func()) {
	ctx, cancel := context.WithCancel(ctx)
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				if ctx.Err() != nil {
					return
				}
				_ = m.maintain(ctx, stderr)
			}
		}
	}()
	trigger := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	stop := func() {
		cancel()
		<-done
	}
	return trigger, stop
}

// continuousStderr 在持续模式存在后台写入者（维护 goroutine 或硬失效 watcher）时返回加锁 writer；
// 须在启动这些写入者之前调用，使它们与轮次循环共用同一把锁。
func continuousStderr(cfg dispatch.Config, m *monitor, stderr io.Writer) io.Writer {
	if m == nil && (cfg.SnapshotServer == nil || cfg.SnapshotServer.HardInvalidation == nil) {
		return stderr
	}
	return &lockedWriter{w: stderr}
}

// lockedWriter 串行化轮次循环与维护 goroutine 对同一 stderr 的写入。
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (m *monitor) afterRound(ctx context.Context, stderr io.Writer) error {
	var failed error
	run := func(task, message string, action func(context.Context) error) {
		operationCtx, cancel := context.WithTimeout(ctx, maintenanceTimeout)
		err := action(operationCtx)
		cancel()
		// 停止进程触发的取消不是维护失败，不计数也不输出。
		if ctx.Err() != nil {
			return
		}
		m.metrics.RecordMaintenance(task, err)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, message)
			failed = errors.Join(failed, err)
		}
	}
	if m.cfg.Aggregation != nil {
		run("rollup", "dispatchd 指标聚合失败；核对 006 迁移、权限或持锁超时，下一轮重试", func(ctx context.Context) error {
			_, err := m.store.RollupMetrics(ctx, m.core)
			return err
		})
	}
	if (m.cfg.Retention != nil || m.cfg.Aggregation != nil) && time.Since(m.lastRetention) >= retentionInterval {
		m.lastRetention = time.Now()
		run("retention", "dispatchd 历史清理失败；已删除批次保持提交，下一轮继续", func(ctx context.Context) error {
			results, err := m.store.ApplyRetention(ctx, m.core, m.cfg)
			m.metrics.RecordRetention(results)
			return err
		})
	}
	if m.cfg.MetricsListen != "" {
		run("state", "dispatchd 指标现状读取失败", func(ctx context.Context) error {
			state, err := m.store.MetricsState(ctx, m.core, m.revision)
			if err == nil {
				m.metrics.SetState(state)
			}
			return err
		})
	}
	return failed
}

type metricsHandler struct {
	metrics *dispatch.ProcessMetrics
	mu      sync.Mutex
	last    time.Time
}

func (h *metricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/metrics" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.mu.Lock()
	now := time.Now()
	limited := now.Sub(h.last) < metricsMinInterval
	if !limited {
		h.last = now
	}
	h.mu.Unlock()
	if limited {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	body := h.metrics.Render()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

// serveMetrics 只绑定校验过的回环地址或新建的 Unix socket；已存在的 socket 路径不会被替换。
func serveMetrics(listen string, metrics *dispatch.ProcessMetrics) (func(), error) {
	network, address, err := dispatch.MetricsListenAddress(listen)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	if network == "unix" {
		if err := os.Chmod(address, 0o600); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	server := &http.Server{Handler: &metricsHandler{metrics: metrics}, ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 4 << 10}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(netutil.LimitListener(listener, metricsMaxConns))
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), metricsShutdownGrace)
		defer cancel()
		if server.Shutdown(ctx) != nil {
			_ = server.Close()
		}
		<-done
	}, nil
}

func runMetricsReport(ctx context.Context, store *dispatch.Store, cfg dispatch.Config, stdout, stderr io.Writer) int {
	reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	report, err := store.MetricsReport(reportCtx, cfg.CoreInstance, cfg.ConfigRevision)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dispatchd 指标报告不可用；核对配置版本、006 迁移及权限")
		return 1
	}
	if json.NewEncoder(stdout).Encode(report) != nil {
		return 1
	}
	return 0
}
