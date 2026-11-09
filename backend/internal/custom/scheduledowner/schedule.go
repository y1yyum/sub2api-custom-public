package scheduledowner

import (
	"context"
	"errors"
	"sort"
)

const scheduleLock int64 = 739438781220015
const MaxPending = 16

func sortedBindings(bindings []Binding) ([]Binding, error) {
	if len(bindings) > MaxBatch {
		return nil, errors.New("scheduled owner batch exceeds 1000 bindings")
	}
	out := append([]Binding(nil), bindings...)
	sort.Slice(out, func(i, j int) bool { return out[i].PlanID < out[j].PlanID })
	dedup := out[:0]
	for _, b := range out {
		if b.PlanID <= 0 || b.Epoch <= 0 {
			return nil, ErrEpoch
		}
		if len(dedup) > 0 && dedup[len(dedup)-1].PlanID == b.PlanID {
			if dedup[len(dedup)-1].Epoch != b.Epoch {
				return nil, ErrEpoch
			}
			continue
		}
		dedup = append(dedup, b)
	}
	return dedup, nil
}

// Schedule only inserts queue records. It never edits owner epochs, old plans,
// results, grading state, credentials or execution outcomes.
func (s *Store) Schedule(ctx context.Context, bindings []Binding) (int, error) {
	bindings, err := sortedBindings(bindings)
	if err != nil {
		return 0, err
	}
	if len(bindings) == 0 {
		return 0, nil
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, scheduleLock); err != nil {
		return 0, err
	}
	// Validate the whole configuration before inserting any member.
	for _, b := range bindings {
		st, err := scanStatus(tx.QueryRowContext(ctx, statusSQL+` WHERE p.plan_id=$1`, b.PlanID))
		if err != nil {
			return 0, err
		}
		if st.Epoch != b.Epoch {
			return 0, ErrEpoch
		}
		if st.Owner != Dispatch || st.State != Dispatch {
			return 0, ErrNotAdmitted
		}
	}
	count := 0
	for _, b := range bindings {
		var held bool
		if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, planLock(b.PlanID)).Scan(&held); err != nil {
			return 0, err
		}
		if !held {
			continue
		}
		var pending int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_owner.runs WHERE owner='dispatch' AND state IN ('queued','running')`).Scan(&pending); err != nil {
			return 0, err
		}
		if pending >= MaxPending {
			break
		}
		st, err := scanStatus(tx.QueryRowContext(ctx, statusSQL+` WHERE p.plan_id=$1`, b.PlanID))
		if err != nil {
			return 0, err
		}
		if st.Queued != 0 || st.Running != 0 {
			continue
		}
		snap, err := capture(ctx, tx, b.PlanID, false)
		if err != nil {
			return 0, err
		}
		if snap.Digest != st.PlanDigest {
			return 0, ErrChanged
		}
		if !snap.Enabled || snap.Due == nil {
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO scheduled_owner.queue(plan_id,slot,epoch,owner,state,plan_digest)
   SELECT $1,date_trunc('minute',$2::timestamptz),$3,'dispatch','queued',$4 WHERE $2<=clock_timestamp()
   ON CONFLICT DO NOTHING`, b.PlanID, snap.Due, b.Epoch, snap.Digest)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		count += int(n)
	}
	return count, tx.Commit()
}

func (s *Store) ListQueued(ctx context.Context, limit int) ([]int64, error) {
	if limit < 1 || limit > MaxBatch {
		return nil, errors.New("scheduled owner queue limit must be 1..1000")
	}
	if _, err := s.DatabaseID(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.plan_id FROM scheduled_owner.runs r JOIN scheduled_owner.plans p ON p.plan_id=r.plan_id
 WHERE r.state='queued' AND r.owner='dispatch' AND p.state='dispatch' AND p.owner='dispatch' AND p.epoch=r.epoch
 ORDER BY r.created_at,r.plan_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
