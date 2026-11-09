package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// 每批独立事务、每轮有限批次：中断后下一轮从剩余最旧行继续，重复执行只删除仍满足条件的行。
const (
	retentionBatchRows  = 1000
	retentionMaxBatches = 10
)

// retentionRule 记录 dispatch schema 每张表的清理归属；新增表必须在此分类。
type retentionRule struct {
	Table   string
	Cleaner string // retention：本功能按配置清理；quality：既有质量派生记录清理；空：永不清理
	Keep    string
}

var retentionRules = []retentionRule{
	{"observations", "retention", "被隔离或隔离审计引用的触发事件；未聚合（不早于聚合水位）的行"},
	{"probes", "retention", "未结束租约（含过期 Unknown）；带金额记录的探针；每个账号×模型×区域最新一次探针；未聚合的行；结束或结果观测不早于聚合水位的探针"},
	{"metric_minutes", "retention", "分钟保留期内的桶"},
	{"metric_hours", "retention", "小时保留期内的桶"},
	{"quality_results", "quality", "既有逻辑只清七日前的质量派生记录，本功能不改变"},
	{"cores", "", "配置版本、摘要与持久 probe policy"},
	{"targets", "", "Health JSON 与候选元数据"},
	{"quality_configs", "", "质量激活版本"},
	{"quality_baselines", "", "校准基线摘要"},
	{"health_holds", "", "隔离定义与版本（数据库触发器禁止删除）"},
	{"health_hold_audit", "", "隔离触发与解除审计（仅追加）"},
	{"probe_budget_latch", "", "金额预算启用 latch（不可变）"},
	{"probe_budget_policies", "", "各 Core 金额策略"},
	{"probe_prices", "", "报价上界（不可变）"},
	{"probe_money", "", "预留／未知／计额记录（禁止删除）"},
	{"probe_money_audit", "", "金额审计（不可变）"},
	{"publish_lineage", "", "快照发布 lineage（单行，仅显式轮换）"},
	{"metric_rollups", "", "聚合水位"},
	{"schema_migrations", "", "迁移校验和"},
}

var retentionStatements = map[string]string{
	"observations": `DELETE FROM dispatch.observations o USING (
	 SELECT x.event_id FROM dispatch.observations x
	 WHERE x.core_instance=$1 AND x.recorded_at<$2
	 AND NOT EXISTS (SELECT 1 FROM dispatch.health_holds h WHERE h.core_instance=x.core_instance AND h.trigger_event_id=x.event_id)
	 AND NOT EXISTS (SELECT 1 FROM dispatch.health_hold_audit a WHERE a.core_instance=x.core_instance AND a.event_id=x.event_id)
	 ORDER BY x.recorded_at LIMIT $3) d
	 WHERE o.core_instance=$1 AND o.event_id=d.event_id`,
	"probes": `DELETE FROM dispatch.probes p USING (
	 SELECT x.token FROM dispatch.probes x, (SELECT COALESCE($4::timestamptz,'infinity') AS at) w
	 WHERE x.core_instance=$1 AND x.reserved_at<$2 AND x.finished_at IS NOT NULL
	 AND NOT EXISTS (SELECT 1 FROM dispatch.probe_money m WHERE m.token=x.token)
	 AND x.finished_at<w.at AND NOT EXISTS (SELECT 1 FROM dispatch.observations o
	  WHERE o.core_instance=x.core_instance AND o.event_id=x.attempt_id AND o.recorded_at>=w.at)
	 AND EXISTS (SELECT 1 FROM dispatch.probes n WHERE n.core_instance=x.core_instance AND n.account_id=x.account_id
	  AND n.model=x.model AND n.region=x.region AND n.reserved_at>x.reserved_at)
	 ORDER BY x.reserved_at LIMIT $3) d
	 WHERE p.token=d.token`,
	"metric_minutes": `DELETE FROM dispatch.metric_minutes m USING (
	 SELECT bucket,series,label FROM dispatch.metric_minutes WHERE core_instance=$1 AND bucket<$2 ORDER BY bucket LIMIT $3) d
	 WHERE m.core_instance=$1 AND m.bucket=d.bucket AND m.series=d.series AND m.label=d.label`,
	"metric_hours": `DELETE FROM dispatch.metric_hours m USING (
	 SELECT bucket,series,label FROM dispatch.metric_hours WHERE core_instance=$1 AND bucket<$2 ORDER BY bucket LIMIT $3) d
	 WHERE m.core_instance=$1 AND m.bucket=d.bucket AND m.series=d.series AND m.label=d.label`,
}

