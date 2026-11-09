//go:build integration

package repository

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func qualitySourceReadOnlyDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	role := "quality_src_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := "synthetic-" + uuid.NewString()
	_, err := integrationDB.ExecContext(t.Context(), `CREATE ROLE `+pq.QuoteIdentifier(role)+` LOGIN PASSWORD `+pq.QuoteLiteral(password))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(t.Context(), `GRANT USAGE ON SCHEMA public TO `+pq.QuoteIdentifier(role)+`;
	 GRANT SELECT(id,account_id,model_id,kind,prompt,enabled,created_at,updated_at,policy_version) ON public.scheduled_test_plans TO `+pq.QuoteIdentifier(role)+`;
	 GRANT SELECT(id,plan_id,model_id,kind,question_id,prompt,response_text,expected_answer,verdict,status,started_at,finished_at)
	 ON public.scheduled_test_results TO `+pq.QuoteIdentifier(role))
	require.NoError(t, err)
	dsn, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	dsn.User = url.UserPassword(role, password)
	reader, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, reader.Close())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(ctx, `DROP OWNED BY `+pq.QuoteIdentifier(role)+`; DROP ROLE `+pq.QuoteIdentifier(role))
		require.NoError(t, err)
	})
	return reader, role
}

func qualitySourcePlan(t *testing.T, kind string, account int64, cutoff time.Time) (*service.ScheduledTestPlan, dispatch.Target, dispatch.QualityBinding) {
	t.Helper()
	if account == 0 {
		created := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "quality-source"})
		account = created.ID
		t.Cleanup(func() {
			_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, account)
			require.NoError(t, err)
		})
	}
	plan, err := NewScheduledTestPlanRepository(integrationDB, testEntClient(t), nil).Create(t.Context(), &service.ScheduledTestPlan{
		AccountID: account, ModelID: "model", Kind: kind, Prompt: "draw a square", Enabled: true, MaxResults: 50, CronExpression: "*/30 * * * *"})
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(t.Context(), `UPDATE scheduled_test_plans SET created_at=$2,updated_at=$2 WHERE id=$1`, plan.ID, cutoff.Add(-time.Minute))
	require.NoError(t, err)
	target := dispatchTarget("quality", account)
	return plan, target, dispatch.QualityBinding{PlanID: plan.ID, TargetRef: target.Ref, Kind: kind}
}

func qualitySourceResult(t *testing.T, plan *service.ScheduledTestPlan, started time.Time) *service.ScheduledTestResult {
	t.Helper()
	result := &service.ScheduledTestResult{PlanID: plan.ID, ModelID: plan.ModelID, Kind: plan.Kind,
		Prompt: plan.Prompt, ResponseText: "square", Status: "success", StartedAt: started, FinishedAt: started.Add(time.Second)}
	if plan.Kind == "quiz" {
		result.Prompt, result.ExpectedAnswer, result.Verdict = "1+1?", "2", "fail"
	}
	created, err := NewScheduledTestResultRepository(integrationDB).Create(t.Context(), result)
	require.NoError(t, err)
	return created
}

func TestDispatchQualitySourceRestrictedColumns(t *testing.T) {
	cutoff := dispatchNow(t, integrationDB).Add(-time.Minute)
	plan, target, binding := qualitySourcePlan(t, "char_art", 0, cutoff)
	qualitySourceResult(t, plan, cutoff.Add(10*time.Second))
	_, err := dispatch.NewQualitySource(t.Context(), integrationDB)
	require.Error(t, err, "owner and superuser are not restricted readers")
	db, role := qualitySourceReadOnlyDB(t)
	source, err := dispatch.NewQualitySource(t.Context(), db)
	require.NoError(t, err)
	results, err := source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, target.AccountID, results[0].AccountID)
	require.Equal(t, target.Ref, results[0].TargetRef)
	for _, query := range []string{`SELECT credentials FROM accounts`, `SELECT error_message FROM scheduled_test_results`,
		`SELECT * FROM scheduled_test_plans`, `SELECT * FROM intelligence_questions`, `UPDATE scheduled_test_results SET status='success'`,
		`CREATE TABLE public.quality_forbidden(id int)`} {
		rows, err := db.QueryContext(t.Context(), query)
		if rows != nil {
			require.NoError(t, rows.Close())
		}
		require.Error(t, err, query)
	}
	for _, grant := range []struct{ add, remove string }{
		{`GRANT SELECT(credentials) ON accounts TO `, `REVOKE SELECT(credentials) ON accounts FROM `},
		{`GRANT UPDATE(status) ON scheduled_test_results TO `, `REVOKE UPDATE(status) ON scheduled_test_results FROM `},
		{`GRANT SELECT(error_message) ON scheduled_test_results TO `, `REVOKE SELECT(error_message) ON scheduled_test_results FROM `},
		{`GRANT CREATE ON SCHEMA public TO `, `REVOKE CREATE ON SCHEMA public FROM `},
		{`GRANT pg_read_all_data TO `, `REVOKE pg_read_all_data FROM `},
		{`GRANT pg_write_all_data TO `, `REVOKE pg_write_all_data FROM `},
	} {
		_, err := integrationDB.ExecContext(t.Context(), grant.add+pq.QuoteIdentifier(role))
		require.NoError(t, err)
		_, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 10)
		require.Error(t, err, "permissions must be checked again after construction")
		_, err = integrationDB.ExecContext(t.Context(), grant.remove+pq.QuoteIdentifier(role))
		require.NoError(t, err)
	}
	owned := pq.QuoteIdentifier("quality_owned_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	_, err = integrationDB.ExecContext(t.Context(), `CREATE TABLE public.`+owned+`(id int); ALTER TABLE public.`+owned+` OWNER TO `+pq.QuoteIdentifier(role))
	require.NoError(t, err)
	_, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 10)
	require.Error(t, err, "an unrelated application table owner is not a restricted source")
	_, err = integrationDB.ExecContext(t.Context(), `DROP TABLE public.`+owned)
	require.NoError(t, err)
}

