//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
)

func dispatchDB(t *testing.T) *sql.DB {
	t.Helper()
	_, err := integrationDB.ExecContext(t.Context(), `DROP SCHEMA IF EXISTS dispatch CASCADE`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS dispatch CASCADE`)
		require.NoError(t, err)
	})
	require.NoError(t, dispatch.ApplySchema(t.Context(), integrationDB))
	db, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	db.SetMaxOpenConns(20)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func dispatchTarget(ref string, account int64) dispatch.Target {
	return dispatch.Target{Ref: ref, AccountID: account, CredentialRef: fmt.Sprintf("credential-%d", account), CredentialRevision: 1,
		ProviderRef: "provider", PoolRef: "pool", PolicyRevision: "policy-1", Model: "model", Region: "region", BaseWeight: 100, MaxInflight: 10, Enabled: true}
}

func dispatchNow(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	var now time.Time
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT clock_timestamp()`).Scan(&now))
	return now.UTC()
}

func dispatchObservation(t *testing.T, db *sql.DB, target dispatch.Target, event string, outcome dispatch.Outcome) dispatch.Observation {
	t.Helper()
	return dispatch.Observation{EventID: event, TargetRef: target.Ref, AccountID: target.AccountID, CredentialRevision: target.CredentialRevision,
		Outcome: outcome, At: dispatchNow(t, db), ProviderEvidence: true}
}

func dispatchHealth(t *testing.T, db *sql.DB, core, ref string) dispatch.Health {
	t.Helper()
	var raw []byte
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT health FROM dispatch.targets WHERE core_instance=$1 AND target_ref=$2`, core, ref).Scan(&raw))
	var health dispatch.Health
	require.NoError(t, json.Unmarshal(raw, &health))
	return health
}

func dispatchExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), query, args...)
	require.NoError(t, err)
}

func dispatchLimits() dispatch.ProbeLimits {
	return dispatch.ProbeLimits{ConfigRevision: 1, LeaseDuration: time.Minute, GlobalConcurrent: 1000, ProviderRPM: 1000, DailyRequests: 10000}
}

func seedDispatchConfig(ctx context.Context, store *dispatch.Store, core string, revision int64, targets []dispatch.Target) error {
	limits := dispatchLimits()
	limits.ConfigRevision = revision
	return store.SyncConfig(ctx, core, revision, targets, limits)
}

func dispatchConfigRevision(t *testing.T, db *sql.DB, core string) int64 {
	t.Helper()
	var revision int64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT config_revision FROM dispatch.cores WHERE core_instance=$1`, core).Scan(&revision))
	return revision
}

