package dispatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

var ErrMetricsSchema = errors.New("dispatch metrics schema 006 is not applied or does not match")

// 每次最多推进一小时，限制单次扫描范围与持锁写入的分钟行数。
const metricRollupMaxSpan = time.Hour

const (
	metricObservations      = "observations"
	metricProbeObservations = "probe_observations"
	metricProviderEvidence  = "provider_evidence"
	metricFacts             = "facts"
	metricPhases            = "phases"
	metricFailureClasses    = "failure_classes"
	metricProbeReservations = "probe_reservations"
	metricHoldTriggers      = "hold_triggers"
	metricHoldReleases      = "hold_releases"
)

var (
	metricOutcomes      = []string{string(Success), string(UpstreamAuth), string(UpstreamRateLimit), string(UpstreamFailure), string(Unknown), string(LocalRejection), string(ClientCanceled)}
	metricHoldClasses   = []string{FailureQuotaExhausted, FailureCredentialBanned, FailureCapabilityDenied}
	metricLabelAllowset = buildMetricLabels()
)

// buildMetricLabels 枚举每个序列的全部可能标签；任何不在此集合中的值都视为契约破坏。
func buildMetricLabels() map[string]map[string]bool {
	set := func(values ...string) map[string]bool {
		out := make(map[string]bool, len(values))
		for _, value := range values {
			out[value] = true
		}
		return out
	}
	facts := set("usage_missing", "timed_out")
	for fact, phases := range protocolFactPhases {
		for phase := range phases {
			facts[protocolFactKeys(Observation{ProtocolFact: fact, Phase: phase})[0]] = true
		}
	}
	return map[string]map[string]bool{
		metricObservations:      set(metricOutcomes...),
		metricProbeObservations: set(metricOutcomes...),
		metricProviderEvidence:  set(string(Success), string(UpstreamAuth), string(UpstreamRateLimit), string(UpstreamFailure)),
		metricFacts:             facts,
		metricPhases:            set("connect", "headers", "content", "terminal"),
		metricFailureClasses:    set(metricHoldClasses...),
		metricProbeReservations: set(""),
		metricHoldTriggers:      set(metricHoldClasses...),
		metricHoldReleases:      set(metricHoldClasses...),
	}
}

type metricKey struct {
	Bucket int64
	Series string
	Label  string
}

type metricCounts map[metricKey]int64

func (c metricCounts) add(bucket time.Time, series, label string, count int64) error {
	if !metricLabelAllowset[series][label] {
		return fmt.Errorf("dispatch metric label outside allowlist: %s", series)
	}
	c[metricKey{Bucket: bucket.Unix(), Series: series, Label: label}] += count
	return nil
}

// observationMetrics 把一条已去重的观测映射为固定序列；探针结果另计 probe_observations。
func observationMetrics(o Observation, probe bool) [][2]string {
	keys := [][2]string{{metricObservations, string(o.Outcome)}}
	if probe {
		keys = append(keys, [2]string{metricProbeObservations, string(o.Outcome)})
	}
	// 与 Health 归因一致：本地拒绝、客户端取消与 Unknown 即使带证据也不计入上游归因。
	if o.ProviderEvidence && o.Outcome != LocalRejection && o.Outcome != ClientCanceled && o.Outcome != Unknown {
		keys = append(keys, [2]string{metricProviderEvidence, string(o.Outcome)})
	}
	for _, fact := range protocolFactKeys(o) {
		keys = append(keys, [2]string{metricFacts, fact})
	}
	if o.TimedOut {
		keys = append(keys, [2]string{metricFacts, "timed_out"})
	}
	if o.Phase != "" {
		keys = append(keys, [2]string{metricPhases, o.Phase})
	}
	if o.FailureClass != "" {
		keys = append(keys, [2]string{metricFailureClasses, o.FailureClass})
	}
	return keys
}