type RetentionTableResult struct {
	Table   string    `json:"table"`
	Cutoff  time.Time `json:"cutoff"`
	Deleted int64     `json:"deleted"`
	// Pending 表示达到本轮批次上限，仍可能有可删除行留待下一轮。
	Pending bool `json:"pending,omitempty"`
}

type retentionPlan struct {
	table string
	days  int
	raw   bool
}

// ApplyRetention 按显式保留天数分批清理；原始历史的截止点不晚于聚合水位，
// 保证被删除的行已计入分钟／小时聚合。
func (s *Store) ApplyRetention(ctx context.Context, core string, cfg MonitoringConfig) ([]RetentionTableResult, error) {
	if !validRef(core) {
		return nil, errors.New("invalid dispatch core identity")
	}
	var plans []retentionPlan
	if r := cfg.Retention; r != nil {
		plans = append(plans, retentionPlan{"observations", r.ObservationDays, true}, retentionPlan{"probes", r.ProbeDays, true})
	}
	if a := cfg.Aggregation; a != nil {
		plans = append(plans, retentionPlan{"metric_minutes", a.MinuteRetentionDays, false}, retentionPlan{"metric_hours", a.HourRetentionDays, false})
	}
	results := []RetentionTableResult{}
	for _, plan := range plans {
		if plan.days == 0 {
			continue
		}
		result := RetentionTableResult{Table: plan.table}
		for batch := 0; ; batch++ {
			if batch == retentionMaxBatches {
				result.Pending = true
				break
			}
			cutoff, deleted, err := s.retentionBatch(ctx, core, plan)
			if err != nil {
				// 已提交的批次仍计入删除总数；该表仍有未清理行。
				if result.Deleted > 0 {
					result.Pending = true
					results = append(results, result)
				}
				return results, err
			}
			result.Cutoff = cutoff
			result.Deleted += deleted
			if deleted < retentionBatchRows {
				break
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Store) retentionBatch(ctx context.Context, core string, plan retentionPlan) (time.Time, int64, error) {
	tx, now, ctx, cancel, err := s.beginMaintenance(ctx)
	if err != nil {
		return time.Time{}, 0, err
	}
	defer cancel()
	defer func() { _ = tx.Rollback() }()
	ready, err := metricsSchemaReady(ctx, tx)
	if err != nil {
		return time.Time{}, 0, err
	}
	if !ready {
		return time.Time{}, 0, ErrMetricsSchema
	}
	cutoff := now.Add(-time.Duration(plan.days) * 24 * time.Hour)
	args := []any{core, cutoff, retentionBatchRows}
	if plan.raw {
		var watermark sql.NullTime
		err := tx.QueryRowContext(ctx, `SELECT watermark FROM dispatch.metric_rollups WHERE core_instance=$1`, core).Scan(&watermark)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, 0, err
		}
		if watermark.Valid && watermark.Time.Before(cutoff) {
			cutoff = watermark.Time.UTC()
		}
		args[1] = cutoff
		// 探针的结果观测在水位之后时，聚合仍需以 attempt_id 判定其来源。
		if plan.table == "probes" {
			args = append(args, watermark)
		}
	}
	result, err := tx.ExecContext(ctx, retentionStatements[plan.table], args...)
	if err != nil {
		return time.Time{}, 0, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return time.Time{}, 0, err
	}
	return cutoff, deleted, tx.Commit()
}