func syncDispatchTargets(ctx context.Context, db *sql.DB, store *dispatch.Store, core string, targets []dispatch.Target) error {
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT config_revision FROM dispatch.cores WHERE core_instance=$1),0)`, core).Scan(&revision); err != nil {
		return err
	}
	return seedDispatchConfig(ctx, store, core, revision+1, targets)
}

func dispatchCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var value int
	require.NoError(t, db.QueryRowContext(t.Context(), query, args...).Scan(&value))
	return value
}

func TestDispatchSchemaExplicitAndChecksummed(t *testing.T) {
	ctx := t.Context()
	db := dispatchDB(t)
	publicOID := dispatchCount(t, db, `SELECT 'public.accounts'::regclass::oid::bigint`)
	store := dispatch.NewStore(db)
	target := dispatchTarget("target", 1)
	require.NoError(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{target}))
	require.NoError(t, dispatch.ApplySchema(ctx, db))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.targets`))
	var checksum string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT checksum FROM dispatch.schema_migrations WHERE version='001'`).Scan(&checksum))
	require.Len(t, checksum, 64)
	dispatchExec(t, db, `UPDATE dispatch.schema_migrations SET checksum='tampered' WHERE version='001'`)
	require.ErrorContains(t, dispatch.ApplySchema(ctx, db), "checksum")
	dispatchExec(t, db, `UPDATE dispatch.schema_migrations SET checksum=$1 WHERE version='001'`, checksum)
	dispatchExec(t, db, `INSERT INTO dispatch.schema_migrations(version,checksum) VALUES ('999','unknown')`)
	require.ErrorContains(t, dispatch.ApplySchema(ctx, db), "version")
	dispatchExec(t, db, `DELETE FROM dispatch.schema_migrations WHERE version='999'`)
	require.NoError(t, dispatch.ApplySchema(ctx, db))
	require.Equal(t, publicOID, dispatchCount(t, db, `SELECT 'public.accounts'::regclass::oid::bigint`))
	dispatchExec(t, db, `DROP SCHEMA dispatch CASCADE`)
	require.Error(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{target}))
	require.NoError(t, dispatch.ApplySchema(ctx, db))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errs <- dispatch.ApplySchema(ctx, db) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 5, dispatchCount(t, db, `SELECT count(*) FROM dispatch.schema_migrations`))
	require.Equal(t, 5, dispatchCount(t, db, `SELECT count(*) FROM dispatch.schema_migrations WHERE version IN ('001','003','004','005','006')`))
}

func TestDispatchSyncIdentityAndCredentialRotation(t *testing.T) {
	ctx := t.Context()
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b := dispatchTarget("a", 1), dispatchTarget("b", 2)
	require.NoError(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{a, b}))
	for _, mutate := range []func(*dispatch.Target){
		func(v *dispatch.Target) { v.AccountID++ }, func(v *dispatch.Target) { v.PoolRef = "other" },
		func(v *dispatch.Target) { v.Model = "other" }, func(v *dispatch.Target) { v.Region = "other" },
		func(v *dispatch.Target) { v.CredentialRef = "other" }, func(v *dispatch.Target) { v.ProviderRef = "other" },
	} {
		changed := a
		mutate(&changed)
		require.Error(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{changed, b}))
	}
	require.NoError(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{a}))
	snapshot, err := store.Publish(ctx, "core", dispatchConfigRevision(t, db, "core"), 30*time.Second)
	require.NoError(t, err)
	require.Len(t, snapshot.Candidates, 2)
	require.False(t, snapshot.Candidates[1].Enabled)
	require.EqualValues(t, 0, snapshot.Candidates[1].EffectiveWeight)
	o := dispatchObservation(t, db, a, "auth", dispatch.UpstreamAuth)
	inserted, err := store.Observe(ctx, "core", o)
	require.NoError(t, err)
	require.True(t, inserted)
	alias := a
	alias.Ref = "alias"
	alias.PoolRef = "other-pool"
	require.NoError(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{a, alias}))
	require.True(t, dispatchHealth(t, db, "core", "alias").AuthBlocked)
	limitsBefore := dispatchLimits()
	limitsBefore.ConfigRevision = dispatchConfigRevision(t, db, "core")
	lease, err := store.ClaimProbe(ctx, "core", "alias", "owner", limitsBefore)
	require.NoError(t, err)
	require.Nil(t, lease)
	updated := a
	updated.CredentialRevision = 2
	require.NoError(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{updated}))
	health := dispatchHealth(t, db, "core", "a")
	require.False(t, health.AuthBlocked)
	require.Equal(t, dispatch.Tripped, health.State)
	require.Error(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{a}))
	require.Error(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{alias}))
	_, err = store.Observe(ctx, "core", dispatchObservation(t, db, a, "old-credential", dispatch.Success))
	require.Error(t, err)
	limits := dispatchLimits()
	limits.ConfigRevision = dispatchConfigRevision(t, db, "core")
	lease, err = store.ClaimProbe(ctx, "core", "a", "owner", limits)
	require.NoError(t, err)
	require.NotNil(t, lease)
	require.Equal(t, dispatch.Probing, dispatchHealth(t, db, "core", "a").State)
	oldLease := *lease
	updated.PolicyRevision = "policy-2"
	require.NoError(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{updated}))
	require.ErrorIs(t, store.FinishProbe(ctx, oldLease, dispatchObservation(t, db, a, "old-finish", dispatch.Success)), dispatch.ErrProbeLease)
	require.NotEqual(t, dispatch.Healthy, dispatchHealth(t, db, "core", "a").State)
}

func TestDispatchObservationIdempotencyAndFaultDomains(t *testing.T) {
	ctx := t.Context()
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	alias := a
	alias.Ref = "same-domain"
	alias.PoolRef = "duplicate-pool"
	credentialPeer := dispatchTarget("same-credential", 2)
	credentialPeer.CredentialRef = a.CredentialRef
	credentialPeer.Model = "other-model"
	credentialPeer.Region = "other-region"
	credentialPeer.PoolRef = "other-pool"
	otherAccount := dispatchTarget("other-account", 3)
	otherModel := a
	otherModel.Ref = "other-model"
	otherModel.Model = "other-model"
	otherRegion := a
	otherRegion.Ref = "other-region"
	otherRegion.Region = "other-region"
	otherPool := dispatchTarget("other-pool", 4)
	otherPool.PoolRef = "other-pool"
	targets := []dispatch.Target{a, alias, credentialPeer, otherAccount, otherModel, otherRegion, otherPool}
	require.NoError(t, syncDispatchTargets(ctx, db, store, "core", targets))
	require.NoError(t, syncDispatchTargets(ctx, db, store, "other-core", targets))
	base := dispatchHealth(t, db, "core", "a")
	for i, outcome := range []dispatch.Outcome{dispatch.LocalRejection, dispatch.ClientCanceled, dispatch.Unknown, dispatch.UpstreamAuth, dispatch.UpstreamRateLimit, dispatch.UpstreamFailure} {
		o := dispatchObservation(t, db, a, fmt.Sprintf("unattributed-%d", i), outcome)
		if i >= 3 {
			o.ProviderEvidence = false
		}
		inserted, err := store.Observe(ctx, "core", o)
		require.NoError(t, err)
		require.True(t, inserted)
	}
	require.Equal(t, base, dispatchHealth(t, db, "core", "a"))
	o := dispatchObservation(t, db, a, "rate", dispatch.UpstreamRateLimit)
	o.RetryAfterSeconds = 90
	var wg sync.WaitGroup
	type result struct {
		inserted bool
		err      error
	}
	results := make(chan result, 50)
	for range 50 {
		wg.Go(func() {
			inserted, err := dispatch.NewStore(db).Observe(ctx, "core", o)
			results <- result{inserted, err}
		})
	}
	wg.Wait()
	close(results)
	inserts := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.inserted {
			inserts++
		}
	}
	require.Equal(t, 1, inserts)
	for _, ref := range []string{"a", "same-domain"} {
		h := dispatchHealth(t, db, "core", ref)
		require.Equal(t, dispatch.Tripped, h.State)
		require.Equal(t, "rate_limited", h.Reason)
	}
	for _, ref := range []string{"same-credential", "other-account", "other-model", "other-region", "other-pool"} {
		require.Equal(t, dispatch.Healthy, dispatchHealth(t, db, "core", ref).State)
	}
	require.Equal(t, dispatch.Healthy, dispatchHealth(t, db, "other-core", "a").State)
	conflict := o
	conflict.RetryAfterSeconds++
	inserted, err := store.Observe(ctx, "core", conflict)
	require.ErrorIs(t, err, dispatch.ErrObservationConflict)
	require.False(t, inserted)
	inserted, err = store.Observe(ctx, "other-core", o)
	require.NoError(t, err)
	require.True(t, inserted)
	for i := range 5 {
		_, err := store.Observe(ctx, "core", dispatchObservation(t, db, otherAccount, fmt.Sprintf("failure-%d", i), dispatch.UpstreamFailure))
		require.NoError(t, err)
	}
	require.Equal(t, dispatch.Tripped, dispatchHealth(t, db, "core", "other-account").State)
	require.Equal(t, dispatch.Healthy, dispatchHealth(t, db, "core", "other-model").State)
	// 较早的认证事实依然封禁凭据的全部目标，不受别名较新成功样本遮蔽。
	auth := dispatchObservation(t, db, a, "auth", dispatch.UpstreamAuth)
	success := dispatchObservation(t, db, credentialPeer, "later-success", dispatch.Success)
	_, err = store.Observe(ctx, "core", success)
	require.NoError(t, err)
	_, err = store.Observe(ctx, "core", auth)
	require.NoError(t, err)
	for _, target := range targets {
		h := dispatchHealth(t, db, "core", target.Ref)
		if target.CredentialRef == a.CredentialRef {
			require.True(t, h.AuthBlocked, target.Ref)
			require.Equal(t, dispatch.Tripped, h.State)
		} else {
			require.False(t, h.AuthBlocked, target.Ref)
		}
	}
	for i, change := range []func(*dispatch.Observation){
		func(o *dispatch.Observation) { o.At = o.At.Add(-6 * time.Minute) }, func(o *dispatch.Observation) { o.At = o.At.Add(10 * time.Second) },
		func(o *dispatch.Observation) { o.AccountID++ }, func(o *dispatch.Observation) { o.CredentialRevision++ },
		func(o *dispatch.Observation) { o.Outcome = "invalid" },
	} {
		bad := dispatchObservation(t, db, a, fmt.Sprintf("invalid-%d", i), dispatch.Success)
		change(&bad)
		_, err := store.Observe(ctx, "core", bad)
		require.Error(t, err)
	}
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE event_id LIKE 'invalid-%'`))
	// 历史事件夹具越过新事件的时间窗，重投仍幂等。
	historical := auth
	historical.At = historical.At.Add(-6 * time.Minute)
	raw, err := json.Marshal(historical)
	require.NoError(t, err)
	dispatchExec(t, db, `UPDATE dispatch.observations SET payload=$1 WHERE core_instance='core' AND event_id='auth'`, string(raw))
	inserted, err = store.Observe(ctx, "core", historical)
	require.NoError(t, err)
	require.False(t, inserted)
}

