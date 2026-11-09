package dispatch

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var roundDurationBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120}

var (
	maintenanceTasks  = []string{"rollup", "retention", "state"}
	retentionTables   = []string{"observations", "probes", "metric_minutes", "metric_hours"}
	targetGaugeStates = []State{Healthy, Degraded, Tripped, Probing}
)

// ProcessMetrics 汇总本进程的轮次与维护结果，并缓存最近一次数据库现状；
// 抓取只读取缓存，不触发数据库查询。
type ProcessMetrics struct {
	mu            sync.Mutex
	rounds        [2]uint64
	roundBuckets  []uint64
	roundSum      float64
	lastSuccess   time.Time
	lastRevision  int64
	observations  uint64
	probes        uint64
	probesUnknown uint64
	maintenance   map[[2]string]uint64
	deleted       map[string]uint64
	pending       map[string]bool
	state         *MetricsState
}

func NewProcessMetrics() *ProcessMetrics {
	return &ProcessMetrics{roundBuckets: make([]uint64, len(roundDurationBuckets)),
		maintenance: map[[2]string]uint64{}, deleted: map[string]uint64{}, pending: map[string]bool{}}
}

func (m *ProcessMetrics) RecordRound(result RoundResult, err error, duration time.Duration, finishedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seconds := duration.Seconds()
	for i, bound := range roundDurationBuckets {
		if seconds <= bound {
			m.roundBuckets[i]++
		}
	}
	m.roundSum += seconds
	if err != nil {
		m.rounds[1]++
		return
	}
	m.rounds[0]++
	m.lastSuccess, m.lastRevision = finishedAt, result.Revision
	m.observations += uint64(result.Observations)
	m.probes += uint64(result.Probes)
	m.probesUnknown += uint64(result.Unknown)
}

