//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/scheduledowner"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func ownerStoreDB(t *testing.T) (*sql.DB, *scheduledowner.Store) {
	t.Helper()
	_, err := integrationDB.ExecContext(t.Context(), `DROP SCHEMA IF EXISTS scheduled_owner CASCADE`)
	require.NoError(t, err)
	require.NoError(t, scheduledowner.ApplySchema(t.Context(), integrationDB))
	db, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	db.SetMaxOpenConns(30)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := integrationDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS scheduled_owner CASCADE`)
		require.NoError(t, err)
	})
	store := scheduledowner.NewStore(db)
	id, err := store.DatabaseID(t.Context())
	require.NoError(t, err)
	return db, store.ForDatabase(id)
}

func ownerStorePlan(t *testing.T, db *sql.DB, kind string) int64 {
	t.Helper()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "owner-store-" + uuid.NewString(), Platform: "anthropic", Type: "apikey"})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(t, err)
	})
	var id int64
	err := db.QueryRowContext(t.Context(), `INSERT INTO public.scheduled_test_plans(account_id,model_id,cron_expression,enabled,max_results,kind,prompt,next_run_at)
 VALUES($1,'owner-model','* * * * *',true,1,$2,'private plan fixture',date_trunc('minute',clock_timestamp())-interval '10 minutes') RETURNING id`, account.ID, kind).Scan(&id)
	require.NoError(t, err)
	return id
}

func ownerStoreResult(t *testing.T, db *sql.DB, plan int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, db.QueryRowContext(t.Context(), `INSERT INTO public.scheduled_test_results(plan_id,model_id,response_text,started_at,finished_at)
 VALUES($1,'owner-model','private result fixture',clock_timestamp(),clock_timestamp()) RETURNING id`, plan).Scan(&id))
	return id
}

func ownerStoreExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), query, args...)
	require.NoError(t, err)
}

func ownerStoreActivate(t *testing.T, store *scheduledowner.Store, plan int64) scheduledowner.Status {
	t.Helper()
	statuses, err := store.Import(t.Context(), []int64{plan})
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	frozen, err := store.Freeze(t.Context(), plan, statuses[0].Epoch)
	require.NoError(t, err)
	active, err := store.Activate(t.Context(), plan, frozen.Epoch, scheduledowner.Dispatch)
	require.NoError(t, err)
	return active
}

func ownerStoreFinish(t *testing.T, db *sql.DB, run *scheduledowner.Run) {
	t.Helper()
	require.NoError(t, run.Validate(t.Context()))
	id := ownerStoreResult(t, db, run.PlanID)
	ownerStoreExec(t, db, `UPDATE public.scheduled_test_plans SET rotation_cursor=rotation_cursor+1,last_run_at=clock_timestamp(),
 next_run_at=clock_timestamp()+interval '1 hour',updated_at=clock_timestamp(),last_quiz_outcome_at=clock_timestamp() WHERE id=$1`, run.PlanID)
	require.NoError(t, run.Validate(t.Context()), "Core progress is not an identity edit")
	require.NoError(t, run.Finish(t.Context(), id, nil))
	require.NoError(t, run.Close())
}

func TestScheduledOwnerSchemaAndDatabaseBinding(t *testing.T) {
	db, store := ownerStoreDB(t)
	require.NoError(t, store.CheckSchema(t.Context()))
	id, err := store.DatabaseID(t.Context())
	require.NoError(t, err)
	require.NoError(t, scheduledowner.ApplySchema(t.Context(), db))
	again, err := store.DatabaseID(t.Context())
	require.NoError(t, err)
	require.Equal(t, id, again)
	plan := ownerStorePlan(t, db, "probe")
	wrong := store.ForDatabase(uuid.NewString())
	_, err = wrong.Import(t.Context(), []int64{plan})
	require.ErrorIs(t, err, scheduledowner.ErrDatabase)
	require.ErrorIs(t, store.CheckDatabase(t.Context(), ""), scheduledowner.ErrDatabase)
	require.ErrorIs(t, store.CheckDatabase(t.Context(), uuid.NewString()), scheduledowner.ErrDatabase)
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.plans`))
	ownerStoreExec(t, db, `UPDATE scheduled_owner.schema_migrations SET checksum='corrupt'`)
	require.ErrorContains(t, scheduledowner.ApplySchema(t.Context(), db), "checksum")
	require.ErrorContains(t, store.CheckSchema(t.Context()), "checksum")
	_, err = store.Import(t.Context(), []int64{plan})
	require.ErrorContains(t, err, "checksum")
}