func TestDispatchConfigRevisionFence(t *testing.T) {
	ctx := t.Context()
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b := dispatchTarget("a", 1), dispatchTarget("b", 2)
	require.ErrorIs(t, seedDispatchConfig(ctx, store, "core", 0, []dispatch.Target{a}), dispatch.ErrConfigRevision)
	require.NoError(t, seedDispatchConfig(ctx, store, "core", 1, []dispatch.Target{a, b}))
	limits := dispatchLimits()
	lease, err := store.ClaimProbe(ctx, "core", a.Ref, "old-owner", limits)
	require.NoError(t, err)
	require.NotNil(t, lease)
	before := dispatchCount(t, db, `SELECT generation FROM dispatch.targets WHERE core_instance='core' AND target_ref='a'`)
	require.NoError(t, seedDispatchConfig(ctx, store, "core", 1, []dispatch.Target{b, a}))
	require.Equal(t, before, dispatchCount(t, db, `SELECT generation FROM dispatch.targets WHERE core_instance='core' AND target_ref='a'`))
	changed := b
	changed.PolicyRevision = "policy-2"
	require.NoError(t, seedDispatchConfig(ctx, store, "core", 2, []dispatch.Target{changed}))
	require.ErrorIs(t, seedDispatchConfig(ctx, store, "core", 1, []dispatch.Target{a, b}), dispatch.ErrConfigRevision)
	require.ErrorIs(t, seedDispatchConfig(ctx, store, "core", 2, []dispatch.Target{a, b}), dispatch.ErrConfigRevision)
	require.ErrorIs(t, store.FinishProbe(ctx, *lease, dispatchObservation(t, db, a, lease.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	_, err = store.ClaimProbe(ctx, "core", b.Ref, "stale-round", limits)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	_, err = store.Publish(ctx, "core", 1, 30*time.Second)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	path := filepath.Join(t.TempDir(), "snapshot.json")
	_, err = store.PublishTo(ctx, "core", 1, 30*time.Second, path)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err))
	limits.ConfigRevision = 2
	second, err := store.ClaimProbe(ctx, "core", b.Ref, "new-owner", limits)
	require.NoError(t, err)
	require.NotNil(t, second)
	// 即使内容相同，新操作版本也使旧 worker 结果失效。
	require.NoError(t, seedDispatchConfig(ctx, store, "core", 3, []dispatch.Target{changed}))
	require.ErrorIs(t, store.FinishProbe(ctx, *second, dispatchObservation(t, db, changed, second.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	changed.Enabled = false
	require.NoError(t, seedDispatchConfig(ctx, store, "core", 4, []dispatch.Target{changed}))
	require.ErrorIs(t, seedDispatchConfig(ctx, store, "core", 3, []dispatch.Target{b}), dispatch.ErrConfigRevision)
	snapshot, err := store.Publish(ctx, "core", 4, 30*time.Second)
	require.NoError(t, err)
	for _, candidate := range snapshot.Candidates {
		require.False(t, candidate.Enabled)
		require.Zero(t, candidate.EffectiveWeight)
	}
	require.EqualValues(t, 4, dispatchConfigRevision(t, db, "core"))
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
	// 并发配置同步的最终状态只能停在最大 revision。
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, version := range []int64{5, 6} {
		wg.Go(func() { errs <- seedDispatchConfig(ctx, store, "core", version, []dispatch.Target{changed}) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			require.ErrorIs(t, err, dispatch.ErrConfigRevision)
		}
	}
	require.EqualValues(t, 6, dispatchConfigRevision(t, db, "core"))
}

func TestDispatchAuthoritativeProbeLimits(t *testing.T) {
	ctx := t.Context()
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b := dispatchTarget("a", 1), dispatchTarget("b", 2)
	targets := []dispatch.Target{a, b}
	limits := dispatchLimits()
	require.NoError(t, store.SyncConfig(ctx, "core", 1, targets, limits))
	for _, mutate := range []func(*dispatch.ProbeLimits){
		func(l *dispatch.ProbeLimits) { l.GlobalConcurrent++ }, func(l *dispatch.ProbeLimits) { l.ProviderRPM++ },
		func(l *dispatch.ProbeLimits) { l.DailyRequests++ }, func(l *dispatch.ProbeLimits) { l.LeaseDuration += time.Second },
	} {
		changed := limits
		mutate(&changed)
		require.ErrorIs(t, store.SyncConfig(ctx, "core", 1, targets, changed), dispatch.ErrConfigRevision)
		_, err := store.ClaimProbe(ctx, "core", a.Ref, "unconfigured", changed)
		require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	}
	lease, err := store.ClaimProbe(ctx, "core", a.Ref, "old-owner", limits)
	require.NoError(t, err)
	require.NotNil(t, lease)
	lower := dispatch.ProbeLimits{ConfigRevision: 2, LeaseDuration: time.Minute, GlobalConcurrent: 1, ProviderRPM: 1, DailyRequests: 1}
	require.NoError(t, store.SyncConfig(ctx, "core", 2, targets, lower))
	_, err = store.ClaimProbe(ctx, "core", b.Ref, "old-round", limits)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	pretendNew := limits
	pretendNew.ConfigRevision = 2
	_, err = store.ClaimProbe(ctx, "core", b.Ref, "old-budget-new-version", pretendNew)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	require.ErrorIs(t, store.FinishProbe(ctx, *lease, dispatchObservation(t, db, a, lease.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	next, err := store.ClaimProbe(ctx, "core", b.Ref, "lower-budget", lower)
	require.NoError(t, err)
	require.Nil(t, next)
	disabled := dispatch.ProbeLimits{ConfigRevision: 3}
	require.NoError(t, store.SyncConfig(ctx, "core", 3, targets, disabled))
	next, err = store.ClaimProbe(ctx, "core", b.Ref, "disabled", disabled)
	require.NoError(t, err)
	require.Nil(t, next)
	wrongEnabled := limits
	wrongEnabled.ConfigRevision = 3
	_, err = store.ClaimProbe(ctx, "core", b.Ref, "disabled-bypass", wrongEnabled)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	partial := dispatch.ProbeLimits{ConfigRevision: 4, GlobalConcurrent: 1}
	require.Error(t, store.SyncConfig(ctx, "core", 4, targets, partial))
	require.EqualValues(t, 3, dispatchConfigRevision(t, db, "core"))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
}

func TestDispatchMultiCoreGlobalBudgetMinimum(t *testing.T) {
	for _, name := range []string{"global", "provider-rpm", "utc-daily"} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			low := dispatchLimits()
			high := dispatchLimits()
			switch name {
			case "global":
				low.GlobalConcurrent = 1
			case "provider-rpm":
				low.ProviderRPM = 1
			case "utc-daily":
				low.DailyRequests = 1
			}
			a := dispatchTarget("a", 1)
			disabled := dispatchTarget("disabled-other-provider", 2)
			disabled.ProviderRef = "other-provider"
			disabled.Enabled = false
			b, c, d := dispatchTarget("b", 3), dispatchTarget("c", 4), dispatchTarget("d", 5)
			d.ProviderRef = "other-provider"
			require.NoError(t, store.SyncConfig(ctx, "low-core", 1, []dispatch.Target{a, disabled}, low))
			require.NoError(t, store.SyncConfig(ctx, "high-core", 1, []dispatch.Target{b, c, d}, high))
			require.NoError(t, store.SyncConfig(ctx, "disabled-core", 1, []dispatch.Target{dispatchTarget("off", 6)}, dispatch.ProbeLimits{ConfigRevision: 1}))
			lease, err := store.ClaimProbe(ctx, "high-core", b.Ref, "owner", high)
			require.NoError(t, err)
			require.NotNil(t, lease)
			if name == "utc-daily" {
				require.NoError(t, store.FinishProbe(ctx, *lease, dispatchObservation(t, db, b, lease.AttemptID, dispatch.Unknown)))
			}
			blocked, err := store.ClaimProbe(ctx, "high-core", c.Ref, "higher-config", high)
			require.NoError(t, err)
			require.Nil(t, blocked)
			if name == "provider-rpm" {
				other, err := store.ClaimProbe(ctx, "high-core", d.Ref, "other-provider", high)
				require.NoError(t, err)
				require.NotNil(t, other)
			}
			if name == "global" {
				require.NoError(t, store.FinishProbe(ctx, *lease, dispatchObservation(t, db, b, lease.AttemptID, dispatch.Unknown)))
				freed, err := store.ClaimProbe(ctx, "high-core", c.Ref, "free-concurrent-slot", high)
				require.NoError(t, err)
				require.NotNil(t, freed)
			}
		})
	}
}

func TestDispatchProbeExpiryDuringWrite(t *testing.T) {
	ctx := t.Context()
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	limits := dispatchLimits()
	limits.LeaseDuration = 200 * time.Millisecond
	require.NoError(t, store.SyncConfig(ctx, "core", 1, []dispatch.Target{a}, limits))
	lease, err := store.ClaimProbe(ctx, "core", a.Ref, "owner", limits)
	require.NoError(t, err)
	require.NotNil(t, lease)
	before := dispatchHealth(t, db, "core", a.Ref)
	dispatchExec(t, db, `CREATE FUNCTION dispatch.slow_health() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.25); RETURN NEW; END $$;
		CREATE TRIGGER slow_health BEFORE UPDATE ON dispatch.targets FOR EACH ROW EXECUTE FUNCTION dispatch.slow_health()`)
	require.ErrorIs(t, store.FinishProbe(ctx, *lease, dispatchObservation(t, db, a, lease.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	require.Equal(t, before, dispatchHealth(t, db, "core", a.Ref))
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE finished_at IS NULL`))
}

func TestDispatchProbeNaturalExpiryAndBudgetPersistence(t *testing.T) {
	ctx := t.Context()
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a, b := dispatchTarget("a", 1), dispatchTarget("b", 2)
	limits := dispatchLimits()
	limits.LeaseDuration = 40 * time.Millisecond
	limits.DailyRequests = 1
	require.NoError(t, store.SyncConfig(ctx, "core", 1, []dispatch.Target{a, b}, limits))
	lease, err := store.ClaimProbe(ctx, "core", a.Ref, "lost-process", limits)
	require.NoError(t, err)
	require.NotNil(t, lease)
	time.Sleep(80 * time.Millisecond)
	require.True(t, dispatchNow(t, db).After(lease.ExpiresAt))
	require.ErrorIs(t, store.FinishProbe(ctx, *lease, dispatchObservation(t, db, a, lease.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	restarted, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Close()) })
	next, err := dispatch.NewStore(restarted).ClaimProbe(ctx, "core", b.Ref, "restarted-process", limits)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE finished_at IS NULL`))
	// 昨日的额度仍保留历史，但不占用 DB 当前 UTC 日预算。
	dispatchExec(t, db, `UPDATE dispatch.probes SET reserved_at=date_trunc('day',clock_timestamp() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'-interval '1 second'`)
	next, err = dispatch.NewStore(restarted).ClaimProbe(ctx, "core", b.Ref, "today", limits)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
}

func TestDispatchProbeBudgetConcurrency(t *testing.T) {
	for _, test := range []struct {
		name                     string
		global, rpm, daily, want int
	}{
		{"global", 7, 1000, 1000, 7}, {"provider-rpm", 1000, 9, 1000, 9}, {"utc-daily", 1000, 1000, 11, 11},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			targets := make([]dispatch.Target, 200)
			for i := range targets {
				targets[i] = dispatchTarget(fmt.Sprintf("t-%03d", i), int64(i+1))
			}
			limits := dispatch.ProbeLimits{ConfigRevision: 1, LeaseDuration: time.Minute, GlobalConcurrent: test.global, ProviderRPM: test.rpm, DailyRequests: test.daily}
			if test.name == "utc-daily" {
				limits.LeaseDuration = 40 * time.Millisecond
			}
			require.NoError(t, store.SyncConfig(ctx, "core", 1, targets[:100], limits))
			require.NoError(t, store.SyncConfig(ctx, "other-core", 1, targets[100:], limits))
			type result struct {
				lease *dispatch.ProbeLease
				err   error
			}
			results := make(chan result, 200)
			var wg sync.WaitGroup
			for i, target := range targets {
				wg.Go(func() {
					core := "core"
					if i >= 100 {
						core = "other-core"
					}
					lease, err := dispatch.NewStore(db).ClaimProbe(ctx, core, target.Ref, fmt.Sprintf("owner-%d", i), limits)
					results <- result{lease, err}
				})
			}
			wg.Wait()
			close(results)
			leases := []dispatch.ProbeLease{}
			for result := range results {
				require.NoError(t, result.err)
				if result.lease != nil {
					leases = append(leases, *result.lease)
				}
			}
			require.Len(t, leases, test.want)
			require.Equal(t, test.want, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
			if test.name == "utc-daily" {
				time.Sleep(80 * time.Millisecond)
				require.Equal(t, test.want, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE lease_until<=clock_timestamp()`))
			}
			// Store 重建与连接池重开不能清空任何持久预算。
			other, err := sql.Open("postgres", integrationDSN)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, other.Close()) })
			if test.name != "global" {
				for _, lease := range leases {
					err := store.FinishProbe(ctx, lease, dispatchObservation(t, db, lease.Target, lease.AttemptID, dispatch.Unknown))
					if test.name == "utc-daily" {
						require.ErrorIs(t, err, dispatch.ErrProbeLease)
						require.False(t, dispatchNow(t, db).Before(lease.ExpiresAt))
						require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE core_instance=$1 AND event_id=$2`, lease.CoreInstance, lease.AttemptID))
					} else {
						require.NoError(t, err)
					}
				}
			}
			for _, target := range targets[:100] {
				lease, err := dispatch.NewStore(other).ClaimProbe(ctx, "core", target.Ref, "restarted", limits)
				require.NoError(t, err)
				require.Nil(t, lease)
			}
			if test.name == "provider-rpm" {
				extra := dispatchTarget("different-provider", 1001)
				extra.ProviderRef = "provider-2"
				require.NoError(t, store.SyncConfig(ctx, "extra-core", 1, []dispatch.Target{extra}, limits))
				lease, err := store.ClaimProbe(ctx, "extra-core", extra.Ref, "owner", limits)
				require.NoError(t, err)
				require.NotNil(t, lease)
			}
		})
	}
}

func TestDispatchProbeSlotLeaseGenerationAndUnknown(t *testing.T) {
	ctx := t.Context()
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	alias := a
	alias.Ref = "alias"
	alias.PoolRef = "alias-pool"
	require.NoError(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{a, alias}))
	limits := dispatchLimits()
	type result struct {
		lease *dispatch.ProbeLease
		err   error
	}
	results := make(chan result, 200)
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Go(func() {
			ref := a.Ref
			if i%2 == 0 {
				ref = alias.Ref
			}
			lease, err := dispatch.NewStore(db).ClaimProbe(ctx, "core", ref, fmt.Sprintf("owner-%d", i), limits)
			results <- result{lease, err}
		})
	}
	wg.Wait()
	close(results)
	var first *dispatch.ProbeLease
	for result := range results {
		require.NoError(t, result.err)
		if result.lease != nil {
			require.Nil(t, first)
			first = result.lease
		}
	}
	require.NotNil(t, first)
	require.Equal(t, dispatch.Healthy, dispatchHealth(t, db, "core", first.Target.Ref).State)
	wrongEvent := dispatchObservation(t, db, first.Target, "wrong-attempt-event", dispatch.Success)
	require.ErrorIs(t, store.FinishProbe(ctx, *first, wrongEvent), dispatch.ErrProbeLease)
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations WHERE event_id='wrong-attempt-event'`))
	for _, mutate := range []func(*dispatch.ProbeLease){
		func(l *dispatch.ProbeLease) { l.Token = "unknown" }, func(l *dispatch.ProbeLease) { l.Generation++ },
		func(l *dispatch.ProbeLease) { l.AttemptID = "wrong" }, func(l *dispatch.ProbeLease) { l.ReservationID = "wrong" },
		func(l *dispatch.ProbeLease) { l.Target.AccountID++ }, func(l *dispatch.ProbeLease) { l.ExpiresAt = l.ExpiresAt.Add(time.Second) },
	} {
		bad := *first
		mutate(&bad)
		require.ErrorIs(t, store.FinishProbe(ctx, bad, dispatchObservation(t, db, first.Target, first.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	}
	// 模拟进程消失和 DB lease 超时；同 slot 仍不可重试或退款。
	firstSlot := dispatchCount(t, db, `SELECT slot FROM dispatch.probes WHERE token=$1`, first.Token)
	dispatchExec(t, db, `UPDATE dispatch.probes SET lease_until=clock_timestamp()-interval '1 microsecond' WHERE token=$1`, first.Token)
	require.ErrorIs(t, store.FinishProbe(ctx, *first, dispatchObservation(t, db, first.Target, first.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	lease, err := store.ClaimProbe(ctx, "core", a.Ref, "next-owner", limits)
	require.NoError(t, err)
	expectedProbes := 2
	if lease != nil {
		// 并发矩阵可能跨过真实的 30 秒边界；只允许新的 slot 产生新的探针。
		require.Greater(t, dispatchCount(t, db, `SELECT slot FROM dispatch.probes WHERE token=$1`, lease.Token), firstSlot)
		require.NotEqual(t, first.AttemptID, lease.AttemptID)
		require.NoError(t, store.FinishProbe(ctx, *lease, dispatchObservation(t, db, lease.Target, lease.AttemptID, dispatch.Unknown)))
		expectedProbes++
	}
	// 历史 slot 仅在夹具中前移，不等待墙钟；生产只生成 DB 当前 slot。
	dispatchExec(t, db, `UPDATE dispatch.probes SET slot=slot-10`)
	lease, err = store.ClaimProbe(ctx, "core", first.Target.Ref, "new-slot", limits)
	require.NoError(t, err)
	require.NotNil(t, lease)
	require.Greater(t, lease.Generation, first.Generation)
	require.NotEqual(t, lease.AttemptID, first.AttemptID)
	require.NotEqual(t, lease.ReservationID, first.ReservationID)
	require.ErrorIs(t, store.FinishProbe(ctx, *first, dispatchObservation(t, db, first.Target, first.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	require.NoError(t, store.FinishProbe(ctx, *lease, dispatchObservation(t, db, lease.Target, lease.AttemptID, dispatch.Unknown)))
	require.ErrorIs(t, store.FinishProbe(ctx, *lease, dispatchObservation(t, db, lease.Target, lease.AttemptID, dispatch.Success)), dispatch.ErrProbeLease)
	require.Equal(t, expectedProbes, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes`))
	// 半开 Unknown/缺回执不得恢复，真实可信探活需两次独立成功。
	_, err = store.Observe(ctx, "core", dispatchObservation(t, db, a, "trip", dispatch.UpstreamRateLimit))
	require.NoError(t, err)
	for i, outcome := range []dispatch.Outcome{dispatch.Unknown, dispatch.Success, dispatch.Success} {
		dispatchExec(t, db, `UPDATE dispatch.probes SET slot=slot-10`)
		dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health,'{cool_until}',to_jsonb(clock_timestamp()-interval '1 second')) WHERE core_instance='core'`)
		lease, err := store.ClaimProbe(ctx, "core", a.Ref, "owner", limits)
		require.NoError(t, err)
		require.NotNil(t, lease)
		require.Equal(t, dispatch.Probing, dispatchHealth(t, db, "core", "a").State)
		observation := dispatchObservation(t, db, a, lease.AttemptID, outcome)
		if outcome == dispatch.Unknown {
			observation.ProviderEvidence = false
		}
		require.NoError(t, store.FinishProbe(ctx, *lease, observation))
		if i < 2 {
			require.Equal(t, dispatch.Tripped, dispatchHealth(t, db, "core", "a").State)
		}
	}
	require.Equal(t, dispatch.Degraded, dispatchHealth(t, db, "core", "a").State)
	_, err = store.Observe(ctx, "core", dispatchObservation(t, db, a, "block", dispatch.UpstreamAuth))
	require.NoError(t, err)
	dispatchExec(t, db, `UPDATE dispatch.probes SET slot=slot-10`)
	dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health,'{cool_until}',to_jsonb(clock_timestamp()-interval '1 day'))`)
	lease, err = dispatch.NewStore(db).ClaimProbe(ctx, "core", a.Ref, "later-owner", limits)
	require.NoError(t, err)
	require.Nil(t, lease)
}

