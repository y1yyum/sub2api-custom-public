//go:build integration

package repository

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func probeBudgetLegacySchema(t *testing.T, db *sql.DB, through003 bool) {
	t.Helper()
	dispatchExec(t, db, `DROP SCHEMA dispatch CASCADE; CREATE SCHEMA dispatch;
	 CREATE TABLE dispatch.schema_migrations(version TEXT PRIMARY KEY,checksum TEXT NOT NULL,
	 applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`)
	migrations := []struct{ version, name string }{{"001", "001_dispatch.sql"}}
	if through003 {
		migrations = append(migrations, struct{ version, name string }{"003", "003_failure_holds.sql"})
	}
	for _, migration := range migrations {
		body, err := os.ReadFile("../custom/dispatch/migrations/" + migration.name)
		require.NoError(t, err)
		dispatchExec(t, db, string(body))
		dispatchExec(t, db, `INSERT INTO dispatch.schema_migrations(version,checksum) VALUES ($1,$2)`,
			migration.version, fmt.Sprintf("%x", sha256.Sum256(body)))
	}
}

func TestDispatchProbeBudgetSchemaUpgradeRetainsLegacyHistory(t *testing.T) {
	for _, through003 := range []bool{false, true} {
		t.Run(fmt.Sprintf("through003-%v", through003), func(t *testing.T) {
			db := dispatchDB(t)
			probeBudgetLegacySchema(t, db, through003)
			store := dispatch.NewStore(db)
			a := dispatchTarget("legacy", 1)
			legacy := failureConfig(t, []dispatch.Target{a}, false)
			if through003 {
				require.NoError(t, store.SyncRuntimeConfig(t.Context(), legacy), "nil budgets work before the explicit 004 upgrade")
				lease, err := store.ClaimProbe(t.Context(), legacy.CoreInstance, a.Ref, "legacy-worker", legacy.ProbeLimits())
				require.NoError(t, err)
				require.NotNil(t, lease)
				require.Nil(t, lease.Budget)
				probeScheduleFinish(t, db, store, lease, dispatch.Unknown)
			} else {
				// 当前运行接口依赖 003；001 升级用当时的表契约准备历史行。
				now := dispatchNow(t, db)
				metadata, err := json.Marshal(a)
				require.NoError(t, err)
				health, err := json.Marshal(dispatch.InitialHealth(a, now))
				require.NoError(t, err)
				policy, err := json.Marshal(legacy.ProbeLimits())
				require.NoError(t, err)
				dispatchExec(t, db, `INSERT INTO dispatch.cores(core_instance,config_revision,probe_limits) VALUES ('core',1,$1)`, string(policy))
				dispatchExec(t, db, `INSERT INTO dispatch.targets VALUES ('core','legacy',$1,$2,1)`, string(metadata), string(health))
				dispatchExec(t, db, `INSERT INTO dispatch.observations VALUES ('core','legacy-event','{}',$1)`, now)
				token := uuid.NewString()
				dispatchExec(t, db, `INSERT INTO dispatch.probes
				 (token,core_instance,target_ref,target,generation,owner,account_id,model,region,provider_ref,slot,attempt_id,reservation_id,reserved_at,lease_until,finished_at)
				 VALUES ($1,'core','legacy',$2,1,'legacy-worker',$3,$4,$5,$6,$7,$8,$9,$10,$11,$10)`,
					token, string(metadata), a.AccountID, a.Model, a.Region, a.ProviderRef, now.Unix()/30,
					"dispatch-attempt:"+token, "dispatch-reservation:"+token, now, now.Add(time.Minute))
			}
			before := probeBudgetOperationalState(t, db)
			publicOID := dispatchCount(t, db, `SELECT 'public.accounts'::regclass::oid::bigint`)
			require.NoError(t, dispatch.ApplySchema(t.Context(), db))
			require.NoError(t, dispatch.ApplySchema(t.Context(), db))
			require.Equal(t, before, probeBudgetOperationalState(t, db))
			require.Equal(t, publicOID, dispatchCount(t, db, `SELECT 'public.accounts'::regclass::oid::bigint`))
			require.Equal(t, 3, dispatchCount(t, db, `SELECT count(*) FROM dispatch.schema_migrations WHERE version IN ('001','003','004')`))
			report := probeBudgetReport(t, store, legacy)
			require.False(t, report.Enabled)
			require.False(t, report.Active)
			require.Nil(t, report.Activation)
			require.Zero(t, report.EntryCount, "migration must not invent prices for pre-budget probe history")
			require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money_audit`))
			priced := probeBudgetConfig(t, "core", 500, 500, 500, a, dispatchTarget("new", 2))
			priced.ConfigRevision++
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), priced))
			probeBudgetClaim(t, store, priced, "new")
			report = probeBudgetReport(t, store, priced)
			require.EqualValues(t, 1, report.EntryCount)
			require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
			require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
		})
	}
}

func TestDispatchProbeBudgetSchemaChecksumAndPrefixFailClosed(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeBudgetConfig(t, "core", 500, 500, 500, dispatchTarget("a", 1))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	probeBudgetClaim(t, store, cfg, "a")
	before := probeBudgetOperationalState(t, db)
	var checksum string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT checksum FROM dispatch.schema_migrations WHERE version='004'`).Scan(&checksum))
	body, err := os.ReadFile("../custom/dispatch/migrations/004_probe_budget.sql")
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(body)), checksum)
	dispatchExec(t, db, `UPDATE dispatch.schema_migrations SET checksum='synthetic-tamper' WHERE version='004'`)
	require.ErrorContains(t, dispatch.ApplySchema(t.Context(), db), "checksum")
	_, err = store.ProbeBudgetReport(t.Context(), cfg.CoreInstance, cfg.ConfigRevision)
	require.ErrorIs(t, err, dispatch.ErrProbeBudgetSchema)
	dispatchExec(t, db, `UPDATE dispatch.schema_migrations SET checksum=$1 WHERE version='004'`, checksum)
	for _, version := range []string{"001", "003"} {
		var original string
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT checksum FROM dispatch.schema_migrations WHERE version=$1`, version).Scan(&original))
		dispatchExec(t, db, `DELETE FROM dispatch.schema_migrations WHERE version=$1`, version)
		require.ErrorContains(t, dispatch.ApplySchema(t.Context(), db), "order")
		dispatchExec(t, db, `INSERT INTO dispatch.schema_migrations(version,checksum) VALUES ($1,$2)`, version, original)
	}
	require.NoError(t, dispatch.ApplySchema(t.Context(), db))
	require.Equal(t, before, probeBudgetOperationalState(t, db))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money`))
}