func TestScheduledOwnerImportPreservesLegacyAndReviewFencesEdits(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "quiz")
	var question int64
	require.NoError(t, db.QueryRowContext(t.Context(), `INSERT INTO public.intelligence_questions(prompt,answer_rule,expected_answers,tolerance) VALUES('private question','exact','["answer"]',0) RETURNING id`).Scan(&question))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM public.intelligence_questions WHERE id=$1`, question)
	})
	ownerStoreExec(t, db, `INSERT INTO public.scheduled_test_plan_questions(plan_id,question_id) VALUES($1,$2)`, plan, question)
	ownerStoreResult(t, db, plan)
	var before string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_jsonb(p)::text FROM public.scheduled_test_plans p WHERE id=$1`, plan).Scan(&before))
	a, err := store.Import(t.Context(), []int64{plan, plan})
	require.NoError(t, err)
	require.Len(t, a, 1)
	require.Equal(t, int64(1), a[0].ResultsCount)
	var after string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_jsonb(p)::text FROM public.scheduled_test_plans p WHERE id=$1`, plan).Scan(&after))
	require.Equal(t, before, after)
	body, err := json.Marshal(a)
	require.NoError(t, err)
	require.NotContains(t, string(body), "private")
	again, err := store.Import(t.Context(), []int64{plan})
	require.NoError(t, err)
	require.Equal(t, a, again)
	frozen, err := store.Freeze(t.Context(), plan, 1)
	require.NoError(t, err)
	ownerStoreExec(t, db, `UPDATE public.intelligence_questions SET expected_answers='["edited"]',tolerance=0.5 WHERE id=$1`, question)
	_, err = store.Activate(t.Context(), plan, frozen.Epoch, scheduledowner.Dispatch)
	require.ErrorIs(t, err, scheduledowner.ErrChanged)
	_, err = store.Review(t.Context(), plan, frozen.Epoch, "")
	require.Error(t, err)
	reviewed, err := store.Review(t.Context(), plan, frozen.Epoch, "review isolated fixture grading edit")
	require.NoError(t, err)
	require.NotEqual(t, a[0].PlanDigest, reviewed.PlanDigest)
	_, err = store.Activate(t.Context(), plan, frozen.Epoch, scheduledowner.Dispatch)
	require.ErrorIs(t, err, scheduledowner.ErrEpoch)
	active, err := store.Activate(t.Context(), plan, reviewed.Epoch, scheduledowner.Dispatch)
	require.NoError(t, err)
	require.Equal(t, scheduledowner.Dispatch, active.Owner)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.plans WHERE (import_snapshot->'questions'->0->'question'->'expected_answers')='["answer"]'::jsonb`))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.audit WHERE action='review_edit'`))
}

func TestScheduledOwnerWatermarkIncludesLateLowerIDCommit(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "char_art")
	late, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = late.Rollback() }()
	var low int64
	require.NoError(t, late.QueryRowContext(t.Context(), `INSERT INTO public.scheduled_test_results(plan_id,response_text) VALUES($1,'late lower id') RETURNING id`, plan).Scan(&low))
	high := ownerStoreResult(t, db, plan)
	require.Greater(t, high, low)
	first, err := store.Import(t.Context(), []int64{plan})
	require.NoError(t, err)
	require.Equal(t, int64(1), first[0].ResultsCount)
	require.Equal(t, high, first[0].ResultsMaxID)
	require.NoError(t, late.Commit())
	frozen, err := store.Freeze(t.Context(), plan, 1)
	require.NoError(t, err)
	refreshed, err := store.Activate(t.Context(), plan, frozen.Epoch, scheduledowner.Legacy)
	require.NoError(t, err)
	require.Equal(t, int64(2), refreshed.ResultsCount)
	require.Equal(t, high, refreshed.ResultsMaxID)
	require.NotEqual(t, first[0].ResultsDigest, refreshed.ResultsDigest)
}

func TestScheduledOwnerUnmanagedLockAndCrashAcknowledgement(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "probe")
	run, err := store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.NoError(t, err)
	require.False(t, run.Managed)
	_, err = store.Import(t.Context(), []int64{plan})
	require.ErrorIs(t, err, scheduledowner.ErrBusy)
	_, err = store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.ErrorIs(t, err, scheduledowner.ErrBusy)
	statuses, err := store.Status(t.Context(), []int64{plan})
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	require.Equal(t, int64(0), statuses[0].Epoch)
	require.Equal(t, 1, statuses[0].Running)
	_, err = store.AckUnknown(t.Context(), plan, 0, "confirm isolated external call stopped")
	require.ErrorIs(t, err, scheduledowner.ErrBusy)
	require.NoError(t, run.Close())
	_, err = store.Import(t.Context(), []int64{plan})
	require.ErrorIs(t, err, scheduledowner.ErrPending)
	_, err = store.AckUnknown(t.Context(), plan, 0, "confirm isolated external call stopped")
	require.NoError(t, err)
	imported, err := store.Import(t.Context(), []int64{plan})
	require.NoError(t, err)
	require.Equal(t, int64(1), imported[0].Epoch)
	_, err = store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.ErrorIs(t, err, scheduledowner.ErrNotAdmitted)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.runs WHERE state='unknown'`))
}

