//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func failureConfig(t *testing.T, targets []dispatch.Target, routing bool) dispatch.Config {
	t.Helper()
	cfg := dispatch.Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, Targets: targets,
		SnapshotFile: filepath.Join(t.TempDir(), "snapshot.json"), SnapshotTTLSeconds: 30,
		Probe: &dispatch.ProbeConfig{LeaseSeconds: 60, GlobalConcurrent: 1000, ProviderRPM: 1000, DailyRequests: 10000}}
	if routing {
		cfg.Telemetry = &dispatch.TelemetryConfig{SlotTTLSeconds: 1800, LoadMaxAgeSeconds: 5, MaxObservations: 100}
		cfg.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}
	}
	return cfg
}

func failureSnapshot(t *testing.T, db *sql.DB, store *dispatch.Store, cfg dispatch.Config) dispatch.Snapshot {
	t.Helper()
	var sample *dispatch.LoadSample
	if cfg.Routing != nil {
		sample = &dispatch.LoadSample{ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{}}
		for _, target := range cfg.Targets {
			if target.Enabled {
				sample.Accounts[target.AccountID] = 0
			}
		}
	}
	snapshot, err := store.PublishRuntime(t.Context(), cfg, sample)
	require.NoError(t, err)
	return snapshot
}

func failureEvent(t *testing.T, db *sql.DB, target dispatch.Target, id, class string) dispatch.Observation {
	t.Helper()
	outcome := dispatch.UpstreamAuth
	if class == dispatch.FailureQuotaExhausted {
		outcome = dispatch.UpstreamRateLimit
	}
	o := dispatchObservation(t, db, target, id, outcome)
	o.FailureClass = class
	return o
}

func releaseRequest(h dispatch.HealthHoldReport, cfg dispatch.Config, operation string) dispatch.HoldReleaseRequest {
	return dispatch.HoldReleaseRequest{CoreInstance: cfg.CoreInstance, ConfigRevision: cfg.ConfigRevision,
		HoldID: h.ID, HoldRevision: h.Revision, OperationID: operation, Actor: "operator-assertion", Reason: "task-228 resolved", EvidenceRef: "ticket:228"}
}

func TestDispatchFailureScopesAndRoutingHardVeto(t *testing.T) {
	for _, routing := range []bool{false, true} {
		for _, kind := range []string{dispatch.FailureCredentialBanned, dispatch.FailureCapabilityDenied, "quota_shared", "quota_account"} {
			t.Run(fmt.Sprintf("%s/routing-%v", kind, routing), func(t *testing.T) {
				db := dispatchDB(t)
				store := dispatch.NewStore(db)
				a := dispatchTarget("a", 1)
				a.QuotaRef = "shared"
				alias := a
				alias.Ref, alias.PoolRef = "alias", "other-pool"
				credential := a
				credential.Ref, credential.AccountID, credential.Model, credential.Region = "credential", 2, "other", "other"
				domain := a
				domain.Ref, domain.CredentialRef, domain.ProviderRef = "domain", "different-credential", "different-provider"
				quota := dispatchTarget("quota", 3)
				quota.QuotaRef = "shared"
				otherProvider := dispatchTarget("other-provider", 4)
				otherProvider.ProviderRef, otherProvider.QuotaRef = "different-provider", "shared"
				class := kind
				want := map[string]bool{"a": true, "alias": true}
				switch kind {
				case dispatch.FailureCredentialBanned:
					want["credential"] = true
				case dispatch.FailureCapabilityDenied:
					want["domain"] = true
				case "quota_shared":
					class = dispatch.FailureQuotaExhausted
					want["credential"], want["quota"] = true, true
				case "quota_account":
					class, a.QuotaRef, alias.QuotaRef = dispatch.FailureQuotaExhausted, "", ""
					want["domain"] = true
				}
				cfg := failureConfig(t, []dispatch.Target{a, alias, credential, domain, quota, otherProvider}, routing)
				require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
				other := cfg
				other.CoreInstance, other.SnapshotFile = "other-core", filepath.Join(t.TempDir(), "other.json")
				require.NoError(t, store.SyncRuntimeConfig(t.Context(), other))
				o := failureEvent(t, db, a, "failure", class)
				inserted, err := store.Observe(t.Context(), "core", o)
				require.NoError(t, err)
				require.True(t, inserted)
				holds, err := store.HealthHolds(t.Context(), "core", 1)
				require.NoError(t, err)
				require.Len(t, holds, 1)
				require.True(t, holds[0].Active)
				require.EqualValues(t, 1, holds[0].Revision)
				for _, target := range cfg.Targets {
					require.False(t, dispatchHealth(t, db, "core", target.Ref).AuthBlocked, "new classes must not become legacy auth blocks")
				}
				// 即便底层 Health 被独立写入为健康，持久锁仍在 routing 后硬否决。
				dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health,'{state}','"healthy"') WHERE core_instance='core'`)
				for _, c := range failureSnapshot(t, db, dispatch.NewStore(db), cfg).Candidates {
					require.Equal(t, want[c.Ref], c.EffectiveWeight == 0, "candidate %s", c.Ref)
					if want[c.Ref] {
						lease, err := store.ClaimProbe(t.Context(), "core", c.Ref, "owner", cfg.ProbeLimits())
						require.NoError(t, err)
						require.Nil(t, lease)
					}
				}
				for _, c := range failureSnapshot(t, db, store, other).Candidates {
					require.Positive(t, c.EffectiveWeight, "holds do not cross Core instances")
				}
				inserted, err = dispatch.NewStore(db).Observe(t.Context(), "core", o)
				require.NoError(t, err)
				require.False(t, inserted)
				require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.health_hold_audit`))
			})
		}
	}
}