func TestDispatchProbeBudgetSchemaMissingEnableFailsClosed(t *testing.T) {
	db := dispatchDB(t)
	probeBudgetLegacySchema(t, db, true)
	store := dispatch.NewStore(db)
	legacy := failureConfig(t, []dispatch.Target{dispatchTarget("legacy", 1)}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), legacy))
	report, err := store.ProbeBudgetReport(t.Context(), legacy.CoreInstance, legacy.ConfigRevision)
	require.NoError(t, err)
	require.False(t, report.SchemaReady)
	require.False(t, report.Enabled)
	before := probeBudgetOperationalState(t, db)
	cfg := probeBudgetConfig(t, "core", 500, 500, 500, legacy.Targets...)
	cfg.ConfigRevision++
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), cfg), dispatch.ErrProbeBudgetSchema)
	require.Equal(t, before, probeBudgetOperationalState(t, db))
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.schema_migrations WHERE version='004'`))
	var exists bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass('dispatch.probe_money') IS NOT NULL`).Scan(&exists))
	require.False(t, exists, "runtime must not implicitly migrate the database")
	require.NoError(t, dispatch.ApplySchema(t.Context(), db))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	dispatchExec(t, db, `DROP TABLE dispatch.probe_money_audit`)
	_, err = store.ClaimProbe(t.Context(), cfg.CoreInstance, "legacy", "partial-schema", cfg.ProbeLimits())
	require.ErrorIs(t, err, dispatch.ErrProbeBudgetSchema)
	_, err = store.ProbeBudgetReport(t.Context(), cfg.CoreInstance, cfg.ConfigRevision)
	require.ErrorIs(t, err, dispatch.ErrProbeBudgetSchema)
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
}