func TestScheduledOwnerFreezeDrainsCapturedEpoch(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "quiz")
	_, err := store.Import(t.Context(), []int64{plan})
	require.NoError(t, err)
	run, err := store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.NoError(t, err)
	require.True(t, run.Managed)
	frozen, err := store.Freeze(t.Context(), plan, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), frozen.Epoch)
	require.NoError(t, run.Validate(t.Context()))
	_, err = store.Activate(t.Context(), plan, 2, scheduledowner.Dispatch)
	require.ErrorIs(t, err, scheduledowner.ErrBusy)
	ownerStoreFinish(t, db, run)
	active, err := store.Activate(t.Context(), plan, 2, scheduledowner.Dispatch)
	require.NoError(t, err)
	require.Equal(t, int64(3), active.Epoch)
	require.Equal(t, 0, active.Running)
	require.Equal(t, int64(1), active.ResultsCount)
}

func TestScheduledOwnerUnknownNeverReplaysDueAcrossOwners(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "probe")
	active := ownerStoreActivate(t, store, plan)
	count, err := store.Schedule(t.Context(), []scheduledowner.Binding{{PlanID: plan, Epoch: active.Epoch}})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	run, err := store.Acquire(t.Context(), plan, scheduledowner.Dispatch)
	require.NoError(t, err)
	slot := run.Slot
	require.NoError(t, run.Close())
	_, err = store.Acquire(t.Context(), plan, scheduledowner.Dispatch)
	require.ErrorIs(t, err, scheduledowner.ErrPending)
	frozen, err := store.Freeze(t.Context(), plan, active.Epoch)
	require.NoError(t, err)
	_, err = store.Activate(t.Context(), plan, frozen.Epoch, scheduledowner.Legacy)
	require.ErrorIs(t, err, scheduledowner.ErrPending)
	_, err = store.AckUnknown(t.Context(), plan, frozen.Epoch, "")
	require.Error(t, err)
	_, err = store.AckUnknown(t.Context(), plan, frozen.Epoch, "isolated provider drained; accept unknown")
	require.NoError(t, err)
	rolled, err := store.Activate(t.Context(), plan, frozen.Epoch, scheduledowner.Legacy)
	require.NoError(t, err)
	_, err = store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.ErrorIs(t, err, scheduledowner.ErrNotAdmitted)
	frozen, err = store.Freeze(t.Context(), plan, rolled.Epoch)
	require.NoError(t, err)
	active, err = store.Activate(t.Context(), plan, frozen.Epoch, scheduledowner.Dispatch)
	require.NoError(t, err)
	count, err = store.Schedule(t.Context(), []scheduledowner.Binding{{PlanID: plan, Epoch: active.Epoch}})
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.runs WHERE plan_id=$1 AND slot=$2 AND state='unknown'`, plan, slot))
	_, err = store.Schedule(t.Context(), []scheduledowner.Binding{{PlanID: plan, Epoch: 3}})
	require.ErrorIs(t, err, scheduledowner.ErrEpoch)
}

func TestScheduledOwnerConcurrentSchedulersAndExecutors(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "char_art")
	active := ownerStoreActivate(t, store, plan)
	bindings := []scheduledowner.Binding{{PlanID: plan, Epoch: active.Epoch}, {PlanID: plan, Epoch: active.Epoch}}
	var wg sync.WaitGroup
	counts := make(chan int, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); n, err := store.Schedule(t.Context(), bindings); counts <- n; errs <- err }()
	}
	wg.Wait()
	close(counts)
	close(errs)
	total := 0
	for n := range counts {
		total += n
	}
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 1, total)
	run, err := store.Acquire(t.Context(), plan, scheduledowner.Dispatch)
	require.NoError(t, err)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Acquire(t.Context(), plan, scheduledowner.Dispatch)
			require.ErrorIs(t, err, scheduledowner.ErrBusy)
		}()
	}
	wg.Wait()
	ownerStoreFinish(t, db, run)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.runs WHERE plan_id=$1 AND state='completed'`, plan))
}