func TestDispatchFailureConfigRotationAndRefRetention(t *testing.T) {
	for _, class := range []string{dispatch.FailureCredentialBanned, dispatch.FailureQuotaExhausted} {
		t.Run(class, func(t *testing.T) {
			db := dispatchDB(t)
			store := dispatch.NewStore(db)
			a := dispatchTarget("a", 1)
			cfg := failureConfig(t, []dispatch.Target{a}, false)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			_, err := store.Observe(t.Context(), "core", failureEvent(t, db, a, "first", class))
			require.NoError(t, err)
			a.CredentialRevision++
			a.QuotaRef = "new-quota"
			cfg.ConfigRevision++
			cfg.Targets = []dispatch.Target{a}
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			require.Zero(t, failureSnapshot(t, db, store, cfg).Candidates[0].EffectiveWeight)
			cfg.ConfigRevision++
			cfg.Targets = nil
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			a.Ref = "replacement"
			cfg.ConfigRevision++
			cfg.Targets = []dispatch.Target{a}
			require.NoError(t, dispatch.NewStore(db).SyncRuntimeConfig(t.Context(), cfg))
			for _, c := range failureSnapshot(t, db, dispatch.NewStore(db), cfg).Candidates {
				require.Zero(t, c.EffectiveWeight)
			}
			holds, err := store.HealthHolds(t.Context(), "core", cfg.ConfigRevision)
			require.NoError(t, err)
			require.Len(t, holds, 1)
			require.EqualValues(t, 1, holds[0].Revision)
			require.Len(t, holds[0].Targets, 2)
		})
	}
}

func TestDispatchFailureQuotaConfigCannotReassignHeldDomain(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	a.QuotaRef = "quota-x"
	cfg := failureConfig(t, []dispatch.Target{a}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	_, err := store.Observe(t.Context(), "core", failureEvent(t, db, a, "first", dispatch.FailureQuotaExhausted))
	require.NoError(t, err)
	for _, replacement := range []string{"a", "new-ref"} {
		changed := cfg
		changed.ConfigRevision++
		target := a
		target.Ref, target.QuotaRef = replacement, "quota-y"
		changed.Targets = []dispatch.Target{target}
		require.ErrorContains(t, store.SyncRuntimeConfig(t.Context(), changed), "reassignment")
	}
	changed := cfg
	changed.Targets = nil
	changed.ConfigRevision++
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), changed))
	a.Ref, a.QuotaRef = "new-ref", "quota-y"
	changed.Targets = []dispatch.Target{a}
	changed.ConfigRevision++
	require.ErrorContains(t, store.SyncRuntimeConfig(t.Context(), changed), "reassignment", "retained disabled refs must enforce the lock")
	changed.ConfigRevision--
	a.QuotaRef = "quota-x"
	changed.Targets = []dispatch.Target{a}
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), changed), dispatch.ErrConfigRevision, "same revision metadata conflicts")
}

