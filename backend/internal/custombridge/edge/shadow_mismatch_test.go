package edge

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

func assertShadowAccounting(t *testing.T, stats ShadowStats) {
	t.Helper()
	reasons := stats.MismatchReasons
	if total := reasons.ExpectedAccountAbsent + reasons.RouteMismatch + reasons.Disabled + reasons.Health +
		reasons.ZeroWeight + reasons.Capacity + reasons.SWRRChoice; total != stats.Mismatches {
		t.Fatalf("mismatch reasons sum=%d, mismatches=%d", total, stats.Mismatches)
	}
	if stats.Observations != stats.Matches+stats.Mismatches+stats.Unavailable {
		t.Fatalf("observation accounting diverged: %+v", stats)
	}
	if stats.Unavailable != stats.SnapshotUnavailable+stats.SnapshotExpired+stats.NoEligibleRoute {
		t.Fatalf("unavailable accounting diverged: %+v", stats)
	}
}

func TestEdgeShadowMismatchReasonsAndUnavailableAccounting(t *testing.T) {
	now := time.Now().UTC()
	cfg := shadowConfig(t, "https://core.test")
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	handler.shadow.close()
	grant := cfg.Grants[0]
	handler.shadow.observe(grant, "fixture-model", now)
	if stats := handler.ShadowStats(); stats.SnapshotUnavailable != 1 || stats.MismatchReasons != (MismatchReasons{}) {
		t.Fatalf("snapshot unavailable was reclassified: %+v", stats)
	}
	var want MismatchReasons
	for index, test := range []struct {
		name   string
		change func(*dispatch.Snapshot)
		count  func(*MismatchReasons)
	}{
		{"absent", func(s *dispatch.Snapshot) { s.Candidates = s.Candidates[:1] }, func(r *MismatchReasons) { r.ExpectedAccountAbsent++ }},
		{"route", func(s *dispatch.Snapshot) { s.Candidates[1].Region = "other-region" }, func(r *MismatchReasons) { r.RouteMismatch++ }},
		{"disabled", func(s *dispatch.Snapshot) {
			s.Candidates[1].Enabled, s.Candidates[1].EffectiveWeight = false, 0
		}, func(r *MismatchReasons) { r.Disabled++ }},
		{"health", func(s *dispatch.Snapshot) {
			s.Candidates[1].State, s.Candidates[1].EffectiveWeight = dispatch.Tripped, 0
		}, func(r *MismatchReasons) { r.Health++ }},
		{"zero_weight", func(s *dispatch.Snapshot) { s.Candidates[1].EffectiveWeight = 0 }, func(r *MismatchReasons) { r.ZeroWeight++ }},
		{"capacity", func(s *dispatch.Snapshot) {
			s.Candidates[1].Active = s.Candidates[1].MaxInflight
		}, func(r *MismatchReasons) { r.Capacity++ }},
		{"swrr_choice", func(*dispatch.Snapshot) {}, func(r *MismatchReasons) { r.SWRRChoice++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := shadowSnapshot(now)
			snapshot.Revision += int64(index)
			expected := snapshot.Candidates[0]
			expected.Ref, expected.AccountID = "expected-candidate-private-ref", grant.AccountID
			expected.Reason = "private-error-reason"
			snapshot.Candidates = append(snapshot.Candidates, expected)
			test.change(&snapshot)
			writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, snapshot)
			handler.shadow.refresh(now)
			handler.shadow.observe(grant, "fixture-model", now)
			test.count(&want)
			stats := handler.ShadowStats()
			assertShadowAccounting(t, stats)
			if !stats.LastRefreshOK || stats.MismatchReasons != want || stats.Mismatches != uint64(index+1) {
				t.Fatalf("mismatch classification failed: %+v, want=%+v", stats, want)
			}
		})
	}
	snapshot := shadowSnapshot(now)
	snapshot.Revision += 7
	snapshot.Candidates[0].AccountID = grant.AccountID
	writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, snapshot)
	handler.shadow.refresh(now)
	handler.shadow.observe(grant, "fixture-model", now)
	handler.shadow.observe(grant, "unknown-request-model", now)
	handler.shadow.observe(grant, "fixture-model", snapshot.ExpiresAt)
	if err := os.WriteFile(cfg.ShadowDispatch.SnapshotFile, []byte("raw-private-snapshot-error"), 0600); err != nil {
		t.Fatal(err)
	}
	handler.shadow.refresh(now)
	stats := handler.ShadowStats()
	assertShadowAccounting(t, stats)
	if stats.MismatchReasons != want || stats.Mismatches != 7 || stats.Matches != 1 || stats.Unavailable != 3 ||
		stats.SnapshotUnavailable != 1 || stats.SnapshotExpired != 1 || stats.NoEligibleRoute != 1 || stats.LastRefreshOK {
		t.Fatalf("match, unavailable or refresh changed mismatch accounting: %+v", stats)
	}
	assertShadowStatsJSON(t, stats)
}

