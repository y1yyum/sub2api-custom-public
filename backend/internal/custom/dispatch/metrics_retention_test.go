package dispatch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDispatchMonitoringConfigBoundsAndDefaults(t *testing.T) {
	base := Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, SnapshotFile: "snapshot.json"}
	cfg, err := normalizeConfig(base)
	require.NoError(t, err)
	require.Nil(t, cfg.Monitoring, "monitoring stays disabled unless configured")
	socket := filepath.Join(t.TempDir(), "metrics.sock")
	valid := []MonitoringConfig{
		{MetricsListen: "127.0.0.1:9464"},
		{MetricsListen: "[::1]:9464"},
		{MetricsListen: "unix:" + socket},
		{Aggregation: &AggregationConfig{MinuteRetentionDays: 1, HourRetentionDays: 1}},
		{Aggregation: &AggregationConfig{MinuteRetentionDays: 31, HourRetentionDays: 400}},
		{Retention: &RetentionConfig{ObservationDays: 1}},
		{Retention: &RetentionConfig{ProbeDays: 366}},
	}
	for _, monitoring := range valid {
		c := base
		c.Monitoring = &monitoring
		_, err := normalizeConfig(c)
		require.NoError(t, err, "%+v", monitoring)
	}
	invalid := map[string]MonitoringConfig{
		"empty":              {},
		"wildcard":           {MetricsListen: "0.0.0.0:9464"},
		"public":             {MetricsListen: "192.0.2.1:9464"},
		"hostname":           {MetricsListen: "localhost:9464"},
		"zone":               {MetricsListen: "[::1%lo]:9464"},
		"ephemeral":          {MetricsListen: "127.0.0.1:0"},
		"no-port":            {MetricsListen: "127.0.0.1"},
		"relative-socket":    {MetricsListen: "unix:metrics.sock"},
		"unclean-socket":     {MetricsListen: "unix:" + filepath.Dir(socket) + "/../metrics.sock"},
		"minute-zero":        {Aggregation: &AggregationConfig{HourRetentionDays: 1}},
		"minute-unbounded":   {Aggregation: &AggregationConfig{MinuteRetentionDays: 32, HourRetentionDays: 40}},
		"hour-below-minute":  {Aggregation: &AggregationConfig{MinuteRetentionDays: 5, HourRetentionDays: 4}},
		"hour-unbounded":     {Aggregation: &AggregationConfig{MinuteRetentionDays: 1, HourRetentionDays: 401}},
		"retention-implicit": {Retention: &RetentionConfig{}},
		"retention-negative": {Retention: &RetentionConfig{ObservationDays: -1, ProbeDays: 1}},
		"retention-large":    {Retention: &RetentionConfig{ObservationDays: 367}},
	}
	for name, monitoring := range invalid {
		t.Run(name, func(t *testing.T) {
			c := base
			c.Monitoring = &monitoring
			_, err := normalizeConfig(c)
			require.Error(t, err)
		})
	}
}

func TestDispatchMonitoringConfigDoesNotChangeSharedDigest(t *testing.T) {
	cfg := Config{Targets: []Target{testTarget("a", 1)}}
	before, err := runtimeConfigDigest(cfg, ProbeLimits{ConfigRevision: 1})
	require.NoError(t, err)
	cfg.Monitoring = &MonitoringConfig{MetricsListen: "127.0.0.1:9464",
		Aggregation: &AggregationConfig{MinuteRetentionDays: 2, HourRetentionDays: 30}, Retention: &RetentionConfig{ObservationDays: 3}}
	after, err := runtimeConfigDigest(cfg, ProbeLimits{ConfigRevision: 1})
	require.NoError(t, err)
	require.Equal(t, before, after, "process-local monitoring must not require a config revision bump")

	path := filepath.Join(t.TempDir(), "config.json")
	for _, body := range []string{
		`{"version":1,"core_instance":"core","config_revision":1,"snapshot_file":"s","targets":[],"monitoring":{"retention":{"observation_days":3,"unknown":1}}}`,
		`{"version":1,"core_instance":"core","config_revision":1,"snapshot_file":"s","targets":[],"monitoring":{"retention":{"observation_days":3,"Observation_Days":3}}}`,
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		_, err := LoadConfig(path)
		require.Error(t, err)
	}
}