func TestDispatchFailureReleaseFencesAndTwoProbeRecovery(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	cfg := failureConfig(t, []dispatch.Target{a}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	old, err := store.ClaimProbe(t.Context(), "core", "a", "old-owner", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, old)
	o := failureEvent(t, db, a, "first", dispatch.FailureCredentialBanned)
	_, err = store.Observe(t.Context(), "core", o)
	require.NoError(t, err)
	probe := dispatchObservation(t, db, a, old.AttemptID, dispatch.Success)
	require.ErrorIs(t, store.FinishProbe(t.Context(), *old, probe), dispatch.ErrProbeLease)
	holds, err := store.HealthHolds(t.Context(), "core", 1)
	require.NoError(t, err)
	request := releaseRequest(holds[0], cfg, "release-1")
	bad := request
	bad.HoldRevision++
	_, err = store.ReleaseHealthHold(t.Context(), bad)
	require.ErrorIs(t, err, dispatch.ErrHealthHoldRevision)
	bad = request
	bad.ConfigRevision++
	_, err = store.ReleaseHealthHold(t.Context(), bad)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	result, err := store.ReleaseHealthHold(t.Context(), request)
	require.NoError(t, err)
	require.False(t, result.Idempotent)
	require.False(t, result.Hold.Active)
	require.EqualValues(t, 2, result.Hold.Revision)
	require.NotNil(t, result.Hold.LastRelease)
	require.Zero(t, failureSnapshot(t, db, store, cfg).Candidates[0].EffectiveWeight)
	require.ErrorIs(t, store.FinishProbe(t.Context(), *old, probe), dispatch.ErrProbeLease)
	inserted, err := store.Observe(t.Context(), "core", o)
	require.NoError(t, err)
	require.False(t, inserted, "old deduplicated event must not relock")
	for n := range 2 {
		// 数据库夹具推进探针 slot/cooldown，不等待真实 30 秒。
		dispatchExec(t, db, `UPDATE dispatch.probes SET slot=slot-1000,lease_until=clock_timestamp()-interval '1 second',reserved_at=clock_timestamp()-interval '2 seconds' WHERE core_instance='core'`)
		dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health,'{cool_until}',to_jsonb(clock_timestamp()-interval '1 second')) WHERE core_instance='core'`)
		lease, err := store.ClaimProbe(t.Context(), "core", "a", fmt.Sprintf("owner-%d", n), cfg.ProbeLimits())
		require.NoError(t, err)
		require.NotNil(t, lease)
		observation := dispatchObservation(t, db, a, lease.AttemptID, dispatch.Success)
		require.NoError(t, store.FinishProbe(t.Context(), *lease, observation))
		snapshot := failureSnapshot(t, db, store, cfg)
		if n == 0 {
			require.Zero(t, snapshot.Candidates[0].EffectiveWeight)
		} else {
			require.EqualValues(t, 10, snapshot.Candidates[0].EffectiveWeight)
		}
	}
	_, err = store.Observe(t.Context(), "core", failureEvent(t, db, a, "new-event", dispatch.FailureCredentialBanned))
	require.NoError(t, err)
	require.Zero(t, failureSnapshot(t, db, store, cfg).Candidates[0].EffectiveWeight)
	holds, err = store.HealthHolds(t.Context(), "core", 1)
	require.NoError(t, err)
	require.EqualValues(t, 3, holds[0].Revision)
	require.NotNil(t, holds[0].LastRelease, "relocked hold retains the manual release history")
	repeated, err := dispatch.NewStore(db).ReleaseHealthHold(t.Context(), request)
	require.NoError(t, err)
	require.True(t, repeated.Idempotent)
	require.Equal(t, result.Audit.ID, repeated.Audit.ID)
	require.False(t, repeated.Hold.Active, "idempotent response represents the original operation, not the newly relocked hold")
	_, err = store.ReleaseHealthHold(t.Context(), releaseRequest(result.Hold, cfg, "stale"))
	require.ErrorIs(t, err, dispatch.ErrHealthHoldRevision)
}

func TestDispatchFailureIntersectingHoldsAndLegacyAuth(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	alias := a
	alias.Ref = "alias"
	cfg := failureConfig(t, []dispatch.Target{a, alias}, true)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	for n, class := range []string{dispatch.FailureCredentialBanned, dispatch.FailureCapabilityDenied} {
		_, err := store.Observe(t.Context(), "core", failureEvent(t, db, a, fmt.Sprintf("failure-%d", n), class))
		require.NoError(t, err)
	}
	_, err := store.Observe(t.Context(), "core", dispatchObservation(t, db, a, "legacy-auth", dispatch.UpstreamAuth))
	require.NoError(t, err)
	holds, err := store.HealthHolds(t.Context(), "core", 1)
	require.NoError(t, err)
	require.Len(t, holds, 2)
	for n, hold := range holds {
		_, err := store.ReleaseHealthHold(t.Context(), releaseRequest(hold, cfg, fmt.Sprintf("release-%d", n)))
		require.NoError(t, err)
		for _, c := range failureSnapshot(t, db, store, cfg).Candidates {
			require.Zero(t, c.EffectiveWeight)
			require.True(t, dispatchHealth(t, db, "core", c.Ref).AuthBlocked)
		}
	}
	_, err = store.Observe(t.Context(), "core", dispatchObservation(t, db, a, "bare-429", dispatch.UpstreamRateLimit))
	require.NoError(t, err)
	require.Equal(t, 2, dispatchCount(t, db, `SELECT count(*) FROM dispatch.health_holds`), "bare 429 creates no persistent hold")
	cfg.ConfigRevision++
	for i := range cfg.Targets {
		cfg.Targets[i].CredentialRevision++
	}
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	for _, c := range failureSnapshot(t, db, store, cfg).Candidates {
		require.Zero(t, c.EffectiveWeight, "rotation still requires recovery probes")
		require.False(t, dispatchHealth(t, db, "core", c.Ref).AuthBlocked, "legacy credential revision rotation behavior is preserved")
	}
}

func TestDispatchFailureReleaseConcurrencyIdempotencyAndAuditRollback(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	cfg := failureConfig(t, []dispatch.Target{a}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for n := range 8 {
		o := failureEvent(t, db, a, fmt.Sprintf("failure-%d", n), dispatch.FailureQuotaExhausted)
		wg.Go(func() { _, err := dispatch.NewStore(db).Observe(t.Context(), "core", o); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	holds, err := store.HealthHolds(t.Context(), "core", 1)
	require.NoError(t, err)
	require.EqualValues(t, 8, holds[0].Revision)
	request := releaseRequest(holds[0], cfg, "release")
	before := dispatchHealth(t, db, "core", "a")
	dispatchExec(t, db, `CREATE FUNCTION dispatch.fail_release_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='release' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$;
	 CREATE TRIGGER reject_release_audit BEFORE INSERT ON dispatch.health_hold_audit FOR EACH ROW EXECUTE FUNCTION dispatch.fail_release_audit()`)
	_, err = store.ReleaseHealthHold(t.Context(), request)
	require.ErrorContains(t, err, "synthetic audit failure")
	require.Equal(t, before, dispatchHealth(t, db, "core", "a"))
	require.Equal(t, 8, dispatchCount(t, db, `SELECT count(*) FROM dispatch.health_hold_audit`))
	holds, err = store.HealthHolds(t.Context(), "core", 1)
	require.NoError(t, err)
	require.True(t, holds[0].Active)
	require.EqualValues(t, 8, holds[0].Revision)
	dispatchExec(t, db, `DROP TRIGGER reject_release_audit ON dispatch.health_hold_audit`)
	results := make(chan dispatch.HoldReleaseResult, 8)
	errs = make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			result, err := dispatch.NewStore(db).ReleaseHealthHold(t.Context(), request)
			results <- result
			errs <- err
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	count := 0
	for result := range results {
		if !result.Idempotent {
			count++
		}
		require.EqualValues(t, 9, result.Hold.Revision)
	}
	require.Equal(t, 1, count)
	request.Reason = "different evidence"
	_, err = store.ReleaseHealthHold(t.Context(), request)
	require.ErrorIs(t, err, dispatch.ErrHealthHoldOperationConflict)
	require.Equal(t, 9, dispatchCount(t, db, `SELECT count(*) FROM dispatch.health_hold_audit`))
	for _, query := range []string{
		`UPDATE dispatch.health_hold_audit SET reason='overwrite'`, `DELETE FROM dispatch.health_hold_audit`, `TRUNCATE dispatch.health_hold_audit`,
		`UPDATE dispatch.health_holds SET scope='{}'::jsonb,revision=revision+1`, `DELETE FROM dispatch.health_holds`,
	} {
		_, err := db.ExecContext(t.Context(), query)
		require.Error(t, err)
	}
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM information_schema.role_table_grants
	 WHERE table_schema='dispatch' AND table_name IN ('health_holds','health_hold_audit') AND grantee='PUBLIC'`))
}

