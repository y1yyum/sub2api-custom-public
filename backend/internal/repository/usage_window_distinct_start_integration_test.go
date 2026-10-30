//go:build integration

package repository

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// Compare the real PostgreSQL VALUES join against the unchanged single-account
// query using a rollback-only temporary table; no live/operator DSN is accepted.
func TestAccountWindowStatsDistinctStartsPostgres(t *testing.T) {
	if integrationDB == nil {
		t.Fatal("isolated repository fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := integrationDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE usage_logs (
		account_id bigint NOT NULL, created_at timestamptz NOT NULL,
		input_tokens bigint, output_tokens bigint, cache_creation_tokens bigint, cache_read_tokens bigint,
		account_stats_cost numeric, total_cost numeric, actual_cost numeric, account_rate_multiplier numeric
	) ON COMMIT DROP; SET LOCAL search_path TO pg_temp;`)
	if err != nil {
		t.Fatal(err)
	}
	startA := time.Date(2026, 10, 25, 1, 30, 0, 0, time.FixedZone("offset", 3600))
	startB := startA.Add(90 * time.Minute)
	for _, id := range []int64{1, 2} {
		for _, at := range []time.Time{startA.Add(-time.Microsecond), startA, startA.Add(time.Hour), startB, startB.Add(time.Hour)} {
			_, err := tx.ExecContext(ctx, `INSERT INTO usage_logs VALUES ($1,$2,1,2,3,4,NULL,2,1,NULL)`, id, at)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// Override + multiplier and NULL-token semantics must match the old query.
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_logs VALUES (1,$1,10,20,30,40,3,9,4,2),(2,$2,NULL,2,3,4,NULL,5,3,0.5)`, startA, startB)
	if err != nil {
		t.Fatal(err)
	}
	repo := &usageLogRepository{sql: tx}
	starts := map[int64]time.Time{1: startA, 2: startB, 3: startA}
	got, err := repo.GetAccountWindowStatsByStart(ctx, starts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(starts) {
		t.Fatalf("missing zero-stat account: %v", got)
	}
	for id, start := range starts {
		want, err := repo.GetAccountWindowStats(ctx, id, start)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got[id], want) {
			t.Fatalf("account %d: batch=%+v single=%+v", id, got[id], want)
		}
	}
	if got[1].Requests != 5 || got[2].Requests != 3 || got[3].Requests != 0 {
		t.Fatalf("distinct starts or inclusive boundary lost: %+v %+v %+v", got[1], got[2], got[3])
	}
	// Equivalent instants expressed in another zone must not change aggregates.
	starts[1] = startA.UTC()
	again, err := repo.GetAccountWindowStatsByStart(ctx, starts)
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Fatalf("timezone instant drift: %v %v", again, err)
	}
}
