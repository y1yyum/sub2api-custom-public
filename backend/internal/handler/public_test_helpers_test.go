//go:build unit || integration

package handler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type openAIWSFailoverHandlerAccountRepoStub struct {
	service.AccountRepository
	accounts       []service.Account
	rateLimitedIDs []int64
}

func (s *openAIWSFailoverHandlerAccountRepoStub) ListSchedulableByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	out := make([]service.Account, 0, len(s.accounts))
	for _, account := range s.accounts {
		if account.Platform == platform && account.IsSchedulable() {
			out = append(out, account)
		}
	}
	return out, nil
}

func (s *openAIWSFailoverHandlerAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]service.Account, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

func (s *openAIWSFailoverHandlerAccountRepoStub) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

func (s *openAIWSFailoverHandlerAccountRepoStub) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	for _, account := range s.accounts {
		if account.ID == id {
			acc := account
			return &acc, nil
		}
	}
	return nil, nil
}

func (s *openAIWSFailoverHandlerAccountRepoStub) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	s.rateLimitedIDs = append(s.rateLimitedIDs, id)
	for i := range s.accounts {
		if s.accounts[i].ID == id {
			reset := resetAt
			s.accounts[i].RateLimitResetAt = &reset
			break
		}
	}
	return nil
}

type openAIWSUsageHandlerUsageLogRepoStub struct {
	service.UsageLogRepository
	created chan *service.UsageLog
}

func (s *openAIWSUsageHandlerUsageLogRepoStub) Create(ctx context.Context, log *service.UsageLog) (bool, error) {
	if s.created != nil {
		s.created <- log
	}
	return true, nil
}
func resetOpsErrorLoggerStateForTest(t *testing.T) {
	t.Helper()

	opsErrorLogMu.Lock()
	ch := opsErrorLogQueue
	opsErrorLogQueue = nil
	opsErrorLogStopping = true
	opsErrorLogMu.Unlock()

	if ch != nil {
		close(ch)
	}
	opsErrorLogWorkersWg.Wait()

	opsErrorLogOnce = sync.Once{}
	opsErrorLogStopOnce = sync.Once{}
	opsErrorLogWorkersWg = sync.WaitGroup{}
	opsErrorLogMu = sync.RWMutex{}
	opsErrorLogStopping = false

	opsErrorLogQueueLen.Store(0)
	opsErrorLogEnqueued.Store(0)
	opsErrorLogDropped.Store(0)
	opsErrorLogProcessed.Store(0)
	opsErrorLogSanitized.Store(0)
	opsErrorLogLastDropLogAt.Store(0)

	opsErrorLogShutdownCh = make(chan struct{})
	opsErrorLogShutdownOnce = sync.Once{}
	opsErrorLogDrained.Store(false)
}

func setupOpsErrorLogTestQueue(t *testing.T, size int) {
	t.Helper()
	resetOpsErrorLoggerStateForTest(t)
	opsErrorLogOnce.Do(func() {})
	opsErrorLogMu.Lock()
	opsErrorLogQueue = make(chan opsErrorLogJob, size)
	opsErrorLogMu.Unlock()
}