func metricsSchemaReady(ctx context.Context, tx *sql.Tx) (bool, error) {
	var present int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM unnest(ARRAY['dispatch.metric_rollups',
	 'dispatch.metric_minutes','dispatch.metric_hours']) AS t(name) WHERE to_regclass(name) IS NOT NULL`).Scan(&present); err != nil {
		return false, err
	}
	if present == 0 {
		return false, nil
	}
	var checksum string
	if present != 3 || tx.QueryRowContext(ctx, `SELECT checksum FROM dispatch.schema_migrations WHERE version='006'`).Scan(&checksum) != nil ||
		checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(metricsSchema))) {
		return false, ErrMetricsSchema
	}
	return true, nil
}

type MetricsRollup struct {
	Watermark time.Time `json:"watermark"`
	Minutes   int       `json:"minutes"`
	Rows      int       `json:"rows"`
}

// RollupMetrics 把 [watermark, 当前整分钟) 的去重事实计入分钟与小时表，分三段执行，全局锁只覆盖首尾两段短事务：
//  1. 持锁读数据库时刻与水位，确定 [watermark, stop)。所有写入观测、探针与隔离审计的事务都先取同一把锁再读时钟，
//     因此取得锁时 recorded_at／reserved_at 早于 stop 的行都已提交，此后也不会再出现。
//  2. 释放锁后在只读 REPEATABLE READ 快照中扫描该区间；区间内的行不再变化，清理只删除早于水位的原始行，
//     并保留结果观测未聚合的探针，扫描结果与持锁扫描相同。
//  3. 重新持锁，先以 CAS 把水位从 watermark 推进到 stop，再写入计数后提交；并发聚合或水位被改动时 CAS
//     失败，水位回拨时分钟行冲突，整个事务回滚，计数不重复。
//
// 第 1、3 段都受 maintenanceLockHold 约束（覆盖到提交完成），超时即回滚并释放锁，水位不推进，下一轮重试。
// 提交被取消时，服务端要么已持久提交（取消被忽略，COMMIT 返回成功），要么回滚并返回错误；仅连接在提交途中断开时
// 结果未知。此时若实际已提交，下一轮从新水位起算，第 3 段的水位 CAS 与分钟行主键保证同一区间不会重复计数。
func (s *Store) RollupMetrics(ctx context.Context, core string) (MetricsRollup, error) {
	if !validRef(core) {
		return MetricsRollup{}, errors.New("invalid dispatch core identity")
	}
	watermark, stop, done, err := s.metricRollupRange(ctx, core)
	if err != nil || done {
		return MetricsRollup{Watermark: watermark}, err
	}
	counts, err := s.scanMetricCounts(ctx, core, watermark, stop)
	if err != nil {
		return MetricsRollup{}, err
	}
	tx, now, ctx, cancel, err := s.beginMaintenance(ctx)
	if err != nil {
		return MetricsRollup{}, err
	}
	defer cancel()
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE dispatch.metric_rollups SET watermark=$3,updated_at=$4
	 WHERE core_instance=$1 AND watermark=$2`, core, watermark, stop, now)
	if err != nil {
		return MetricsRollup{}, err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return MetricsRollup{}, errors.New("dispatch metric rollup watermark conflict")
	}
	if err := writeMetricCounts(ctx, tx, core, counts); err != nil {
		return MetricsRollup{}, err
	}
	return MetricsRollup{Watermark: stop, Minutes: int(stop.Sub(watermark) / time.Minute), Rows: len(counts)}, tx.Commit()
}

// maintenanceLockHold 是维护事务取得调度全局锁后的持锁上界，明显小于探针 3 秒结算时限，
// 保证慢聚合或慢清理不让并发的 FinishProbe、配置同步与快照发布超时。
const maintenanceLockHold = time.Second

// beginMaintenance 用可取消的 ctx 开启事务，取得全局锁后才开始计时（等锁不计入）。到时即取消该 ctx：
// lib/pq 在整个事务期间监听 BeginTx 的 ctx，取消请求同样中止进行中的语句与 COMMIT（含提交时触发的延迟约束），
// 服务端回滚并释放锁，因此持锁上界覆盖到提交或回滚完成。statement_timeout 在服务端为单条语句兜底。
// 返回的 ctx 供持锁语句使用；调用方先回滚再调用 release。
func (s *Store) beginMaintenance(ctx context.Context) (*sql.Tx, time.Time, context.Context, func(), error) {
	held, cancel := context.WithCancel(ctx)
	tx, now, err := s.begin(held)
	if err != nil {
		cancel()
		return nil, time.Time{}, nil, nil, err
	}
	timer := time.AfterFunc(maintenanceLockHold, cancel)
	release := func() {
		timer.Stop()
		cancel()
	}
	if _, err := tx.ExecContext(held, fmt.Sprintf(`SET LOCAL statement_timeout=%d`, maintenanceLockHold.Milliseconds())); err != nil {
		_ = tx.Rollback()
		release()
		return nil, time.Time{}, nil, nil, err
	}
	return tx, now, held, release, nil
}