func (m *ProcessMetrics) RecordMaintenance(task string, err error) {
	result := "success"
	if err != nil {
		result = "failure"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maintenance[[2]string{task, result}]++
}

func (m *ProcessMetrics) RecordRetention(results []RetentionTableResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, result := range results {
		m.deleted[result.Table] += uint64(result.Deleted)
		m.pending[result.Table] = result.Pending
	}
}

func (m *ProcessMetrics) SetState(state MetricsState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = &state
}

type promWriter struct{ bytes.Buffer }

func (w *promWriter) family(name, kind, help string) {
	fmt.Fprintf(&w.Buffer, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

// sample 的标签值全部来自固定枚举，因此无需转义；名称和值顺序固定，输出可逐字比较。
func (w *promWriter) sample(name string, value float64, labels ...string) {
	pairs := make([]string, 0, len(labels)/2)
	for i := 0; i < len(labels); i += 2 {
		pairs = append(pairs, fmt.Sprintf("%s=%q", labels[i], labels[i+1]))
	}
	selector := ""
	if len(pairs) > 0 {
		selector = "{" + strings.Join(pairs, ",") + "}"
	}
	fmt.Fprintf(&w.Buffer, "%s%s %s\n", name, selector, strconv.FormatFloat(value, 'g', -1, 64))
}

func seconds(at time.Time) float64 { return float64(at.UnixMilli()) / 1000 }

// Render 输出 Prometheus 文本格式（0.0.4）。
func (m *ProcessMetrics) Render() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := new(promWriter)
	w.family("dispatch_rounds_total", "counter", "Completed dispatch work rounds by result.")
	w.sample("dispatch_rounds_total", float64(m.rounds[0]), "result", "success")
	w.sample("dispatch_rounds_total", float64(m.rounds[1]), "result", "failure")
	w.family("dispatch_round_duration_seconds", "histogram", "Dispatch work round duration.")
	for i, bound := range roundDurationBuckets {
		w.sample("dispatch_round_duration_seconds_bucket", float64(m.roundBuckets[i]), "le", strconv.FormatFloat(bound, 'g', -1, 64))
	}
	total := float64(m.rounds[0] + m.rounds[1])
	w.sample("dispatch_round_duration_seconds_bucket", total, "le", "+Inf")
	w.sample("dispatch_round_duration_seconds_sum", m.roundSum)
	w.sample("dispatch_round_duration_seconds_count", total)
	w.family("dispatch_last_round_success_timestamp_seconds", "gauge", "Unix time of the last successful round that published a snapshot; 0 before the first.")
	if m.lastSuccess.IsZero() {
		w.sample("dispatch_last_round_success_timestamp_seconds", 0)
	} else {
		w.sample("dispatch_last_round_success_timestamp_seconds", seconds(m.lastSuccess))
	}
	w.family("dispatch_snapshot_revision", "gauge", "Revision of the last snapshot published by this process.")
	w.sample("dispatch_snapshot_revision", float64(m.lastRevision))
	w.family("dispatch_round_observations_total", "counter", "Newly recorded passive observations reported by successful rounds.")
	w.sample("dispatch_round_observations_total", float64(m.observations))
	w.family("dispatch_round_probes_total", "counter", "Probe executions reported by successful rounds.")
	w.sample("dispatch_round_probes_total", float64(m.probes-m.probesUnknown), "outcome", "known")
	w.sample("dispatch_round_probes_total", float64(m.probesUnknown), "outcome", "unknown")
	w.family("dispatch_maintenance_total", "counter", "Monitoring maintenance tasks by result.")
	for _, task := range maintenanceTasks {
		for _, result := range []string{"success", "failure"} {
			w.sample("dispatch_maintenance_total", float64(m.maintenance[[2]string{task, result}]), "task", task, "result", result)
		}
	}
	w.family("dispatch_retention_deleted_rows_total", "counter", "Rows removed by explicit history retention.")
	for _, table := range retentionTables {
		w.sample("dispatch_retention_deleted_rows_total", float64(m.deleted[table]), "table", table)
	}
	w.family("dispatch_retention_pending", "gauge", "1 when the last retention pass stopped at its batch limit.")
	for _, table := range retentionTables {
		w.sample("dispatch_retention_pending", boolGauge(m.pending[table]), "table", table)
	}
	if m.state != nil {
		m.state.render(w)
	}
	return w.Bytes()
}

func boolGauge(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func (s *MetricsState) render(w *promWriter) {
	w.family("dispatch_state_timestamp_seconds", "gauge", "Database time of the cached dispatch state below.")
	w.sample("dispatch_state_timestamp_seconds", seconds(s.ObservedAt))
	w.family("dispatch_targets", "gauge", "Retained targets by persisted health state and enabled flag.")
	for _, state := range targetGaugeStates {
		for _, enabled := range []bool{true, false} {
			w.sample("dispatch_targets", float64(s.Targets[targetGaugeKey{State: state, Enabled: enabled}]),
				"state", string(state), "enabled", strconv.FormatBool(enabled))
		}
	}
	w.family("dispatch_targets_auth_blocked", "gauge", "Retained targets blocked by a confirmed credential rejection.")
	w.sample("dispatch_targets_auth_blocked", float64(s.AuthBlocked))
	w.family("dispatch_health_holds_active", "gauge", "Active persistent health holds by class.")
	for _, class := range metricHoldClasses {
		w.sample("dispatch_health_holds_active", float64(s.ActiveHolds[class]), "class", class)
	}
	w.family("dispatch_probe_leases", "gauge", "Unfinished probe leases of this core.")
	w.sample("dispatch_probe_leases", float64(s.ActiveLeases), "state", "active")
	w.sample("dispatch_probe_leases", float64(s.ExpiredUnfinished), "state", "expired_unfinished")
	w.family("dispatch_history_oldest_age_seconds", "gauge", "Age of the oldest retained raw history row of this core.")
	for _, item := range []struct {
		table string
		at    *time.Time
	}{{"observations", s.OldestObservation}, {"probes", s.OldestProbe}} {
		if item.at != nil {
			w.sample("dispatch_history_oldest_age_seconds", s.ObservedAt.Sub(*item.at).Seconds(), "table", item.table)
		}
	}
	if s.BudgetReady {
		w.family("dispatch_probe_budget_microusd", "gauge", "Global probe cost ceilings in USD micro: open reservations, unknown results and today's charges.")
		w.sample("dispatch_probe_budget_microusd", float64(s.Budget.ReservedMicrousd), "state", "reserved")
		w.sample("dispatch_probe_budget_microusd", float64(s.Budget.UnknownMicrousd), "state", "unknown")
		w.sample("dispatch_probe_budget_microusd", float64(s.Budget.ChargedMicrousd), "state", "charged_today")
		w.sample("dispatch_probe_budget_microusd", float64(s.Budget.ExposureMicrousd), "state", "exposure")
		if s.BudgetDailyLimit != nil {
			w.family("dispatch_probe_budget_daily_limit_microusd", "gauge", "Effective global daily probe cost ceiling across active cores.")
			w.sample("dispatch_probe_budget_daily_limit_microusd", float64(*s.BudgetDailyLimit))
		}
	}
	if !s.MetricsReady {
		return
	}
	if s.RollupWatermark != nil {
		w.family("dispatch_metric_rollup_lag_seconds", "gauge", "Seconds between database time and the minute aggregation watermark.")
		w.sample("dispatch_metric_rollup_lag_seconds", s.ObservedAt.Sub(*s.RollupWatermark).Seconds())
	}
	w.family("dispatch_events_last_hour", "gauge", "Aggregated deduplicated events in the last 60 complete minutes by fixed series and label.")
	keys := make([]seriesLabel, 0, len(s.LastHour))
	for key := range s.LastHour {
		if metricLabelAllowset[key.Series][key.Label] {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Series != keys[j].Series {
			return keys[i].Series < keys[j].Series
		}
		return keys[i].Label < keys[j].Label
	})
	for _, key := range keys {
		w.sample("dispatch_events_last_hour", float64(s.LastHour[key]), "series", key.Series, "label", key.Label)
	}
}
