//go:build unit

package admin

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type distinctWindowReader struct {
	calls  int
	starts map[int64]time.Time
	err    error
}

func (s *distinctWindowReader) GetAccountWindowStatsBatchByStart(_ context.Context, starts map[int64]time.Time) (map[int64]*usagestats.AccountStats, error) {
	s.calls++
	s.starts = starts
	return map[int64]*usagestats.AccountStats{1: {Cost: 20, StandardCost: 7, UserCost: 2}, 2: nil, 999: {StandardCost: 100}}, s.err
}

func TestAdminWindowStartsRetainDistinctEligibleWindows(t *testing.T) {
	first := time.Now().Add(-2 * time.Hour).In(time.FixedZone("fixture-offset", 3*3600))
	second, end := first.Add(time.Hour), time.Now().Add(time.Hour)
	makeAccount := func(id int64, start time.Time) service.Account {
		return service.Account{ID: id, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth, Extra: map[string]any{"window_cost_limit": 10.0}, SessionWindowStart: &start, SessionWindowEnd: &end}
	}
	accounts := []service.Account{makeAccount(1, first), makeAccount(2, second), makeAccount(1, second), makeAccount(3, first), makeAccount(4, first)}
	accounts[3].Platform = service.PlatformOpenAI
	accounts[4].Extra = nil
	got := accountWindowStarts(accounts)
	want := map[int64]time.Time{1: first, 2: second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("window starts collapsed/duplicated: got=%v want=%v", got, want)
	}
}

func TestAdminWindowCostsOneCallStandardCostAndBestEffort(t *testing.T) {
	r := &distinctWindowReader{}
	starts := map[int64]time.Time{1: time.Unix(100, 0), 2: time.Unix(200, 0)}
	got := loadAccountWindowCosts(context.Background(), r, starts)
	if r.calls != 1 || !reflect.DeepEqual(r.starts, starts) || !reflect.DeepEqual(got, map[int64]float64{1: 7}) {
		t.Fatalf("cost projection changed: %v %+v", got, r)
	}
	r.err = errors.New("synthetic failure")
	if got := loadAccountWindowCosts(context.Background(), r, starts); len(got) != 0 || r.calls != 2 {
		t.Fatal("error must remain best effort without retry")
	}
	if got := loadAccountWindowCosts(context.Background(), r, nil); len(got) != 0 || r.calls != 2 {
		t.Fatal("empty input must not query")
	}
}
