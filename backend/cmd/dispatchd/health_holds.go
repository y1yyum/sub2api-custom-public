package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

func runHealthHoldAction(ctx context.Context, store *dispatch.Store, cfg dispatch.Config, report bool, path string, stdout, stderr io.Writer) int {
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if report {
		holds, err := store.HealthHolds(operationCtx, cfg.CoreInstance, cfg.ConfigRevision)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 隔离报告不可用；核对配置版本、迁移及权限")
			return 1
		}
		if json.NewEncoder(stdout).Encode(holds) != nil {
			return 1
		}
		return 0
	}
	request, err := dispatch.LoadHoldReleaseRequest(path)
	if err != nil || request.CoreInstance != cfg.CoreInstance || request.ConfigRevision != cfg.ConfigRevision {
		_, _ = fmt.Fprintln(stderr, "dispatchd 隔离处置请求无效或配置身份不符")
		return 1
	}
	result, err := store.ReleaseHealthHold(operationCtx, request)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dispatchd 隔离处置失败；核对当前隔离版本、操作标识及处置依据")
		return 1
	}
	if json.NewEncoder(stdout).Encode(result) != nil {
		return 1
	}
	return 0
}
