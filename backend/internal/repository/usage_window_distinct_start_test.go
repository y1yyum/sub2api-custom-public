//go:build unit

package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestAccountWindowDistinctStartQueryArgumentsAndZeros(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := time.Date(2026, 10, 25, 1, 30, 0, 0, time.FixedZone("offset", 3600))
	second := first.Add(90 * time.Minute)
	pattern := `(?s)` + regexp.QuoteMeta("JOIN (VALUES ($1::bigint, $2::timestamptz),($3::bigint, $4::timestamptz)) AS w(account_id, start_at) ON u.account_id = w.account_id") + `\s+WHERE u.created_at >= w.start_at\s+GROUP BY u.account_id`
	mock.ExpectQuery(pattern).WithArgs(int64(4), first, int64(9), second).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "requests", "tokens", "cost", "standard_cost", "user_cost"}).AddRow(4, 2, 33, 4.5, 3.0, 2.0)).RowsWillBeClosed()
	repo := &usageLogRepository{sql: db}
	stats, err := repo.GetAccountWindowStatsByStart(context.Background(), map[int64]time.Time{9: second, 4: first})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 || stats[4].Requests != 2 || stats[4].Tokens != 33 || stats[4].Cost != 4.5 || stats[4].StandardCost != 3 || stats[4].UserCost != 2 || *stats[9] != (usagestats.AccountStats{}) {
		t.Fatalf("unexpected aggregate map: %+v", stats)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountWindowDistinctStartEmptyBoundAndErrors(t *testing.T) {
	repo := &usageLogRepository{}
	if result, err := repo.GetAccountWindowStatsByStart(context.Background(), nil); err != nil || len(result) != 0 {
		t.Fatalf("empty: %v %v", result, err)
	}
	tooMany := make(map[int64]time.Time)
	for i := 0; i <= service.MaxAccountWindowStatsBatchSize; i++ {
		tooMany[int64(i)] = time.Time{}
	}
	if _, err := repo.GetAccountWindowStatsByStart(context.Background(), tooMany); err == nil {
		t.Fatal("unbounded batch accepted")
	}
	for _, kind := range []string{"query", "scan", "rows"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			want := errors.New("synthetic database failure")
			expect := mock.ExpectQuery("(?s)SELECT.*JOIN \\(VALUES")
			switch kind {
			case "query":
				expect.WillReturnError(want)
			case "scan":
				expect.WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow("invalid"))
			case "rows":
				expect.WillReturnRows(sqlmock.NewRows([]string{"account_id", "requests", "tokens", "cost", "standard_cost", "user_cost"}).AddRow(1, 1, 1, 1, 1, 1).RowError(0, want))
			}
			repo := &usageLogRepository{sql: db}
			if result, err := repo.GetAccountWindowStatsByStart(context.Background(), map[int64]time.Time{1: time.Time{}}); err == nil || result != nil {
				t.Fatalf("%s error concealed: %v %v", kind, result, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