func TestScheduledOwnerFailureLeavesAuditedUnresolvedSlot(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "probe")
	_, err := store.Import(t.Context(), []int64{plan})
	require.NoError(t, err)
	run, err := store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.NoError(t, err)
	id := ownerStoreResult(t, db, plan)
	require.ErrorIs(t, run.Finish(t.Context(), id, errors.New("private provider fixture error")), scheduledowner.ErrPending)
	require.NoError(t, run.Close())
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.runs WHERE plan_id=$1 AND state='running' AND result_id=$2`, plan, id))
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.audit WHERE facts::text LIKE '%private%' OR reason LIKE '%private%'`))
	statuses, err := store.Status(t.Context(), []int64{plan})
	require.NoError(t, err)
	require.Equal(t, "running", statuses[0].LastRunState)
}

func TestScheduledOwnerQueueAndBatchBounds(t *testing.T) {
	db, store := ownerStoreDB(t)
	bindings := make([]scheduledowner.Binding, 0, 20)
	for i := 0; i < 20; i++ {
		plan := ownerStorePlan(t, db, "probe")
		st := ownerStoreActivate(t, store, plan)
		bindings = append(bindings, scheduledowner.Binding{PlanID: plan, Epoch: st.Epoch})
	}
	var wg sync.WaitGroup
	counts := make(chan int, 4)
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		part := bindings[i*5 : (i+1)*5]
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := store.Schedule(t.Context(), part)
			counts <- n
			errors <- err
		}()
	}
	wg.Wait()
	close(counts)
	close(errors)
	count := 0
	for n := range counts {
		count += n
	}
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, 16, count)
	require.Equal(t, 16, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.runs WHERE state='queued'`))
	_, err := store.Schedule(t.Context(), append(bindings, scheduledowner.Binding{PlanID: bindings[0].PlanID, Epoch: 99}))
	require.ErrorIs(t, err, scheduledowner.ErrEpoch)
	tooMany := make([]scheduledowner.Binding, 1001)
	_, err = store.Schedule(t.Context(), tooMany)
	require.ErrorContains(t, err, "1000")
	_, err = store.Import(t.Context(), make([]int64, 1001))
	require.ErrorContains(t, err, "1000")
	_, err = store.ListQueued(t.Context(), 1001)
	require.Error(t, err)
}

func TestScheduledOwnerSourceLimitsFailClosed(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "char_art")
	ownerStoreExec(t, db, `INSERT INTO public.scheduled_test_results(plan_id,response_text) VALUES($1,repeat('x',8388609))`, plan)
	_, err := store.Import(t.Context(), []int64{plan})
	require.ErrorContains(t, err, "watermark exceeds")
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.plans`))
	ownerStoreExec(t, db, `DELETE FROM public.scheduled_test_results WHERE plan_id=$1`, plan)
	ownerStoreExec(t, db, `UPDATE public.scheduled_test_plans SET prompt=repeat('x',8388609) WHERE id=$1`, plan)
	_, err = store.Import(t.Context(), []int64{plan})
	require.ErrorContains(t, err, "snapshot exceeds")
}

