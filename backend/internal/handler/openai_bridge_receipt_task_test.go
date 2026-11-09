package handler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBridgeReceiptUsageTaskSurvivesDropPolicy(t *testing.T) {
	for _, managed := range []bool{false, true} {
		name := "ordinary"
		if managed {
			name = "managed"
		}
		t.Run(name, func(t *testing.T) {
			pool := newFullBridgeReceiptDropPool(t)

			parent := context.Background()
			if managed {
				parent = custombridge.WithRequest(parent, custombridge.Request{
					ReservationID: "reservation-drop",
					AttemptID:     "attempt-drop",
					AccountID:     1,
				})
			}
			parent, cancel := context.WithCancel(parent)
			cancel()
			handler := &OpenAIGatewayHandler{usageRecordWorkerPool: pool}
			var called atomic.Bool
			handler.submitOpenAIUsageRecordTask(parent, &service.OpenAIForwardResult{}, func(ctx context.Context) {
				require.NoError(t, ctx.Err(), "billing must outlive client cancellation")
				called.Store(true)
			})
			require.Equal(t, managed, called.Load(), "managed billing must survive overflow; ordinary policy must remain unchanged")
		})
	}
}

func newFullBridgeReceiptDropPool(t *testing.T) *service.UsageRecordWorkerPool {
	t.Helper()
	pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{
		WorkerCount: 1, QueueSize: 1, TaskTimeout: time.Minute,
		OverflowPolicy: config.UsageRecordOverflowPolicyDrop,
	})
	t.Cleanup(pool.Stop)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := make(chan struct{})
	require.Equal(t, service.UsageRecordSubmitModeEnqueued, pool.Submit(func(context.Context) {
		close(started)
		<-release
	}))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	require.Equal(t, service.UsageRecordSubmitModeEnqueued, pool.Submit(func(context.Context) {}))
	return pool
}
