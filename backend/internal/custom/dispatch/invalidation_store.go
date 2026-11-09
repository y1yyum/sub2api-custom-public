package dispatch

import (
	"context"
	"database/sql"
	"errors"
)

var ErrInvalidationCoreMissing = errors.New("dispatch invalidation core row missing")

// HoldInvalidationState 在只读 REPEATABLE READ 事务中一次读取 lineage、已提交快照 revision 与活动 hold，
// 不取调度全局锁。hold 触发与发布都在全局锁内串行提交，因此同一读取视图中：
// revision 大于读到的 revision 的快照必然在 hold 提交后构建并已体现该 hold。
// 记录的恢复检测身份与当前库不符时（逻辑恢复后尚未发布），lineage 视为未知，本次不产生失效项。
func (s *Store) HoldInvalidationState(ctx context.Context, core string) (HoldInvalidationState, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return HoldInvalidationState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	identity, err := currentPublishLineageIdentity(ctx, tx)
	if err != nil {
		return HoldInvalidationState{}, err
	}
	var lineage sql.NullString
	var recorded publishLineageIdentity
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT c.revision, l.lineage::text, COALESCE(l.system_identifier,0), COALESCE(l.database_oid,0)
	 FROM dispatch.cores c LEFT JOIN dispatch.publish_lineage l ON l.singleton WHERE c.core_instance=$1`, core).
		Scan(&revision, &lineage, &recorded.system, &recorded.database)
	if errors.Is(err, sql.ErrNoRows) {
		return HoldInvalidationState{}, ErrInvalidationCoreMissing
	}
	if err != nil {
		return HoldInvalidationState{}, err
	}
	if !lineage.Valid || recorded != identity {
		return HoldInvalidationState{}, nil
	}
	holds, err := loadHealthHolds(ctx, tx, core)
	if err != nil {
		return HoldInvalidationState{}, err
	}
	active := make([]HealthHoldReport, 0, len(holds))
	for _, hold := range holds {
		if hold.Active {
			active = append(active, hold)
		}
	}
	return HoldInvalidationState{PublishLineage: lineage.String, SnapshotRevision: revision, Holds: active}, tx.Commit()
}
