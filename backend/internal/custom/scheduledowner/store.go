package scheduledowner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	Legacy   = "legacy"
	Dispatch = "dispatch"
	Draining = "draining"
	MaxBatch = 1000
)

var (
	ErrNotAdmitted = errors.New("scheduled owner admission denied")
	ErrBusy        = errors.New("scheduled owner plan busy")
	ErrEpoch       = errors.New("scheduled owner epoch mismatch")
	ErrChanged     = errors.New("scheduled owner plan or questions changed")
	ErrPending     = errors.New("scheduled owner unresolved running slot")
	ErrDatabase    = errors.New("scheduled owner database identity mismatch")
)

type Binding struct {
	PlanID int64 `json:"plan_id"`
	Epoch  int64 `json:"owner_epoch"`
}

type Status struct {
	DatabaseID      string     `json:"database_id"`
	PlanID          int64      `json:"plan_id"`
	Epoch           int64      `json:"epoch"`
	Owner           string     `json:"owner"`
	State           string     `json:"state"`
	PlanDigest      string     `json:"plan_digest"`
	ResultsCount    int64      `json:"results_count"`
	ResultsMaxID    int64      `json:"results_max_id"`
	ResultsLatestAt *time.Time `json:"results_latest_at"`
	ResultsDigest   string     `json:"results_digest"`
	ObservedAt      time.Time  `json:"observed_at"`
	Queued          int        `json:"queued"`
	Running         int        `json:"running"`
	LastSlot        *time.Time `json:"last_slot,omitempty"`
	LastRunState    string     `json:"last_run_state,omitempty"`
}