func TestDispatchMetricLabelsStayWithinFixedAllowlist(t *testing.T) {
	total := 0
	for series, labels := range metricLabelAllowset {
		require.Regexp(t, `^[a-z_]+$`, series)
		for label := range labels {
			require.Regexp(t, `^[a-z_.]{0,48}$`, label, "migration CHECK must accept every allowlisted label")
		}
		total += len(labels)
	}
	require.LessOrEqual(t, total, 64, "the fixed label set bounds rows per minute bucket")
	outcomes := []Outcome{Success, UpstreamAuth, UpstreamRateLimit, UpstreamFailure, Unknown, LocalRejection, ClientCanceled}
	phases := []string{"", "connect", "headers", "content", "terminal"}
	facts := []string{"", "semantic_failure", "incomplete", "non_text_terminal", "stream_break", "local_timeout", "bridge_deadline"}
	classes := []string{"", FailureQuotaExhausted, FailureCredentialBanned, FailureCapabilityDenied}
	checked := 0
	for _, outcome := range outcomes {
		for _, evidence := range []bool{false, true} {
			for _, phase := range phases {
				for _, fact := range facts {
					for _, class := range classes {
						for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}} {
							o := Observation{Outcome: outcome, ProviderEvidence: evidence, Phase: phase, ProtocolFact: fact,
								FailureClass: class, TimedOut: flags[0], UsageMissing: flags[1]}
							if !validProtocolFacts(o) || !validFailureClass(o) || o.TimedOut && (!o.ProviderEvidence || o.Outcome != UpstreamFailure) {
								continue
							}
							counts := make(metricCounts)
							for _, probe := range []bool{false, true} {
								for _, key := range observationMetrics(o, probe) {
									require.NoError(t, counts.add(time.Unix(0, 0), key[0], key[1], 1), "%+v", o)
								}
							}
							checked++
						}
					}
				}
			}
		}
	}
	require.Positive(t, checked)
	for _, rejected := range [][2]string{{metricObservations, "acct-1"}, {metricFacts, "model gpt"}, {"unknown_series", ""}, {metricProbeReservations, "x"}} {
		require.Error(t, make(metricCounts).add(time.Unix(0, 0), rejected[0], rejected[1], 1))
	}
}

func TestDispatchMetricsRenderIsLowCardinalityPrometheusText(t *testing.T) {
	metrics := NewProcessMetrics()
	metrics.RecordRound(RoundResult{Revision: 7, Observations: 3, Probes: 2, Unknown: 1}, nil, 300*time.Millisecond, time.Unix(1700000000, 0))
	metrics.RecordRound(RoundResult{}, os.ErrDeadlineExceeded, 3*time.Second, time.Unix(1700000001, 0))
	metrics.RecordMaintenance("rollup", nil)
	metrics.RecordMaintenance("retention", os.ErrPermission)
	metrics.RecordRetention([]RetentionTableResult{{Table: "observations", Deleted: 1000, Pending: true}})
	now := time.Unix(1700000100, 0).UTC()
	watermark := now.Add(-2 * time.Minute)
	limit := int64(5000)
	metrics.SetState(MetricsState{ObservedAt: now, Targets: map[targetGaugeKey]int{{Healthy, true}: 3, {Tripped, true}: 1},
		ActiveHolds: map[string]int{FailureQuotaExhausted: 2}, ActiveLeases: 1, BudgetReady: true,
		Budget: ProbeBudgetTotals{ReservedMicrousd: 10}, BudgetDailyLimit: &limit, MetricsReady: true, RollupWatermark: &watermark,
		LastHour: map[seriesLabel]int64{{metricObservations, "success"}: 9, {metricObservations, "acct-secret"}: 1}, OldestObservation: &watermark})
	body := string(metrics.Render())
	line := regexp.MustCompile(`^[a-z_]+(\{[a-z_]+="[a-z_.+0-9A-Za-z]*"(,[a-z_]+="[a-z_.+0-9A-Za-z]*")*\})? -?[0-9.e+]+$`)
	allowedLabels := map[string]bool{"result": true, "le": true, "outcome": true, "task": true, "table": true,
		"state": true, "enabled": true, "class": true, "series": true, "label": true}
	samples := 0
	for _, text := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if strings.HasPrefix(text, "# HELP ") || strings.HasPrefix(text, "# TYPE ") {
			continue
		}
		require.Regexp(t, line, text)
		for _, match := range regexp.MustCompile(`([a-z_]+)="`).FindAllStringSubmatch(text, -1) {
			require.True(t, allowedLabels[match[1]], "unexpected label %s", match[1])
		}
		samples++
	}
	require.LessOrEqual(t, samples, 200)
	for _, expected := range []string{
		`dispatch_rounds_total{result="success"} 1`, `dispatch_rounds_total{result="failure"} 1`,
		`dispatch_round_duration_seconds_bucket{le="0.5"} 1`, `dispatch_round_duration_seconds_bucket{le="+Inf"} 2`,
		`dispatch_last_round_success_timestamp_seconds 1.7e+09`, `dispatch_snapshot_revision 7`,
		`dispatch_round_probes_total{outcome="unknown"} 1`, `dispatch_maintenance_total{task="retention",result="failure"} 1`,
		`dispatch_retention_deleted_rows_total{table="observations"} 1000`, `dispatch_retention_pending{table="observations"} 1`,
		`dispatch_targets{state="tripped",enabled="true"} 1`, `dispatch_health_holds_active{class="quota_exhausted"} 2`,
		`dispatch_probe_leases{state="active"} 1`, `dispatch_probe_budget_microusd{state="reserved"} 10`,
		`dispatch_probe_budget_daily_limit_microusd 5000`, `dispatch_metric_rollup_lag_seconds 120`,
		`dispatch_events_last_hour{series="observations",label="success"} 9`,
		`dispatch_history_oldest_age_seconds{table="observations"} 120`,
	} {
		require.Contains(t, body, expected+"\n")
	}
	require.NotContains(t, body, "acct-secret", "labels outside the allowlist are never exported")
	require.NotContains(t, body, "core_instance")
}

