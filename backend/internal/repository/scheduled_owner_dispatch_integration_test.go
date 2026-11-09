//go:build integration

package repository

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custom/scheduledowner"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func scheduledDispatchQueueDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	role := "owner_queue_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := "synthetic-" + uuid.NewString()
	quoted := pq.QuoteIdentifier(role)
	dispatchExec(t, integrationDB, `CREATE ROLE `+quoted+` LOGIN PASSWORD `+pq.QuoteLiteral(password))
	dispatchExec(t, integrationDB, `GRANT USAGE ON SCHEMA public,scheduled_owner TO `+quoted+`;
	 GRANT SELECT ON scheduled_owner.database_identity,scheduled_owner.schema_migrations,scheduled_owner.plans,scheduled_owner.runs,
	 public.scheduled_test_plans,public.scheduled_test_plan_questions,public.intelligence_questions TO `+quoted+`;
	 GRANT INSERT(plan_id,slot,epoch,owner,state,plan_digest) ON scheduled_owner.queue TO `+quoted)
	dsn, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	dsn.User = url.UserPassword(role, password)
	db, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(ctx, `DROP OWNED BY `+quoted+`; DROP ROLE `+quoted)
		require.NoError(t, err)
	})
	return db, role
}

func TestScheduledOwnerDispatchQueuePrivilegesAndFencing(t *testing.T) {
	dispatchExec(t, integrationDB, `DROP SCHEMA IF EXISTS scheduled_owner CASCADE`)
	require.NoError(t, scheduledowner.ApplySchema(t.Context(), integrationDB))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS scheduled_owner CASCADE`)
		require.NoError(t, err)
	})
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "owner-queue-synthetic"})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(t, err)
	})
	next := time.Now().Add(-time.Minute)
	plan, err := NewScheduledTestPlanRepository(integrationDB, testEntClient(t), nil).Create(t.Context(), &service.ScheduledTestPlan{
		AccountID: account.ID, ModelID: "model", Kind: "char_art", Prompt: "draw a square", CronExpression: "* * * * *", Enabled: true, MaxResults: 50, NextRunAt: &next,
	})
	require.NoError(t, err)
	owner := scheduledowner.NewStore(integrationDB)
	databaseID, err := owner.DatabaseID(t.Context())
	require.NoError(t, err)
	owner = owner.ForDatabase(databaseID)
	imported, err := owner.Import(t.Context(), []int64{plan.ID})
	require.NoError(t, err)
	require.Len(t, imported, 1)
	frozen, err := owner.Freeze(t.Context(), plan.ID, imported[0].Epoch)
	require.NoError(t, err)
	active, err := owner.Activate(t.Context(), plan.ID, frozen.Epoch, scheduledowner.Dispatch)
	require.NoError(t, err)
	scheduled := dispatch.ScheduledConfig{DatabaseID: databaseID, Bindings: []dispatch.ScheduledBinding{{PlanID: plan.ID, Epoch: active.Epoch}}}
	_, err = dispatch.NewScheduledSource(t.Context(), integrationDB, scheduled)
	require.Error(t, err, "database owner is not a queue-only identity")
	db, role := scheduledDispatchQueueDB(t)
	source, err := dispatch.NewScheduledSource(t.Context(), db, scheduled)
	require.NoError(t, err)
	for _, query := range []string{
		`SELECT credentials FROM public.accounts`,
		`SELECT response_text FROM public.scheduled_test_results`,
		`UPDATE scheduled_owner.plans SET epoch=epoch+1 WHERE false`,
		`UPDATE scheduled_owner.runs SET state='completed' WHERE false`,
		`INSERT INTO scheduled_owner.runs(plan_id,slot,epoch,owner,state,plan_digest) VALUES(1,now(),1,'dispatch','completed','fake')`,
		`INSERT INTO scheduled_owner.queue(plan_id,slot,epoch,owner,state,plan_digest) VALUES(1,now(),1,'dispatch','completed','fake')`,
		`INSERT INTO scheduled_owner.queue(plan_id,slot,epoch,owner,state,plan_digest,result_id) VALUES(1,now(),1,'dispatch','queued','fake',1)`,
		`CREATE TABLE public.owner_queue_forbidden(id int)`,
	} {
		_, err := db.ExecContext(t.Context(), query)
		require.Error(t, err, query)
	}
	controlDB := dispatchDB(t)
	cfg := dispatch.Config{Version: 1, CoreInstance: "owner-queue-core", ConfigRevision: 1, SnapshotFile: filepath.Join(t.TempDir(), "snapshot.json"), SnapshotTTLSeconds: 3, PollIntervalMilliseconds: 1000, Scheduled: &scheduled}
	store := dispatch.NewStore(controlDB)
	result, err := dispatch.RunRound(t.Context(), cfg, store, nil, &dispatch.RuntimeSources{Scheduled: source})
	require.NoError(t, err)
	require.Equal(t, 1, result.ScheduledPlans)
	result, err = dispatch.RunRound(t.Context(), cfg, store, nil, &dispatch.RuntimeSources{Scheduled: source})
	require.NoError(t, err)
	require.Zero(t, result.ScheduledPlans, "queue replay cannot create a second logical slot")
	before, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	for _, grant := range []struct{ add, remove string }{
		{`GRANT SELECT(credentials) ON public.accounts TO `, `REVOKE SELECT(credentials) ON public.accounts FROM `},
		{`GRANT INSERT ON scheduled_owner.runs TO `, `REVOKE INSERT ON scheduled_owner.runs FROM `},
		{`GRANT UPDATE(epoch) ON scheduled_owner.plans TO `, `REVOKE UPDATE(epoch) ON scheduled_owner.plans FROM `},
		{`GRANT SELECT ON public.scheduled_test_results TO `, `REVOKE SELECT ON public.scheduled_test_results FROM `},
		{`GRANT CREATE ON SCHEMA public TO `, `REVOKE CREATE ON SCHEMA public FROM `},
		{`GRANT pg_read_all_data TO `, `REVOKE pg_read_all_data FROM `},
	} {
		dispatchExec(t, integrationDB, grant.add+pq.QuoteIdentifier(role))
		_, err = dispatch.RunRound(t.Context(), cfg, store, nil, &dispatch.RuntimeSources{Scheduled: source})
		require.Error(t, err, "source permission changes must fail before renewing admission")
		after, err := os.ReadFile(cfg.SnapshotFile)
		require.NoError(t, err)
		require.Equal(t, before, after)
		dispatchExec(t, integrationDB, grant.remove+pq.QuoteIdentifier(role))
	}
	wrongDB := scheduled
	wrongDB.DatabaseID = uuid.NewString()
	_, err = source.Schedule(t.Context(), wrongDB)
	require.Error(t, err)
	changed := scheduled
	changed.Bindings = []dispatch.ScheduledBinding{{PlanID: plan.ID, Epoch: active.Epoch + 1}}
	cfg.Scheduled = &changed
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), cfg), dispatch.ErrConfigRevision, "same config revision cannot change owner epochs")
	cfg.Scheduled = &scheduled
	frozen, err = owner.Freeze(t.Context(), plan.ID, active.Epoch)
	require.NoError(t, err)
	_, err = owner.Activate(t.Context(), plan.ID, frozen.Epoch, scheduledowner.Legacy)
	require.NoError(t, err)
	_, err = dispatch.RunRound(t.Context(), cfg, store, nil, &dispatch.RuntimeSources{Scheduled: source})
	require.Error(t, err, "an old dispatchd cannot resume after rollback")
	after, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