func TestDispatchFailureMigrationPreservesLegacyRowsAndLedger(t *testing.T) {
	db := dispatchDB(t)
	original, err := os.ReadFile("../custom/dispatch/migrations/001_dispatch.sql")
	require.NoError(t, err)
	dispatchExec(t, db, `DROP SCHEMA dispatch CASCADE; CREATE SCHEMA dispatch`)
	dispatchExec(t, db, string(original))
	checksum := fmt.Sprintf("%x", sha256.Sum256(original))
	dispatchExec(t, db, `CREATE TABLE dispatch.schema_migrations(version TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`)
	dispatchExec(t, db, `INSERT INTO dispatch.schema_migrations(version,checksum) VALUES ('001',$1)`, checksum)
	a := dispatchTarget("legacy", 1)
	metadata, err := json.Marshal(a)
	require.NoError(t, err)
	health, err := json.Marshal(dispatch.InitialHealth(a, dispatchNow(t, db)))
	require.NoError(t, err)
	dispatchExec(t, db, `INSERT INTO dispatch.cores(core_instance,config_revision) VALUES ('core',1)`)
	dispatchExec(t, db, `INSERT INTO dispatch.targets VALUES ('core','legacy',$1,$2,7)`, string(metadata), string(health))
	dispatchExec(t, db, `INSERT INTO dispatch.observations VALUES ('core','old-event','{}',clock_timestamp())`)
	require.NoError(t, dispatch.ApplySchema(t.Context(), db))
	require.NoError(t, dispatch.ApplySchema(t.Context(), db))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.targets WHERE metadata=$1::jsonb AND health=$2::jsonb AND generation=7`, string(metadata), string(health)))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.schema_migrations WHERE version='001' AND checksum=$1`, checksum))
	require.Equal(t, 5, dispatchCount(t, db, `SELECT count(*) FROM dispatch.schema_migrations`))
	require.Equal(t, 5, dispatchCount(t, db, `SELECT count(*) FROM dispatch.schema_migrations WHERE version IN ('001','003','004','005','006')`))
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema='dispatch' AND table_name='quality_results'`))
	var currentChecksum string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT checksum FROM dispatch.schema_migrations WHERE version='003'`).Scan(&currentChecksum))
	dispatchExec(t, db, `UPDATE dispatch.schema_migrations SET checksum='tampered' WHERE version='003'`)
	require.ErrorContains(t, dispatch.ApplySchema(t.Context(), db), "checksum")
	dispatchExec(t, db, `UPDATE dispatch.schema_migrations SET checksum=$1 WHERE version='003'`, currentChecksum)
	require.NoError(t, dispatch.ApplyQualitySchema(t.Context(), db))
	require.NoError(t, dispatch.ApplySchema(t.Context(), db), "quality 002 remains in its independent ledger")
}