func TestDispatchRetentionRulesClassifyEverySchemaTable(t *testing.T) {
	entries, err := os.ReadDir("migrations")
	require.NoError(t, err)
	created := map[string]bool{"schema_migrations": true}
	pattern := regexp.MustCompile(`CREATE TABLE dispatch\.([a-z_]+)`)
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join("migrations", entry.Name()))
		require.NoError(t, err)
		for _, match := range pattern.FindAllStringSubmatch(string(body), -1) {
			created[match[1]] = true
		}
	}
	rules := map[string]retentionRule{}
	for _, rule := range retentionRules {
		require.NotEmpty(t, rule.Keep, rule.Table)
		rules[rule.Table] = rule
	}
	for table := range created {
		require.Contains(t, rules, table, "new dispatch tables must declare a retention rule")
	}
	require.Len(t, rules, len(created))
	for table := range retentionStatements {
		require.Equal(t, "retention", rules[table].Cleaner, table)
	}
	for _, rule := range retentionRules {
		if rule.Cleaner == "retention" {
			require.Contains(t, retentionStatements, rule.Table)
		}
	}
	// 必须保留的反例：这些表一旦出现在清理语句里就会破坏租约、金额、隔离或审计事实。
	for _, protected := range []string{"cores", "targets", "health_holds", "health_hold_audit", "probe_budget_latch",
		"probe_budget_policies", "probe_prices", "probe_money", "probe_money_audit", "metric_rollups", "schema_migrations",
		"quality_results", "quality_configs", "quality_baselines"} {
		require.NotContains(t, retentionStatements, protected)
		for _, statement := range retentionStatements {
			require.NotRegexp(t, `DELETE FROM dispatch\.`+protected+`\b`, statement)
		}
	}
	guards := map[string][]string{
		"observations": {"x.recorded_at<$2", "dispatch.health_holds h", "h.trigger_event_id=x.event_id", "dispatch.health_hold_audit a", "a.event_id=x.event_id", "LIMIT $3"},
		"probes": {"x.reserved_at<$2", "x.finished_at IS NOT NULL", "NOT EXISTS (SELECT 1 FROM dispatch.probe_money m WHERE m.token=x.token)",
			"n.reserved_at>x.reserved_at", "n.account_id=x.account_id", "n.model=x.model", "n.region=x.region", "LIMIT $3",
			"x.finished_at<w.at", "o.event_id=x.attempt_id AND o.recorded_at>=w.at"},
		"metric_minutes": {"bucket<$2", "core_instance=$1", "LIMIT $3"},
		"metric_hours":   {"bucket<$2", "core_instance=$1", "LIMIT $3"},
	}
	for table, required := range guards {
		for _, fragment := range required {
			require.Contains(t, retentionStatements[table], fragment, table)
		}
	}
}
