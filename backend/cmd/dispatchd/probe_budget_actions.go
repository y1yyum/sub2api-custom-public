package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

func runProbeBudgetAction(ctx context.Context, store *dispatch.Store, cfg dispatch.Config, report bool, path string, stdout, stderr io.Writer) int {
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if report {
		result, err := store.ProbeBudgetReport(operationCtx, cfg.CoreInstance, cfg.ConfigRevision)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "dispatchd 探针金额预算报告不可用；核对配置版本、迁移及权限")
			return 1
		}
		if json.NewEncoder(stdout).Encode(result) != nil {
			return 1
		}
		return 0
	}
	request, err := dispatch.LoadProbeBudgetCloseRequest(path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dispatchd 探针金额处置请求无效；须提供版本、操作者及处置依据")
		return 1
	}
	result, err := store.CloseProbeBudget(operationCtx, cfg.CoreInstance, cfg.ConfigRevision, request)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dispatchd 探针金额处置失败；核对租约、预留版本及处置依据")
		return 1
	}
	if json.NewEncoder(stdout).Encode(result) != nil {
		return 1
	}
	return 0
}
