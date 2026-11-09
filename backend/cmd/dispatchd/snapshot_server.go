package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

// startSnapshotServer 只在 --run 且显式配置 snapshot_server 时监听同机 UDS。
// 配置 hard_invalidation 时同一监听器另挂失效路径，并启动只读 hold 轮询；未配置时与原服务一致。
func startSnapshotServer(ctx context.Context, cfg dispatch.Config, store *dispatch.Store, stderr io.Writer) (*dispatch.SnapshotServer, func(), error) {
	keys, err := dispatch.LoadSnapshotSyncKeys(cfg.SnapshotServer.KeysFile)
	if err != nil {
		return nil, nil, err
	}
	server, err := dispatch.NewSnapshotServer(cfg.CoreInstance, *cfg.SnapshotServer, keys)
	if err != nil {
		return nil, nil, err
	}
	var handler http.Handler = server
	var feed *dispatch.InvalidationFeed
	connections := 2 * cfg.SnapshotServer.MaxConcurrent
	if hard := cfg.SnapshotServer.HardInvalidation; hard != nil {
		if feed, err = dispatch.NewInvalidationFeed(cfg.CoreInstance, *cfg.SnapshotServer, keys); err != nil {
			return nil, nil, err
		}
		handler, connections = dispatch.NewSyncHandler(server, feed), 3*cfg.SnapshotServer.MaxConcurrent
	}
	listener, err := dispatch.ListenSnapshotSocket(cfg.SnapshotServer.SocketPath, connections)
	if err != nil {
		return nil, nil, err
	}
	httpServer := dispatch.NewSnapshotHTTPServer(handler)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = httpServer.Serve(listener)
	}()
	watchCtx, stopWatch := context.WithCancel(ctx)
	watched := make(chan struct{})
	if feed == nil {
		close(watched)
	} else {
		go func() {
			defer close(watched)
			failing := false
			interval := time.Duration(cfg.SnapshotServer.HardInvalidation.PollIntervalMilliseconds) * time.Millisecond
			dispatch.WatchHoldInvalidations(watchCtx, store, cfg.CoreInstance, interval, feed, func(err error) {
				if err != nil && !failing {
					if errors.Is(err, dispatch.ErrInvalidationCoreMissing) {
						_, _ = fmt.Fprintln(stderr, "dispatchd 硬失效轮询失败：找不到配置的 core 行；失效改由下一份快照传播")
					} else {
						_, _ = fmt.Fprintln(stderr, "dispatchd 硬失效轮询失败；失效改由下一份快照传播")
					}
				}
				failing = err != nil
			})
		}()
	}
	return server, func() {
		stopWatch()
		<-watched
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = httpServer.Close()
		}
		<-served
	}, nil
}

func runRotatePublishLineage(ctx context.Context, store *dispatch.Store, stdout, stderr io.Writer) int {
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lineage, err := store.RotatePublishLineage(operationCtx)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dispatchd 发布 lineage 轮换失败；核对 005 迁移及权限")
		return 1
	}
	if json.NewEncoder(stdout).Encode(struct {
		PublishLineage string `json:"publish_lineage"`
	}{lineage}) != nil {
		return 1
	}
	return 0
}