func TestEdgeShadowMismatchAliasesAndConcurrentRefresh(t *testing.T) {
	now := time.Now().UTC()
	cfg := shadowConfig(t, "https://core.test")
	snapshot := shadowSnapshot(now)
	expected := snapshot.Candidates[0]
	expected.Ref, expected.AccountID = "expected-alias-private", cfg.Grants[0].AccountID
	held := expected
	held.Ref, held.EffectiveWeight, held.Reason = "held-alias-private", 0, "manual_hold"
	disabled := expected
	disabled.Ref, disabled.Enabled, disabled.EffectiveWeight = "disabled-alias-private", false, 0
	snapshot.Candidates = append(snapshot.Candidates, expected, held, disabled)
	writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, snapshot)
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	handler.shadow.close()
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 200 {
				handler.shadow.observe(cfg.Grants[0], "fixture-model", now)
				assertShadowAccounting(t, handler.ShadowStats())
			}
		})
	}
	for revision := int64(11); revision <= 30; revision++ {
		snapshot.Revision = revision
		writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, snapshot)
		handler.shadow.refresh(now)
	}
	workers.Wait()
	stats := handler.ShadowStats()
	assertShadowAccounting(t, stats)
	if stats.Observations != 1600 || stats.Unavailable != 0 || stats.Mismatches == 0 ||
		stats.MismatchReasons != (MismatchReasons{SWRRChoice: stats.Mismatches}) {
		t.Fatalf("eligible alias was attributed to another held or disabled alias: %+v", stats)
	}
	assertShadowStatsJSON(t, stats)
}

func assertShadowStatsJSON(t *testing.T, stats ShadowStats) {
	t.Helper()
	assertShadowAccounting(t, stats)
	body, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	assertShadowStatsJSONBody(t, body)
}

func assertShadowStatsJSONBody(t *testing.T, body []byte) {
	t.Helper()
	var stats ShadowStats
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatal(err)
	}
	assertShadowAccounting(t, stats)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	assertShadowJSONKeys(t, fields, []string{
		"enabled", "snapshot_revision", "snapshot_expires_at", "last_refresh_ok", "refresh_successes", "refresh_failures",
		"observations", "matches", "mismatches", "mismatch_reasons", "unavailable", "snapshot_unavailable", "snapshot_expired", "no_eligible_route",
	})
	var reasons map[string]uint64
	if err := json.Unmarshal(fields["mismatch_reasons"], &reasons); err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]json.RawMessage, len(reasons))
	var total uint64
	for key, value := range reasons {
		keys[key] = nil
		total += value
	}
	assertShadowJSONKeys(t, keys, []string{"expected_account_absent", "route_mismatch", "disabled", "health", "zero_weight", "capacity", "swrr_choice"})
	if total != stats.Mismatches {
		t.Fatalf("JSON mismatch reasons sum=%d, mismatches=%d", total, stats.Mismatches)
	}
	assertShadowStatsPrivacy(t, body)
	var legacy struct {
		Observations uint64 `json:"observations"`
		Matches      uint64 `json:"matches"`
		Mismatches   uint64 `json:"mismatches"`
		Unavailable  uint64 `json:"unavailable"`
	}
	if err := json.Unmarshal(body, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Observations != stats.Observations || legacy.Matches != stats.Matches || legacy.Mismatches != stats.Mismatches || legacy.Unavailable != stats.Unavailable {
		t.Fatal("additive stats fields changed legacy counters")
	}
}

func assertShadowStatsPrivacy(t *testing.T, body []byte) {
	t.Helper()
	for _, value := range []string{
		testBearer, testCoreKey, testSecret, textBody, "actor-fixture", "payer-fixture", "grant-fixture", "candidate-a", "credential-a",
		"fixture-model", "pool-fixture", "policy-1", "region-a", "provider-fixture", "expected-candidate-private-ref", "private-error-reason",
		"raw-private-snapshot-error", "expected-alias-private", "held-alias-private", "disabled-alias-private", "manual_hold",
		"private-gateway-seed", "private-shard-seed", "selection_seed", "gateway_instance", "shard_ref", "candidate_ref", "account_id",
	} {
		if strings.Contains(string(body), value) {
			t.Fatalf("shadow stats leaked private metadata: %s", value)
		}
	}
}

func assertShadowJSONKeys(t *testing.T, fields map[string]json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, 0, len(fields))
	for key := range fields {
		got = append(got, key)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected JSON fields: got=%v, want=%v", got, want)
	}
}