func TestScheduledOwnerLostPhysicalSessionDoesNotTakeOver(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "probe")
	_, err := store.Import(t.Context(), []int64{plan})
	require.NoError(t, err)
	run, err := store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.NoError(t, err)
	var pid int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT DISTINCT pid FROM pg_catalog.pg_locks WHERE locktype='advisory' AND pid<>pg_backend_pid() AND objsubid=1`).Scan(&pid))
	var killed bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT pg_terminate_backend($1)`, pid).Scan(&killed))
	require.True(t, killed)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.Error(t, run.Validate(ctx))
	require.Error(t, run.Close())
	_, err = store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.ErrorIs(t, err, scheduledowner.ErrPending)
	frozen, err := store.Freeze(t.Context(), plan, 1)
	require.NoError(t, err)
	_, err = store.Activate(t.Context(), plan, frozen.Epoch, scheduledowner.Dispatch)
	require.ErrorIs(t, err, scheduledowner.ErrPending)
	_, err = store.AckUnknown(t.Context(), plan, frozen.Epoch, "isolated database session terminated; external call independently drained")
	require.NoError(t, err)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.runs WHERE state='unknown'`))
}

func TestScheduledOwnerFreshPlanQueueAndFinishFences(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "char_art")
	active := ownerStoreActivate(t, store, plan)
	_, err := store.Schedule(t.Context(), []scheduledowner.Binding{{PlanID: plan, Epoch: active.Epoch}})
	require.NoError(t, err)
	ownerStoreExec(t, db, `UPDATE public.scheduled_test_plans SET prompt='edited after queue' WHERE id=$1`, plan)
	_, err = store.Acquire(t.Context(), plan, scheduledowner.Dispatch)
	require.ErrorIs(t, err, scheduledowner.ErrChanged)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.runs WHERE state='queued'`))
	frozen, err := store.Freeze(t.Context(), plan, active.Epoch)
	require.NoError(t, err)
	_, err = store.Activate(t.Context(), plan, frozen.Epoch, scheduledowner.Legacy)
	require.ErrorIs(t, err, scheduledowner.ErrChanged)
	reviewed, err := store.Review(t.Context(), plan, frozen.Epoch, "review queued prompt edit")
	require.NoError(t, err)
	_, err = store.Activate(t.Context(), plan, reviewed.Epoch, scheduledowner.Legacy)
	require.NoError(t, err)
	_, err = store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.ErrorIs(t, err, scheduledowner.ErrNotAdmitted, "cancelled due is also a permanent tombstone")
	// A source progress fixture isolates the store fence; actual admin rescheduling
	// is exercised by the separate Core process topology drill.
	ownerStoreExec(t, db, `UPDATE public.scheduled_test_plans SET next_run_at=next_run_at+interval '1 minute' WHERE id=$1`, plan)
	run, err := store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.NoError(t, err)
	other := ownerStorePlan(t, db, "probe")
	foreign := ownerStoreResult(t, db, other)
	require.ErrorIs(t, run.Finish(t.Context(), foreign, nil), scheduledowner.ErrNotAdmitted)
	require.NoError(t, run.Validate(t.Context()))
	ownerStoreExec(t, db, `UPDATE public.scheduled_test_plans SET policy_version=policy_version+1 WHERE id=$1`, plan)
	require.ErrorIs(t, run.Validate(t.Context()), scheduledowner.ErrChanged)
	require.ErrorIs(t, run.Finish(t.Context(), 0, errors.New("fixture edit during execution")), scheduledowner.ErrPending)
	require.NoError(t, run.Close())
}

func TestScheduledOwnerStatusAllIncludesUnmanagedSummary(t *testing.T) {
	db, store := ownerStoreDB(t)
	unmanaged := ownerStorePlan(t, db, "probe")
	managed := ownerStorePlan(t, db, "probe")
	_, err := store.Import(t.Context(), []int64{managed})
	require.NoError(t, err)
	run, err := store.Acquire(t.Context(), unmanaged, scheduledowner.Legacy)
	require.NoError(t, err)
	statuses, err := store.Status(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	require.Equal(t, unmanaged, statuses[0].PlanID)
	require.Zero(t, statuses[0].Epoch)
	require.Equal(t, 1, statuses[0].Running)
	require.Equal(t, "running", statuses[0].LastRunState)
	require.Equal(t, managed, statuses[1].PlanID)
	require.NoError(t, run.Close())
}

func TestScheduledOwnerFreshDueMustMatchBeforeFirstExecution(t *testing.T) {
	db, store := ownerStoreDB(t)
	plan := ownerStorePlan(t, db, "char_art")
	run, err := store.Acquire(t.Context(), plan, scheduledowner.Legacy)
	require.NoError(t, err)
	ownerStoreExec(t, db, `UPDATE public.scheduled_test_plans SET next_run_at=next_run_at+interval '1 minute' WHERE id=$1`, plan)
	require.ErrorIs(t, run.Validate(t.Context()), scheduledowner.ErrChanged)
	require.NoError(t, run.Close())
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM scheduled_owner.runs WHERE state='running'`))
}
