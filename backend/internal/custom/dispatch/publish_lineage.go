package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrPublishLineageSchema = errors.New("dispatch publish lineage requires schema 005")

// 发布 epoch 为 (publish_lineage, revision)。lineage 随调度库保存；记录的恢复检测身份
// （集群 system_identifier 与库 OID）与当前不符时换新：逻辑恢复到另一集群或库、换库均会触发。
// 独立集群可能分配到相同库 OID，因此单比库 OID 不足以识别跨集群恢复。
// 同一集群内的物理恢复／PITR 两者皆不变，仍需操作者显式轮换。
type publishLineageIdentity struct {
	system   int64
	database int64
}

func currentPublishLineage(ctx context.Context, tx *sql.Tx, now time.Time) (string, error) {
	identity, err := currentPublishLineageIdentity(ctx, tx)
	if err != nil {
		return "", err
	}
	var lineage string
	var recorded publishLineageIdentity
	err = tx.QueryRowContext(ctx, `SELECT lineage::text, system_identifier, database_oid FROM dispatch.publish_lineage WHERE singleton`).
		Scan(&lineage, &recorded.system, &recorded.database)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return writePublishLineage(ctx, tx, identity, "initial", now)
	case err != nil:
		return "", err
	case recorded != identity:
		return writePublishLineage(ctx, tx, identity, "database_identity_changed", now)
	}
	return lineage, nil
}

// currentPublishLineageIdentity 读取失败（含无权执行 pg_control_system）即返回错误，
// 不在无法确认身份时沿用旧 lineage。
func currentPublishLineageIdentity(ctx context.Context, tx queryRower) (publishLineageIdentity, error) {
	var ready bool
	var identity publishLineageIdentity
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('dispatch.publish_lineage') IS NOT NULL,
	 (SELECT system_identifier FROM pg_control_system()),
	 (SELECT oid::bigint FROM pg_database WHERE datname=current_database())`).Scan(&ready, &identity.system, &identity.database); err != nil {
		return publishLineageIdentity{}, fmt.Errorf("dispatch publish lineage identity unavailable: %w", err)
	}
	if !ready {
		return publishLineageIdentity{}, ErrPublishLineageSchema
	}
	return identity, nil
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func writePublishLineage(ctx context.Context, tx *sql.Tx, identity publishLineageIdentity, reason string, now time.Time) (string, error) {
	lineage := uuid.NewString()
	_, err := tx.ExecContext(ctx, `INSERT INTO dispatch.publish_lineage(singleton,lineage,system_identifier,database_oid,reason,started_at)
	 VALUES (TRUE,$1,$2,$3,$4,$5) ON CONFLICT (singleton) DO UPDATE SET lineage=EXCLUDED.lineage,
	 system_identifier=EXCLUDED.system_identifier,database_oid=EXCLUDED.database_oid,reason=EXCLUDED.reason,started_at=EXCLUDED.started_at`,
		lineage, identity.system, identity.database, reason, now)
	if err != nil {
		return "", err
	}
	return lineage, nil
}

// RotatePublishLineage 供物理恢复或其他无法自动识别的回滚后显式换新 lineage。
func (s *Store) RotatePublishLineage(ctx context.Context) (string, error) {
	tx, now, err := s.begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	identity, err := currentPublishLineageIdentity(ctx, tx)
	if err != nil {
		return "", err
	}
	lineage, err := writePublishLineage(ctx, tx, identity, "operator_rotation", now)
	if err != nil {
		return "", err
	}
	return lineage, tx.Commit()
}

// CheckPublishLineageSchema 供启用快照订阅的 --once／--run 在启动时确认 005 已应用且恢复检测身份可读，
// 避免进程起来后每个工作轮次才失败。
func (s *Store) CheckPublishLineageSchema(ctx context.Context) error {
	_, err := currentPublishLineageIdentity(ctx, s.db)
	return err
}