func TestDispatchFailureRestrictedReportAndReleasePermissions(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	cfg := failureConfig(t, []dispatch.Target{a}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	_, err := store.Observe(t.Context(), "core", failureEvent(t, db, a, "failure", dispatch.FailureCapabilityDenied))
	require.NoError(t, err)
	role := "hold_reader_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := "synthetic-" + uuid.NewString()
	dispatchExec(t, db, `CREATE ROLE `+pq.QuoteIdentifier(role)+` LOGIN PASSWORD `+pq.QuoteLiteral(password))
	dispatchExec(t, db, `GRANT USAGE ON SCHEMA dispatch TO `+pq.QuoteIdentifier(role)+`;
	 GRANT SELECT ON dispatch.cores,dispatch.targets,dispatch.health_holds,dispatch.health_hold_audit TO `+pq.QuoteIdentifier(role))
	dsn, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	dsn.User = url.UserPassword(role, password)
	restricted, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, restricted.Close())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, `DROP OWNED BY `+pq.QuoteIdentifier(role)+`; DROP ROLE `+pq.QuoteIdentifier(role))
		require.NoError(t, err)
	})
	restrictedStore := dispatch.NewStore(restricted)
	holds, err := restrictedStore.HealthHolds(t.Context(), "core", 1)
	require.NoError(t, err, "report requires no write, owner or migration privilege")
	require.Len(t, holds, 1)
	request := releaseRequest(holds[0], cfg, "release")
	_, err = restrictedStore.ReleaseHealthHold(t.Context(), request)
	require.Error(t, err)
	dispatchExec(t, db, `GRANT UPDATE (active,revision,released_at,config_revision) ON dispatch.health_holds TO `+pq.QuoteIdentifier(role)+`;
	 GRANT UPDATE (health,generation) ON dispatch.targets TO `+pq.QuoteIdentifier(role)+`;
	 GRANT INSERT ON dispatch.health_hold_audit TO `+pq.QuoteIdentifier(role))
	result, err := restrictedStore.ReleaseHealthHold(t.Context(), request)
	require.NoError(t, err)
	require.False(t, result.Hold.Active)
	_, err = restrictedStore.HealthHolds(t.Context(), "core", 2)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	for _, query := range []string{`UPDATE dispatch.health_hold_audit SET actor='other'`, `DELETE FROM dispatch.health_hold_audit`,
		`UPDATE dispatch.health_holds SET scope='{}'::jsonb`, `SELECT credentials FROM public.accounts LIMIT 1`} {
		_, err := restricted.ExecContext(t.Context(), query)
		require.Error(t, err)
	}
}

func TestDispatchFailureReadErrorPreservesSnapshotAndObservation(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	a := dispatchTarget("a", 1)
	cfg := failureConfig(t, []dispatch.Target{a}, false)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	before := failureSnapshot(t, db, store, cfg)
	body, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	dispatchExec(t, db, `ALTER TABLE dispatch.health_holds RENAME TO unavailable_health_holds`)
	_, err = store.PublishRuntime(t.Context(), cfg, nil)
	require.Error(t, err)
	after, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	require.Equal(t, body, after)
	require.EqualValues(t, before.Revision, dispatchCount(t, db, `SELECT revision FROM dispatch.cores WHERE core_instance='core'`))
	_, err = store.Observe(t.Context(), "core", failureEvent(t, db, a, "failure", dispatch.FailureQuotaExhausted))
	require.Error(t, err)
	require.Equal(t, 0, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
	_, err = store.ClaimProbe(t.Context(), "core", "a", "owner", cfg.ProbeLimits())
	require.Error(t, err)
}