// metricRollupRange 持全局锁确定本次聚合区间；首次启用只建立水位，done 表示无需扫描。
func (s *Store) metricRollupRange(ctx context.Context, core string) (time.Time, time.Time, bool, error) {
	tx, now, ctx, cancel, err := s.beginMaintenance(ctx)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	defer cancel()
	defer func() { _ = tx.Rollback() }()
	ready, err := metricsSchemaReady(ctx, tx)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if !ready {
		return time.Time{}, time.Time{}, false, ErrMetricsSchema
	}
	end := now.Truncate(time.Minute)
	var watermark time.Time
	err = tx.QueryRowContext(ctx, `SELECT watermark FROM dispatch.metric_rollups WHERE core_instance=$1`, core).Scan(&watermark)
	if errors.Is(err, sql.ErrNoRows) {
		// 首次启用从当前整分钟开始，不回填启用前的历史。
		if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.metric_rollups(core_instance,watermark,started_at,updated_at)
		 VALUES ($1,$2,$3,$3)`, core, end, now); err != nil {
			return time.Time{}, time.Time{}, false, err
		}
		return end, end, true, tx.Commit()
	}
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	watermark = watermark.UTC()
	if !watermark.Before(end) {
		return watermark, watermark, true, tx.Commit()
	}
	stop := watermark.Add(metricRollupMaxSpan)
	if end.Before(stop) {
		stop = end
	}
	return watermark, stop, false, tx.Commit()
}

func (s *Store) scanMetricCounts(ctx context.Context, core string, from, to time.Time) (metricCounts, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	counts, err := collectMetricCounts(ctx, tx, core, from, to)
	if err != nil {
		return nil, err
	}
	return counts, tx.Commit()
}

func collectMetricCounts(ctx context.Context, tx *sql.Tx, core string, from, to time.Time) (metricCounts, error) {
	counts := make(metricCounts)
	rows, err := tx.QueryContext(ctx, `SELECT date_trunc('minute',o.recorded_at,'UTC'),o.payload->>'outcome',
	 COALESCE((o.payload->>'provider_evidence')::boolean,FALSE),COALESCE(o.payload->>'failure_class',''),
	 COALESCE((o.payload->>'timed_out')::boolean,FALSE),COALESCE(o.payload->>'phase',''),
	 COALESCE(o.payload->>'protocol_fact',''),COALESCE((o.payload->>'usage_missing')::boolean,FALSE),
	 EXISTS(SELECT 1 FROM dispatch.probes p WHERE p.attempt_id=o.event_id AND p.core_instance=o.core_instance),count(*)
	 FROM dispatch.observations o WHERE o.core_instance=$1 AND o.recorded_at>=$2 AND o.recorded_at<$3
	 GROUP BY 1,2,3,4,5,6,7,8,9`, core, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var bucket time.Time
		var o Observation
		var probe bool
		var count int64
		if err := rows.Scan(&bucket, &o.Outcome, &o.ProviderEvidence, &o.FailureClass, &o.TimedOut, &o.Phase,
			&o.ProtocolFact, &o.UsageMissing, &probe, &count); err != nil {
			_ = rows.Close()
			return nil, err
		}
		for _, key := range observationMetrics(o, probe) {
			if err := counts.add(bucket, key[0], key[1], count); err != nil {
				_ = rows.Close()
				return nil, err
			}
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT date_trunc('minute',reserved_at,'UTC'),'',count(*) FROM dispatch.probes
	 WHERE core_instance=$1 AND reserved_at>=$2 AND reserved_at<$3 GROUP BY 1
	 UNION ALL SELECT date_trunc('minute',a.recorded_at,'UTC'),a.action||':'||h.class,count(*)
	 FROM dispatch.health_hold_audit a JOIN dispatch.health_holds h USING(core_instance,hold_id)
	 WHERE a.core_instance=$1 AND a.recorded_at>=$2 AND a.recorded_at<$3 GROUP BY 1,2`, core, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var bucket time.Time
		var kind string
		var count int64
		if err := rows.Scan(&bucket, &kind, &count); err != nil {
			_ = rows.Close()
			return nil, err
		}
		series, label := metricProbeReservations, ""
		if kind != "" {
			action, class, _ := strings.Cut(kind, ":")
			series, label = metricHoldTriggers, class
			if action == "release" {
				series = metricHoldReleases
			}
		}
		if err := counts.add(bucket, series, label, count); err != nil {
			_ = rows.Close()
			return nil, err
		}
	}
	return counts, closeRows(rows)
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	return err
}