func TestDispatchPublishMonotonicFileAndRollback(t *testing.T) {
	ctx := t.Context()
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	disabled := dispatchTarget("disabled", 2)
	disabled.Enabled = false
	require.NoError(t, syncDispatchTargets(ctx, db, store, "core", []dispatch.Target{a, disabled}))
	_, err := store.Observe(ctx, "core", dispatchObservation(t, db, a, "auth", dispatch.UpstreamAuth))
	require.NoError(t, err)
	for _, ttl := range []time.Duration{0, -time.Second, 31 * time.Second} {
		_, err := store.Publish(ctx, "core", dispatchConfigRevision(t, db, "core"), ttl)
		require.Error(t, err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	type result struct {
		snapshot dispatch.Snapshot
		err      error
	}
	results := make(chan result, 50)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			snapshot, err := dispatch.NewStore(db).PublishTo(ctx, "core", 1, 30*time.Second, path)
			results <- result{snapshot, err}
		})
	}
	wg.Wait()
	close(results)
	revisions := []int64{}
	for result := range results {
		require.NoError(t, result.err)
		require.NoError(t, result.snapshot.Validate(dispatchNow(t, db)))
		revisions = append(revisions, result.snapshot.Revision)
	}
	sort.Slice(revisions, func(i, j int) bool { return revisions[i] < revisions[j] })
	for i := 1; i < len(revisions); i++ {
		require.Greater(t, revisions[i], revisions[i-1])
	}
	latest, err := dispatch.LoadSnapshot(path, dispatchNow(t, db))
	require.NoError(t, err)
	require.Equal(t, revisions[len(revisions)-1], latest.Revision)
	require.Len(t, latest.Candidates, 2)
	require.Equal(t, "credential_rejected", latest.Candidates[0].Reason)
	require.Zero(t, latest.Candidates[0].EffectiveWeight)
	require.False(t, latest.Candidates[1].Enabled)
	require.Zero(t, latest.Candidates[1].EffectiveWeight)
	storedRevision := dispatchCount(t, db, `SELECT revision FROM dispatch.cores WHERE core_instance='core'`)
	sequenceBefore := dispatchCount(t, db, `SELECT last_value FROM dispatch.snapshot_revision`)
	_, err = store.PublishTo(ctx, "core", dispatchConfigRevision(t, db, "core"), 30*time.Second, filepath.Join(t.TempDir(), "absent", "snapshot.json"))
	require.Error(t, err)
	require.Equal(t, storedRevision, dispatchCount(t, db, `SELECT revision FROM dispatch.cores WHERE core_instance='core'`))
	require.Greater(t, dispatchCount(t, db, `SELECT last_value FROM dispatch.snapshot_revision`), sequenceBefore)
	unchanged, err := dispatch.LoadSnapshot(path, dispatchNow(t, db))
	require.NoError(t, err)
	require.Equal(t, latest.Revision, unchanged.Revision)
	// 文件已暴露、数据库提交失败时，下一次发布仍必须使用更大 revision。
	dispatchExec(t, db, `CREATE FUNCTION dispatch.reject_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture commit failure'; END $$;
		CREATE CONSTRAINT TRIGGER reject_snapshot_commit AFTER UPDATE ON dispatch.cores DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION dispatch.reject_commit()`)
	_, err = store.PublishTo(ctx, "core", dispatchConfigRevision(t, db, "core"), 30*time.Second, path)
	require.ErrorContains(t, err, "fixture commit failure")
	exposed, err := dispatch.LoadSnapshot(path, dispatchNow(t, db))
	require.NoError(t, err)
	require.Greater(t, exposed.Revision, latest.Revision)
	require.Equal(t, storedRevision, dispatchCount(t, db, `SELECT revision FROM dispatch.cores WHERE core_instance='core'`))
	dispatchExec(t, db, `DROP TRIGGER reject_snapshot_commit ON dispatch.cores; DROP FUNCTION dispatch.reject_commit()`)
	next, err := dispatch.NewStore(db).PublishTo(ctx, "core", 1, 30*time.Second, path)
	require.NoError(t, err)
	require.Greater(t, next.Revision, exposed.Revision)
	// 构建期间耗尽 TTL 时，原文件维持原样，序号可以留空洞。
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	dispatchExec(t, db, `CREATE FUNCTION dispatch.slow_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.02); RETURN NEW; END $$;
		CREATE TRIGGER slow_snapshot BEFORE UPDATE ON dispatch.cores FOR EACH ROW EXECUTE FUNCTION dispatch.slow_snapshot()`)
	_, err = store.PublishTo(ctx, "core", dispatchConfigRevision(t, db, "core"), time.Millisecond, path)
	require.Error(t, err)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, body, after)
}
