package scheduledowner

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

type runIdentity struct {
	planID, epoch int64
	owner, digest string
	slot          time.Time
	managed       bool
}

// Run holds a dedicated PostgreSQL session until Close. Its exported fields are
// metadata for Core's fresh repository read, never mutable authority.
type Run struct {
	PlanID                      int64
	Epoch                       int64
	Owner                       string
	Slot                        time.Time
	Managed                     bool
	store                       *Store
	conn                        *sql.Conn
	identity                    runIdentity
	mu                          sync.Mutex
	closed, finished, validated bool
}

func (s *Store) Acquire(ctx context.Context, id int64, owner string) (*Run, error) {
	if owner != Legacy && owner != Dispatch {
		return nil, ErrNotAdmitted
	}
	conn, err := s.lock(ctx, id)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = unlock(conn, id)
		}
	}()
	tx, err := s.begin(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := lockedStatus(ctx, tx, id)
	managed := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if !managed {
		if owner != Legacy {
			return nil, ErrNotAdmitted
		}
		st = Status{PlanID: id, Owner: Legacy, State: Legacy}
	}
	if st.Owner != owner || st.State != owner {
		return nil, ErrNotAdmitted
	}
	if st.Running != 0 {
		return nil, ErrPending
	}
	snap, err := capture(ctx, tx, id, false)
	if err != nil {
		return nil, err
	}
	if managed && snap.Digest != st.PlanDigest {
		return nil, ErrChanged
	}
	if !snap.Enabled || snap.Due == nil {
		return nil, ErrNotAdmitted
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}
	if snap.Due.After(now) {
		return nil, ErrNotAdmitted
	}
	slot := snap.Due.UTC().Truncate(time.Minute)
	if owner == Dispatch {
		var epoch int64
		var queuedSlot time.Time
		var digest string
		err = tx.QueryRowContext(ctx, `SELECT epoch,slot,plan_digest FROM scheduled_owner.runs WHERE plan_id=$1 AND state='queued' FOR UPDATE`, id).Scan(&epoch, &queuedSlot, &digest)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotAdmitted
		}
		if err != nil {
			return nil, err
		}
		if epoch != st.Epoch {
			return nil, ErrEpoch
		}
		if !queuedSlot.Equal(slot) || digest != snap.Digest {
			return nil, ErrChanged
		}
		_, err = tx.ExecContext(ctx, `UPDATE scheduled_owner.runs SET state='running',started_at=clock_timestamp() WHERE plan_id=$1 AND slot=$2`, id, slot)
	} else {
		var running int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_owner.runs WHERE plan_id=$1 AND state IN ('queued','running')`, id).Scan(&running); err != nil {
			return nil, err
		}
		if running != 0 {
			return nil, ErrPending
		}
		var res sql.Result
		res, err = tx.ExecContext(ctx, `INSERT INTO scheduled_owner.runs(plan_id,slot,epoch,owner,state,plan_digest,started_at)
  VALUES($1,$2,$3,'legacy','running',$4,clock_timestamp()) ON CONFLICT DO NOTHING`, id, slot, st.Epoch, snap.Digest)
		if err == nil {
			var n int64
			n, err = res.RowsAffected()
			if err == nil && n == 0 {
				return nil, ErrNotAdmitted
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err = audit(ctx, tx, id, st.Epoch, "acquire", "", map[string]any{"owner": owner, "slot": slot, "managed": managed}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	run := &Run{PlanID: id, Epoch: st.Epoch, Owner: owner, Slot: slot, Managed: managed, store: s, conn: conn, identity: runIdentity{planID: id, epoch: st.Epoch, owner: owner, slot: slot, managed: managed, digest: snap.Digest}}
	keep = true
	return run, nil
}

func (r *Run) check(ctx context.Context, q querier, checkPlan bool) error {
	id := r.identity
	if _, err := databaseID(ctx, q, r.store.databaseID); err != nil {
		return err
	}
	var held bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted
 AND classid::bigint=(($1::bigint>>32)&4294967295) AND objid::bigint=($1::bigint&4294967295) AND objsubid=1)`, planLock(id.planID)).Scan(&held)
	if err != nil {
		return err
	}
	if !held {
		return ErrBusy
	}
	var valid bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_owner.runs r WHERE r.plan_id=$1 AND r.slot=$2 AND r.epoch=$3 AND r.owner=$4 AND r.state='running'
 AND (EXISTS(SELECT 1 FROM scheduled_owner.plans p WHERE p.plan_id=r.plan_id AND p.owner=r.owner
 AND ((p.state=p.owner AND p.epoch=r.epoch) OR (p.state='draining' AND p.epoch=r.epoch+1)))
 OR (r.epoch=0 AND r.owner='legacy' AND NOT EXISTS(SELECT 1 FROM scheduled_owner.plans p WHERE p.plan_id=r.plan_id))))`, id.planID, id.slot, id.epoch, id.owner).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrNotAdmitted
	}
	if checkPlan {
		snap, err := capture(ctx, q, id.planID, false)
		if err != nil {
			return err
		}
		if snap.Digest != id.digest {
			return ErrChanged
		}
		if !r.validated && (snap.Due == nil || !snap.Due.UTC().Truncate(time.Minute).Equal(id.slot)) {
			return ErrChanged
		}
	}
	return nil
}

func (r *Run) Validate(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.finished {
		return ErrNotAdmitted
	}
	tx, err := r.store.begin(ctx, r.conn)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.check(ctx, tx, true); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.validated = true
	return nil
}

// Finish only marks a slot completed after Core has persisted its original
// result, grading effects and progress. Any partial failure stays unresolved
// until an explicit acknowledgement; raw error strings are never persisted.
func (r *Run) Finish(ctx context.Context, resultID int64, runErr error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.finished {
		return ErrNotAdmitted
	}
	if resultID < 0 {
		return ErrNotAdmitted
	}
	tx, err := r.store.begin(ctx, r.conn)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = r.check(ctx, tx, runErr == nil); err != nil {
		return err
	}
	id := r.identity
	if resultID > 0 {
		var own bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.scheduled_test_results x JOIN scheduled_owner.runs r ON r.plan_id=x.plan_id
  WHERE x.id=$1 AND r.plan_id=$2 AND r.slot=$3 AND x.created_at>=r.started_at)`, resultID, id.planID, id.slot).Scan(&own)
		if err != nil {
			return err
		}
		if !own {
			return ErrNotAdmitted
		}
	}
	state, outcome, action := "completed", "finished", "finish"
	if runErr != nil {
		state, outcome, action = "running", "unresolved_execution_error", "execution_unresolved"
	}
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_owner.runs SET state=$3,result_id=NULLIF($4::bigint,0::bigint),outcome=$5,
 finished_at=CASE WHEN $3='completed' THEN clock_timestamp() ELSE NULL END WHERE plan_id=$1 AND slot=$2`, id.planID, id.slot, state, resultID, outcome)
	if err != nil {
		return err
	}
	if id.managed && runErr == nil {
		snap, err := capture(ctx, tx, id.planID, true)
		if err != nil {
			return err
		}
		if snap.Digest != id.digest {
			return ErrChanged
		}
		_, err = tx.ExecContext(ctx, `UPDATE scheduled_owner.plans SET snapshot=$2,results_count=$3,results_max_id=$4,results_latest_at=$5,results_digest=$6,observed_at=clock_timestamp() WHERE plan_id=$1`, id.planID, snap.JSON, snap.Count, snap.MaxID, snap.Latest, snap.ResultsDigest)
		if err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, id.planID, id.epoch, action, "", map[string]any{"slot": id.slot, "owner": id.owner, "result_id": resultID, "outcome": outcome}); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if runErr != nil {
		return ErrPending
	}
	r.finished = true
	return nil
}

func (r *Run) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return unlock(r.conn, r.identity.planID)
}
