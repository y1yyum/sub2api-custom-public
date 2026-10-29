//go:build unit

package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

type distinctWindowLegacyStub struct {
	UsageLogRepository
	calls  map[int64]time.Time
	failID int64
}

func (s *distinctWindowLegacyStub) GetAccountWindowStats(_ context.Context, id int64, start time.Time) (*usagestats.AccountStats, error) {
	if s.calls == nil {
		s.calls = make(map[int64]time.Time)
	}
	s.calls[id] = start
	if id == s.failID {
		return nil, errors.New("synthetic failure")
	}
	return &usagestats.AccountStats{StandardCost: float64(id)}, nil
}

type distinctWindowBatchStub struct {
	distinctWindowLegacyStub
	batchCalls int
	starts     map[int64]time.Time
	err        error
}

func (s *distinctWindowBatchStub) GetAccountWindowStatsByStart(_ context.Context, starts map[int64]time.Time) (map[int64]*usagestats.AccountStats, error) {
	s.batchCalls++
	s.starts = starts
	if s.err != nil {
		return nil, s.err
	}
	return map[int64]*usagestats.AccountStats{1: {StandardCost: 7}}, nil
}

func TestAccountUsageDistinctWindowBatchAndFallback(t *testing.T) {
	starts := map[int64]time.Time{1: time.Unix(100, 0), 2: time.Unix(200, 0)}
	batch := &distinctWindowBatchStub{}
	svc := &AccountUsageService{usageLogRepo: batch}
	stats, err := svc.GetAccountWindowStatsBatchByStart(context.Background(), starts)
	if err != nil || stats[1].StandardCost != 7 || batch.batchCalls != 1 || len(batch.calls) != 0 || !reflect.DeepEqual(batch.starts, starts) {
		t.Fatalf("batch path: stats=%v err=%v stub=%+v", stats, err, batch)
	}
	batch.err = errors.New("batch unavailable")
	if _, err := svc.GetAccountWindowStatsBatchByStart(context.Background(), starts); !errors.Is(err, batch.err) || len(batch.calls) != 0 {
		t.Fatal("batch error must propagate without N-query retry")
	}
	legacy := &distinctWindowLegacyStub{failID: 2}
	svc.usageLogRepo = legacy
	stats, err = svc.GetAccountWindowStatsBatchByStart(context.Background(), starts)
	if err != nil || len(stats) != 1 || stats[1].StandardCost != 1 || !reflect.DeepEqual(legacy.calls, starts) {
		t.Fatalf("legacy best-effort behavior changed: %v %v %+v", stats, err, legacy)
	}
}

func TestAccountUsageDistinctWindowEmptyBoundAndCancellation(t *testing.T) {
	svc := &AccountUsageService{}
	if result, err := svc.GetAccountWindowStatsBatchByStart(context.Background(), nil); err != nil || len(result) != 0 {
		t.Fatal("empty input must not access repository")
	}
	starts := make(map[int64]time.Time)
	for i := 0; i <= MaxAccountWindowStatsBatchSize; i++ {
		starts[int64(i)] = time.Time{}
	}
	if _, err := svc.GetAccountWindowStatsBatchByStart(context.Background(), starts); err == nil {
		t.Fatal("unbounded batch accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.GetAccountWindowStatsBatchByStart(ctx, map[int64]time.Time{1: time.Time{}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request reached repository: %v", err)
	}
}