func probeBudgetLegacyInsert(t *testing.T, tx *sql.Tx, cfg dispatch.Config, now time.Time) error {
	t.Helper()
	target := cfg.Targets[0]
	body, err := json.Marshal(target)
	require.NoError(t, err)
	token := uuid.NewString()
	_, err = tx.ExecContext(t.Context(), `INSERT INTO dispatch.probes
	 (token,core_instance,target_ref,target,generation,owner,account_id,model,region,provider_ref,slot,attempt_id,reservation_id,reserved_at,lease_until)
	 VALUES ($1,$2,$3,$4,1,'old-binary',$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		token, cfg.CoreInstance, target.Ref, string(body), target.AccountID, target.Model, target.Region,
		target.ProviderRef, now.Unix()/30, "dispatch-attempt:"+token, "dispatch-reservation:"+token, now, now.Add(time.Minute))
	return err
}

func TestDispatchProbeBudgetSchemaRejectsLegacyUnpricedInsertAtCommit(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := probeBudgetConfig(t, "core", 500, 500, 500, dispatchTarget("a", 1))
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	before := probeBudgetOperationalState(t, db)
	tx, err := probeBudgetConnection(t).BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	err = probeBudgetLegacyInsert(t, tx, cfg, dispatchNow(t, db))
	require.NoError(t, err, "the legacy INSERT is checked after its transaction has had a chance to reserve money")
	require.ErrorContains(t, tx.Commit(), "frozen money reservation")
	require.Equal(t, before, probeBudgetOperationalState(t, db))
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money`))
	probeBudgetClaim(t, store, cfg, "a")
}

func TestDispatchProbeBudgetSchemaRejectsPreActivationSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name            string
		beforeMigration bool
		isolation       sql.IsolationLevel
	}{
		{"repeatable-read", false, sql.LevelRepeatableRead},
		{"serializable", false, sql.LevelSerializable},
		{"repeatable-read-before-migration", true, sql.LevelRepeatableRead},
		{"serializable-before-migration", true, sql.LevelSerializable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dispatchDB(t)
			if tc.beforeMigration {
				probeBudgetLegacySchema(t, db, true)
			}
			store := dispatch.NewStore(db)
			legacy := failureConfig(t, []dispatch.Target{dispatchTarget("legacy", 1)}, false)
			legacy.CoreInstance = "legacy-core"
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), legacy))
			tx, err := probeBudgetConnection(t).BeginTx(t.Context(), &sql.TxOptions{Isolation: tc.isolation})
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			var revision int64
			require.NoError(t, tx.QueryRowContext(t.Context(), `SELECT config_revision FROM dispatch.cores WHERE core_instance=$1`, legacy.CoreInstance).Scan(&revision))
			require.Equal(t, legacy.ConfigRevision, revision, "establish a real PostgreSQL snapshot before activation")
			if tc.beforeMigration {
				require.NoError(t, dispatch.ApplySchema(t.Context(), db))
			}
			cfg := probeBudgetConfig(t, "budget-core", 500, 500, 500, dispatchTarget("priced", 2))
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			before := probeBudgetOperationalState(t, db)
			err = probeBudgetLegacyInsert(t, tx, legacy, dispatchNow(t, db))
			require.Error(t, err, "a pre-activation snapshot cannot hide the latch; non-READ-COMMITTED probe inserts fail immediately")
			var isolationError *pq.Error
			require.ErrorAs(t, err, &isolationError)
			require.Equal(t, pq.ErrorCode("25000"), isolationError.Code)
			require.NoError(t, tx.Rollback())
			require.Equal(t, before, probeBudgetOperationalState(t, db))
			require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
			require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money`))
			require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_budget_latch`))
		})
	}
}

func TestDispatchProbeBudgetSchemaIsolationFenceBeforeActivation(t *testing.T) {
	for _, isolation := range []sql.IsolationLevel{sql.LevelReadCommitted, sql.LevelReadUncommitted} {
		t.Run(isolation.String(), func(t *testing.T) {
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			cfg := failureConfig(t, []dispatch.Target{dispatchTarget("legacy", 1)}, false)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			require.False(t, probeBudgetReport(t, store, cfg).Enabled)
			tx, err := probeBudgetConnection(t).BeginTx(t.Context(), &sql.TxOptions{Isolation: isolation})
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			err = probeBudgetLegacyInsert(t, tx, cfg, dispatchNow(t, db))
			if isolation == sql.LevelReadCommitted {
				require.NoError(t, err, "READ COMMITTED legacy inserts remain compatible before activation")
				require.NoError(t, tx.Commit())
				require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
			} else {
				require.Error(t, err, "the isolation fence is unconditional even when the latch has never been enabled")
				var isolationError *pq.Error
				require.ErrorAs(t, err, &isolationError)
				require.Equal(t, pq.ErrorCode("25000"), isolationError.Code)
				require.NoError(t, tx.Rollback())
				require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
			}
			require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_money`))
			require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probe_budget_latch`))
		})
	}
}
