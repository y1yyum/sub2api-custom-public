package edge

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

const (
	shadowStatsInterval = 30 * time.Second
	edgeShutdownTimeout = 5 * time.Second
)

func Run(ctx context.Context, cfg Config) error {
	return runWithStats(ctx, cfg, nil, shadowStatsInterval)
}

// RunWithStats 要求输出实际支持写期限，否则在监听前返回错误。
// 运行期间独占文件写入和写期限，返回前恢复无期限，不关闭调用方文件。
func RunWithStats(ctx context.Context, cfg Config, output StatsWriter) error {
	if (cfg.ShadowDispatch != nil || cfg.StrictDispatch != nil) && output == nil {
		return errors.New("edge 统计输出必须支持可取消的写期限")
	}
	return runWithStats(ctx, cfg, output, shadowStatsInterval)
}

func runWithStats(ctx context.Context, cfg Config, output StatsWriter, interval time.Duration) error {
	handler, err := NewHandler(cfg)
	if err != nil {
		return err
	}
	var reporter shadowStatsReporter
	report := func(ctx context.Context, phase string) {
		if handler.strict != nil {
			reporter.writeRecord(ctx, strictStatsRecord{Type: "bridge_edge_strict_stats", Phase: phase, Stats: handler.StrictStats()})
		} else {
			reporter.write(ctx, phase, handler.ShadowStats())
		}
	}
	defer func() {
		handler.Close()
		finalCtx, cancel := context.WithTimeout(context.Background(), shadowStatsWriteTimeout)
		defer cancel()
		report(finalCtx, "shutdown")
	}()
	if (handler.shadow != nil || handler.strict != nil) && output != nil {
		if err := output.SetWriteDeadline(time.Time{}); err != nil {
			return errors.New("edge 统计输出不支持可取消的写期限")
		}
		reporter.output = output
	}
	listener, err := net.Listen("tcp", handler.cfg.Listen)
	if err != nil {
		return errors.New("edge 监听失败")
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	var ticks <-chan time.Time
	if reporter.output != nil {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		ticks = ticker.C
		report(ctx, "startup")
	}
	for {
		select {
		case <-ticks:
			report(ctx, "periodic")
		case err := <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				return errors.New("edge 服务异常停止")
			}
			return nil
		case <-ctx.Done():
			handler.shadow.close()
			handler.strict.close()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), edgeShutdownTimeout)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				_ = server.Close()
			}
			<-done
			return nil
		}
	}
}