func writeMetricCounts(ctx context.Context, tx *sql.Tx, core string, counts metricCounts) error {
	if len(counts) == 0 {
		return nil
	}
	hours := make(metricCounts)
	for key, count := range counts {
		hour := time.Unix(key.Bucket, 0).UTC().Truncate(time.Hour).Unix()
		hours[metricKey{Bucket: hour, Series: key.Series, Label: key.Label}] += count
	}
	// 分钟行只写一次：冲突说明水位被回拨或重复聚合，直接失败而不是叠加。
	if err := upsertMetricCounts(ctx, tx, `INSERT INTO dispatch.metric_minutes(core_instance,bucket,series,label,count)
	 SELECT $1,to_timestamp(b),s,l,c FROM unnest($2::bigint[],$3::text[],$4::text[],$5::bigint[]) AS u(b,s,l,c)`, core, counts); err != nil {
		return err
	}
	return upsertMetricCounts(ctx, tx, `INSERT INTO dispatch.metric_hours(core_instance,bucket,series,label,count)
	 SELECT $1,to_timestamp(b),s,l,c FROM unnest($2::bigint[],$3::text[],$4::text[],$5::bigint[]) AS u(b,s,l,c)
	 ON CONFLICT (core_instance,bucket,series,label) DO UPDATE SET count=dispatch.metric_hours.count+EXCLUDED.count`, core, hours)
}

func upsertMetricCounts(ctx context.Context, tx *sql.Tx, query, core string, counts metricCounts) error {
	buckets := make([]int64, 0, len(counts))
	series := make([]string, 0, len(counts))
	labels := make([]string, 0, len(counts))
	values := make([]int64, 0, len(counts))
	for key, count := range counts {
		buckets, series, labels, values = append(buckets, key.Bucket), append(series, key.Series), append(labels, key.Label), append(values, count)
	}
	_, err := tx.ExecContext(ctx, query, core, pq.Array(buckets), pq.Array(series), pq.Array(labels), pq.Array(values))
	return err
}

type MetricCount struct {
	Bucket time.Time `json:"bucket"`
	Series string    `json:"series"`
	Label  string    `json:"label"`
	Count  int64     `json:"count"`
}

type MetricsRollupStatus struct {
	Watermark  time.Time `json:"watermark"`
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LagSeconds int64     `json:"lag_seconds"`
}

type HistoryTableStatus struct {
	Table  string     `json:"table"`
	Rows   int64      `json:"rows"`
	Oldest *time.Time `json:"oldest,omitempty"`
}

type MetricsReport struct {
	CoreInstance     string               `json:"core_instance"`
	ConfigRevision   int64                `json:"config_revision"`
	ObservedAt       time.Time            `json:"observed_at"`
	SchemaReady      bool                 `json:"schema_ready"`
	Rollup           *MetricsRollupStatus `json:"rollup,omitempty"`
	Minutes          []MetricCount        `json:"minutes"`
	Hours            []MetricCount        `json:"hours"`
	MinutesTruncated bool                 `json:"minutes_truncated,omitempty"`
	HoursTruncated   bool                 `json:"hours_truncated,omitempty"`
	History          []HistoryTableStatus `json:"history"`
}