func TestDispatchQualitySourceActivationAndPlanChanges(t *testing.T) {
	cutoff := dispatchNow(t, integrationDB).Add(-time.Minute)
	plan, target, binding := qualitySourcePlan(t, "char_art", 0, cutoff)
	db, _ := qualitySourceReadOnlyDB(t)
	source, err := dispatch.NewQualitySource(t.Context(), db)
	require.NoError(t, err)
	read := func() ([]dispatch.LegacyQualityResult, error) {
		return source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 10)
	}
	qualitySourceResult(t, plan, cutoff.Add(-time.Second))
	results, err := read()
	require.NoError(t, err)
	require.Empty(t, results, "a result started before activation cannot inherit a current identity")
	qualitySourceResult(t, plan, cutoff.Add(10*time.Second))
	require.NoError(t, NewScheduledTestPlanRepository(integrationDB, testEntClient(t), nil).UpdateAfterRun(t.Context(), plan.ID, time.Now(), time.Now().Add(time.Hour)))
	results, err = read()
	require.NoError(t, err, "ordinary runner bookkeeping may advance updated_at with a stable policy version")
	require.Len(t, results, 1)
	restarted, err := dispatch.NewQualitySource(t.Context(), db)
	require.NoError(t, err)
	_, err = restarted.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 10)
	require.ErrorContains(t, err, "fresh activation", "legacy tables cannot reconstruct the old activation after a restart")
	rotated := target
	rotated.CredentialRevision++
	_, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{rotated}, cutoff, 10)
	require.Error(t, err, "a new credential must not inherit old results")
	otherPlan, _, otherBinding := qualitySourcePlan(t, "char_art", target.AccountID, cutoff)
	_, err = source.Read(t.Context(), []dispatch.QualityBinding{otherBinding}, []dispatch.Target{target}, cutoff, 10)
	require.Error(t, err, "replacing a plan without changing the cutoff is not a new activation")
	_, err = integrationDB.ExecContext(t.Context(), `DELETE FROM scheduled_test_plans WHERE id=$1`, otherPlan.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(t.Context(), `UPDATE scheduled_test_plans SET policy_version=policy_version+1,updated_at=clock_timestamp() WHERE id=$1`, plan.ID)
	require.NoError(t, err)
	_, err = read()
	require.ErrorContains(t, err, "changed after activation")
	fresh := dispatchNow(t, integrationDB)
	results, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{rotated}, fresh, 10)
	require.NoError(t, err)
	require.Empty(t, results, "rebinding cuts off every older run")
	_, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{rotated}, cutoff, 10)
	require.Error(t, err, "activation cannot move backwards")
	_, err = integrationDB.ExecContext(t.Context(), `DELETE FROM scheduled_test_plans WHERE id=$1`, plan.ID)
	require.NoError(t, err)
	_, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{rotated}, fresh, 10)
	require.ErrorContains(t, err, "missing")
}