type Store struct {
	db         *sql.DB
	databaseID string
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// ForDatabase returns an immutable binding; mutations recheck it inside their transaction.
func (s *Store) ForDatabase(id string) *Store { return &Store{db: s.db, databaseID: id} }

func databaseID(ctx context.Context, q querier, expected string) (string, error) {
	var id string
	if err := q.QueryRowContext(ctx, `SELECT database_id::text FROM scheduled_owner.database_identity WHERE singleton`).Scan(&id); err != nil {
		return "", err
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", ErrDatabase
	}
	if expected != "" && id != expected {
		return "", ErrDatabase
	}
	return id, nil
}

func (s *Store) DatabaseID(ctx context.Context) (string, error) {
	return databaseID(ctx, s.db, s.databaseID)
}
func (s *Store) CheckDatabase(ctx context.Context, id string) error {
	if id == "" {
		return ErrDatabase
	}
	_, err := databaseID(ctx, s.db, id)
	return err
}

func (s *Store) begin(ctx context.Context, conn *sql.Conn) (*sql.Tx, error) {
	var tx *sql.Tx
	var err error
	if conn == nil {
		tx, err = s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	} else {
		tx, err = conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SET LOCAL TIME ZONE 'UTC'`); err == nil {
		_, err = databaseID(ctx, tx, s.databaseID)
	}
	if err == nil {
		var applied bool
		applied, err = checkLedger(ctx, tx)
		if err == nil && !applied {
			err = errors.New("scheduled owner schema not applied")
		}
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func planLock(id int64) int64 {
	sum := sha256.Sum256([]byte(fmt.Sprintf("sub2api/scheduled-owner/plan/%d", id)))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func (s *Store) lock(ctx context.Context, id int64) (*sql.Conn, error) {
	if id <= 0 {
		return nil, ErrNotAdmitted
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var held bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, planLock(id)).Scan(&held); err != nil {
		// PostgreSQL may have acquired the lock before its acknowledgement failed.
		discard(conn)
		return nil, err
	}
	if !held {
		_ = conn.Close()
		return nil, ErrBusy
	}
	return conn, nil
}

func unlock(conn *sql.Conn, id int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var released bool
	err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, planLock(id)).Scan(&released)
	if err != nil || !released {
		// Never return a session with an uncertain advisory lock to the pool.
		discard(conn)
		if err != nil {
			return err
		}
		return ErrBusy
	}
	return conn.Close()
}

func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

func idsSorted(ids []int64) ([]int64, error) {
	if len(ids) > MaxBatch {
		return nil, errors.New("scheduled owner batch exceeds 1000 plans")
	}
	ids = append([]int64{}, ids...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := ids[:0]
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrNotAdmitted
		}
		if len(out) == 0 || out[len(out)-1] != id {
			out = append(out, id)
		}
	}
	return out, nil
}

const statusSQL = `SELECT p.plan_id,p.epoch,p.owner,p.state,p.plan_digest,p.results_count,p.results_max_id,p.results_latest_at,p.results_digest,p.observed_at,
 (SELECT count(*) FROM scheduled_owner.runs r WHERE r.plan_id=p.plan_id AND r.state='queued'),
 (SELECT count(*) FROM scheduled_owner.runs r WHERE r.plan_id=p.plan_id AND r.state='running')
 FROM scheduled_owner.plans p`

func scanStatus(row interface{ Scan(...any) error }) (Status, error) {
	var st Status
	err := row.Scan(&st.PlanID, &st.Epoch, &st.Owner, &st.State, &st.PlanDigest, &st.ResultsCount, &st.ResultsMaxID, &st.ResultsLatestAt, &st.ResultsDigest, &st.ObservedAt, &st.Queued, &st.Running)
	return st, err
}

func (s *Store) Status(ctx context.Context, ids []int64) ([]Status, error) {
	ids, err := idsSorted(ids)
	if err != nil {
		return nil, err
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	dbID, err := databaseID(ctx, tx, s.databaseID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT * FROM (`+statusSQL+` UNION ALL
 SELECT r.plan_id,0::bigint,'legacy','legacy',max(r.plan_digest),0::bigint,0::bigint,NULL::timestamptz,'',max(r.created_at),
 count(*) FILTER(WHERE r.state='queued'),count(*) FILTER(WHERE r.state='running')
 FROM scheduled_owner.runs r WHERE NOT EXISTS(SELECT 1 FROM scheduled_owner.plans p WHERE p.plan_id=r.plan_id)
 GROUP BY r.plan_id) summary WHERE cardinality($1::bigint[])=0 OR summary.plan_id=ANY($1) ORDER BY summary.plan_id LIMIT 1001`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Status, 0)
	for rows.Next() {
		st, err := scanStatus(rows)
		if err != nil {
			return nil, err
		}
		st.DatabaseID = dbID
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(out) > MaxBatch {
		return nil, errors.New("scheduled owner status requires explicit plan selection")
	}
	for i := range out {
		err := tx.QueryRowContext(ctx, `SELECT slot,state FROM scheduled_owner.runs WHERE plan_id=$1 ORDER BY created_at DESC,slot DESC LIMIT 1`, out[i].PlanID).Scan(&out[i].LastSlot, &out[i].LastRunState)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return out, tx.Commit()
}

func (s *Store) IsManaged(ctx context.Context, id int64) (bool, error) {
	if _, err := s.DatabaseID(ctx); err != nil {
		return false, err
	}
	var managed bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_owner.plans WHERE plan_id=$1)`, id).Scan(&managed)
	return managed, err
}

func audit(ctx context.Context, tx *sql.Tx, id, epoch int64, action, reason string, facts any) error {
	body, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_owner.audit(plan_id,epoch,action,reason,facts) VALUES($1,$2,$3,$4,$5)`, id, epoch, action, reason, body)
	return err
}

func lockedStatus(ctx context.Context, tx *sql.Tx, id int64) (Status, error) {
	return scanStatus(tx.QueryRowContext(ctx, statusSQL+` WHERE p.plan_id=$1 FOR UPDATE OF p`, id))
}

func (s *Store) Import(ctx context.Context, ids []int64) ([]Status, error) {
	ids, err := idsSorted(ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		conn, err := s.lock(ctx, id)
		if err != nil {
			return nil, err
		}
		err = s.importOne(ctx, conn, id)
		closeErr := unlock(conn, id)
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return s.Status(ctx, ids)
}

func (s *Store) importOne(ctx context.Context, conn *sql.Conn, id int64) error {
	tx, err := s.begin(ctx, conn)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = lockedStatus(ctx, tx, id)
	if err == nil {
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var running int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_owner.runs WHERE plan_id=$1 AND state='running'`, id).Scan(&running); err != nil {
		return err
	}
	if running != 0 {
		return ErrPending
	}
	snap, err := capture(ctx, tx, id, true)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_owner.plans(plan_id,epoch,owner,state,snapshot,import_snapshot,plan_digest,results_count,results_max_id,results_latest_at,results_digest)
 VALUES($1,1,'legacy','legacy',$2,$2,$3,$4,$5,$6,$7)`, id, snap.JSON, snap.Digest, snap.Count, snap.MaxID, snap.Latest, snap.ResultsDigest)
	if err != nil {
		return err
	}
	st := Status{PlanID: id, Epoch: 1, Owner: Legacy, State: Legacy, PlanDigest: snap.Digest, ResultsCount: snap.Count, ResultsMaxID: snap.MaxID, ResultsLatestAt: snap.Latest, ResultsDigest: snap.ResultsDigest}
	if err = audit(ctx, tx, id, 1, "import", "", st); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Freeze(ctx context.Context, id, expected int64) (Status, error) {
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, scheduleLock); err != nil {
		return Status{}, err
	}
	st, err := lockedStatus(ctx, tx, id)
	if err != nil {
		return st, err
	}
	if st.Epoch != expected {
		return st, ErrEpoch
	}
	if st.State != Draining {
		if _, err = tx.ExecContext(ctx, `UPDATE scheduled_owner.plans SET state='draining',epoch=epoch+1 WHERE plan_id=$1`, id); err != nil {
			return st, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE scheduled_owner.runs SET state='cancelled',outcome='freeze',finished_at=clock_timestamp() WHERE plan_id=$1 AND state='queued'`, id); err != nil {
			return st, err
		}
		st.State = Draining
		st.Epoch++
		st.Queued = 0
		if err = audit(ctx, tx, id, st.Epoch, "freeze", "", st); err != nil {
			return st, err
		}
	}
	st.DatabaseID, err = databaseID(ctx, tx, s.databaseID)
	if err != nil {
		return st, err
	}
	return st, tx.Commit()
}

func (s *Store) Activate(ctx context.Context, id, expected int64, owner string) (Status, error) {
	if owner != Legacy && owner != Dispatch {
		return Status{}, ErrNotAdmitted
	}
	conn, err := s.lock(ctx, id)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = unlock(conn, id) }()
	tx, err := s.begin(ctx, conn)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, scheduleLock); err != nil {
		return Status{}, err
	}
	st, err := lockedStatus(ctx, tx, id)
	if err != nil {
		return st, err
	}
	if st.Epoch != expected {
		return st, ErrEpoch
	}
	if st.State != Draining {
		return st, ErrNotAdmitted
	}
	if st.Running != 0 {
		return st, ErrPending
	}
	snap, err := capture(ctx, tx, id, true)
	if err != nil {
		return st, err
	}
	if snap.Digest != st.PlanDigest {
		return st, ErrChanged
	}
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_owner.plans SET epoch=epoch+1,owner=$2,state=$2,snapshot=$3,plan_digest=$4,
 results_count=$5,results_max_id=$6,results_latest_at=$7,results_digest=$8,observed_at=clock_timestamp() WHERE plan_id=$1`, id, owner, snap.JSON, snap.Digest, snap.Count, snap.MaxID, snap.Latest, snap.ResultsDigest)
	if err != nil {
		return st, err
	}
	st, err = lockedStatus(ctx, tx, id)
	if err != nil {
		return st, err
	}
	st.DatabaseID, err = databaseID(ctx, tx, s.databaseID)
	if err != nil {
		return st, err
	}
	if err = audit(ctx, tx, id, st.Epoch, "activate", "precondition: all legacy runners coordinated or stopped; external verification Unknown", st); err != nil {
		return st, err
	}
	return st, tx.Commit()
}

// Review accepts an explicitly inspected edit only while admission is frozen.
func (s *Store) Review(ctx context.Context, id, expected int64, reason string) (Status, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1024 || strings.ContainsAny(reason, "\x00\r\n") {
		return Status{}, errors.New("scheduled owner review requires a one-line reason of at most 1024 bytes")
	}
	conn, err := s.lock(ctx, id)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = unlock(conn, id) }()
	tx, err := s.begin(ctx, conn)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := lockedStatus(ctx, tx, id)
	if err != nil {
		return st, err
	}
	if st.Epoch != expected {
		return st, ErrEpoch
	}
	if st.State != Draining {
		return st, ErrNotAdmitted
	}
	if st.Running != 0 {
		return st, ErrPending
	}
	snap, err := capture(ctx, tx, id, true)
	if err != nil {
		return st, err
	}
	old := st
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_owner.plans SET epoch=epoch+1,snapshot=$2,plan_digest=$3,results_count=$4,results_max_id=$5,results_latest_at=$6,results_digest=$7,observed_at=clock_timestamp() WHERE plan_id=$1`, id, snap.JSON, snap.Digest, snap.Count, snap.MaxID, snap.Latest, snap.ResultsDigest)
	if err != nil {
		return st, err
	}
	st, err = lockedStatus(ctx, tx, id)
	if err != nil {
		return st, err
	}
	st.DatabaseID, err = databaseID(ctx, tx, s.databaseID)
	if err != nil {
		return st, err
	}
	if err = audit(ctx, tx, id, st.Epoch, "review_edit", reason, map[string]any{"before": old, "after": st}); err != nil {
		return st, err
	}
	return st, tx.Commit()
}

func (s *Store) AckUnknown(ctx context.Context, id, expected int64, reason string) (Status, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1024 || strings.ContainsAny(reason, "\x00\r\n") {
		return Status{}, errors.New("scheduled owner acknowledgement requires a one-line reason of at most 1024 bytes")
	}
	conn, err := s.lock(ctx, id)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = unlock(conn, id) }()
	tx, err := s.begin(ctx, conn)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := lockedStatus(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		st = Status{PlanID: id, Owner: Legacy, State: Legacy}
	} else if err != nil {
		return st, err
	}
	if st.Epoch != expected {
		return st, ErrEpoch
	}
	if st.Epoch != 0 && st.State != Draining {
		return st, ErrNotAdmitted
	}
	res, err := tx.ExecContext(ctx, `UPDATE scheduled_owner.runs SET state='unknown',outcome='operator_acknowledged_unknown',finished_at=clock_timestamp() WHERE plan_id=$1 AND state='running'`, id)
	if err != nil {
		return st, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return st, err
	}
	if n == 0 {
		return st, ErrNotAdmitted
	}
	if err = audit(ctx, tx, id, st.Epoch, "ack_unknown", reason, map[string]any{"runs": n, "automatic_replay": false}); err != nil {
		return st, err
	}
	st.Running = 0
	st.DatabaseID, err = databaseID(ctx, tx, s.databaseID)
	if err != nil {
		return st, err
	}
	return st, tx.Commit()
}