const (
	metricsReportMinutes = 60
	metricsReportHours   = 48
	metricsReportRows    = 5000
)

// MetricsReport 只读：最近 60 个分钟桶与 48 个小时桶、聚合水位及各历史表的行数和最早时间。
func (s *Store) MetricsReport(ctx context.Context, core string, revision int64) (MetricsReport, error) {
	report := MetricsReport{CoreInstance: core, ConfigRevision: revision, Minutes: []MetricCount{}, Hours: []MetricCount{}, History: []HistoryTableStatus{}}
	if !validRef(core) || revision < 1 {
		return report, ErrConfigRevision
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return report, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, core, revision); err != nil {
		return report, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&report.ObservedAt); err != nil {
		return report, err
	}
	report.ObservedAt = report.ObservedAt.UTC()
	history := []struct{ table, query string }{
		{"observations", `SELECT count(*),min(recorded_at) FROM dispatch.observations WHERE core_instance=$1`},
		{"probes", `SELECT count(*),min(reserved_at) FROM dispatch.probes WHERE core_instance=$1`},
	}
	report.SchemaReady, err = metricsSchemaReady(ctx, tx)
	if err != nil {
		return report, err
	}
	if report.SchemaReady {
		history = append(history,
			struct{ table, query string }{"metric_minutes", `SELECT count(*),min(bucket) FROM dispatch.metric_minutes WHERE core_instance=$1`},
			struct{ table, query string }{"metric_hours", `SELECT count(*),min(bucket) FROM dispatch.metric_hours WHERE core_instance=$1`})
		status := new(MetricsRollupStatus)
		err := tx.QueryRowContext(ctx, `SELECT watermark,started_at,updated_at FROM dispatch.metric_rollups WHERE core_instance=$1`, core).
			Scan(&status.Watermark, &status.StartedAt, &status.UpdatedAt)
		if err == nil {
			status.Watermark, status.StartedAt, status.UpdatedAt = status.Watermark.UTC(), status.StartedAt.UTC(), status.UpdatedAt.UTC()
			status.LagSeconds = int64(report.ObservedAt.Sub(status.Watermark) / time.Second)
			report.Rollup = status
		} else if !errors.Is(err, sql.ErrNoRows) {
			return report, err
		}
		if report.Minutes, report.MinutesTruncated, err = readMetricCounts(ctx, tx, "metric_minutes", core,
			report.ObservedAt.Truncate(time.Minute).Add(-metricsReportMinutes*time.Minute)); err != nil {
			return report, err
		}
		if report.Hours, report.HoursTruncated, err = readMetricCounts(ctx, tx, "metric_hours", core,
			report.ObservedAt.Truncate(time.Hour).Add(-metricsReportHours*time.Hour)); err != nil {
			return report, err
		}
	}
	for _, item := range history {
		status := HistoryTableStatus{Table: item.table}
		var oldest sql.NullTime
		if err := tx.QueryRowContext(ctx, item.query, core).Scan(&status.Rows, &oldest); err != nil {
			return report, err
		}
		if oldest.Valid {
			at := oldest.Time.UTC()
			status.Oldest = &at
		}
		report.History = append(report.History, status)
	}
	return report, tx.Commit()
}

func readMetricCounts(ctx context.Context, tx *sql.Tx, table, core string, since time.Time) ([]MetricCount, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT bucket,series,label,count FROM dispatch.`+table+`
	 WHERE core_instance=$1 AND bucket>=$2 ORDER BY bucket DESC,series,label LIMIT $3`, core, since, metricsReportRows+1)
	if err != nil {
		return nil, false, err
	}
	counts := []MetricCount{}
	truncated := false
	for rows.Next() {
		if len(counts) == metricsReportRows {
			truncated = true
			break
		}
		var count MetricCount
		if err := rows.Scan(&count.Bucket, &count.Series, &count.Label, &count.Count); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		count.Bucket = count.Bucket.UTC()
		counts = append(counts, count)
	}
	if err := closeRows(rows); err != nil {
		return nil, false, err
	}
	sort.SliceStable(counts, func(i, j int) bool { return counts[i].Bucket.Before(counts[j].Bucket) })
	return counts, truncated, nil
}