func TestDispatchQualitySourceKindsAndIdentity(t *testing.T) {
	for _, kind := range []string{"char_art", "quiz"} {
		t.Run(kind, func(t *testing.T) {
			cutoff := dispatchNow(t, integrationDB).Add(-time.Minute)
			plan, target, binding := qualitySourcePlan(t, kind, 0, cutoff)
			otherPlan, _, otherBinding := qualitySourcePlan(t, kind, target.AccountID, cutoff)
			db, _ := qualitySourceReadOnlyDB(t)
			source, err := dispatch.NewQualitySource(t.Context(), db)
			require.NoError(t, err)
			result := qualitySourceResult(t, plan, cutoff.Add(10*time.Second))
			qualitySourceResult(t, otherPlan, cutoff.Add(11*time.Second))
			read := func() ([]dispatch.LegacyQualityResult, error) {
				return source.Read(t.Context(), []dispatch.QualityBinding{binding, otherBinding}, []dispatch.Target{target}, cutoff, 10)
			}
			results, err := read()
			require.NoError(t, err)
			require.Len(t, results, 2, "multiple plans on the same target and kind are allowed")
			require.Equal(t, kind, results[0].Kind)
			for _, status := range []string{"failed", "unknown"} {
				_, err = integrationDB.ExecContext(t.Context(), `UPDATE scheduled_test_results SET status=$2,verdict='' WHERE id=$1`, result.ID, status)
				require.NoError(t, err)
				results, err = read()
				require.NoError(t, err)
				require.Equal(t, status, results[0].Status)
				require.Empty(t, results[0].Verdict, "a request failure must not become a quality failure")
			}
			for _, query := range []string{
				`UPDATE scheduled_test_results SET model_id='other' WHERE id=$1`,
				`UPDATE scheduled_test_results SET kind='probe' WHERE id=$1`,
			} {
				_, err = integrationDB.ExecContext(t.Context(), query, result.ID)
				require.NoError(t, err)
				_, err = read()
				require.ErrorContains(t, err, "identity mismatch")
				_, err = integrationDB.ExecContext(t.Context(), `UPDATE scheduled_test_results SET model_id='model',kind=$2 WHERE id=$1`, result.ID, kind)
				require.NoError(t, err)
			}
			_, err = integrationDB.ExecContext(t.Context(), `UPDATE scheduled_test_plans SET model_id='other' WHERE id=$1`, plan.ID)
			require.NoError(t, err)
			_, err = read()
			require.ErrorContains(t, err, "identity mismatch", "model aliases are never guessed")
		})
	}
}

func TestDispatchQualitySourceLateCommitAndSevenDayWindow(t *testing.T) {
	cutoff := dispatchNow(t, integrationDB).Add(-8 * 24 * time.Hour)
	plan, target, binding := qualitySourcePlan(t, "char_art", 0, cutoff)
	db, _ := qualitySourceReadOnlyDB(t)
	source, err := dispatch.NewQualitySource(t.Context(), db)
	require.NoError(t, err)
	qualitySourceResult(t, plan, cutoff.Add(time.Hour))
	now := dispatchNow(t, integrationDB)
	tx, err := integrationDB.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	var lateID int64
	require.NoError(t, tx.QueryRowContext(t.Context(), `INSERT INTO scheduled_test_results(plan_id,kind,model_id,prompt,status,response_text,started_at,finished_at)
	 VALUES($1,'char_art','model',$2,'success','late',$3,$4) RETURNING id`, plan.ID, plan.Prompt, now.Add(-30*time.Second), now.Add(-20*time.Second)).Scan(&lateID))
	newer := qualitySourceResult(t, plan, now.Add(-10*time.Second))
	results, err := source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, newer.ID, results[0].ResultID)
	require.NoError(t, tx.Commit())
	results, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 10)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, lateID, results[0].ResultID, "a transaction committing after a larger ID was read must not be skipped")
}

func TestDispatchQualitySourceReadBounds(t *testing.T) {
	cutoff := dispatchNow(t, integrationDB).Add(-time.Minute)
	plan, target, binding := qualitySourcePlan(t, "char_art", 0, cutoff)
	db, _ := qualitySourceReadOnlyDB(t)
	source, err := dispatch.NewQualitySource(t.Context(), db)
	require.NoError(t, err)
	result := qualitySourceResult(t, plan, cutoff.Add(10*time.Second))
	qualitySourceResult(t, plan, cutoff.Add(11*time.Second))
	results, err := source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 1)
	require.ErrorContains(t, err, "exceeds limit")
	require.Nil(t, results, "limit+1 never returns a partial batch")
	for _, column := range []string{"prompt", "response_text", "expected_answer"} {
		_, err := integrationDB.ExecContext(t.Context(), `UPDATE scheduled_test_results SET `+pq.QuoteIdentifier(column)+`=repeat('x',4*1024*1024) WHERE id=$1`, result.ID)
		require.NoError(t, err)
		results, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 10)
		require.ErrorContains(t, err, "oversized")
		require.Nil(t, results)
		_, err = integrationDB.ExecContext(t.Context(), `UPDATE scheduled_test_results SET prompt=$2,response_text='square',expected_answer='' WHERE id=$1`, result.ID, plan.Prompt)
		require.NoError(t, err)
	}
	_, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, time.Time{}, 10)
	require.Error(t, err)
	_, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 10001)
	require.Error(t, err)
	_, err = integrationDB.ExecContext(t.Context(), `INSERT INTO scheduled_test_results(plan_id,kind,model_id,prompt,status,response_text,started_at,finished_at)
	 SELECT $1,'char_art','model',$2,'success',repeat('x',65536),$3::timestamptz,$4::timestamptz FROM generate_series(1,129)`,
		plan.ID, plan.Prompt, cutoff.Add(12*time.Second), cutoff.Add(13*time.Second))
	require.NoError(t, err)
	results, err = source.Read(t.Context(), []dispatch.QualityBinding{binding}, []dispatch.Target{target}, cutoff, 1000)
	require.ErrorContains(t, err, "exceeds limit", "individually bounded texts still have a batch byte budget")
	require.Nil(t, results)
}
