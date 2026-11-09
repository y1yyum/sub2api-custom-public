//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type passiveFactWriter interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertTimedPassiveFact(t *testing.T, writer passiveFactWriter, producer *custombridge.TelemetryProducer, cfg dispatch.Config, outcome dispatch.Outcome, retry int) time.Time {
	t.Helper()
	target := cfg.Targets[0]
	id := uuid.NewString()
	var at time.Time
	err := writer.QueryRowContext(t.Context(), `INSERT INTO bridge.dispatch_observation
	 (event_id,core_instance,target_ref,config_revision,account_id,credential_ref,credential_revision,
	 provider_ref,pool_ref,policy_revision,model,region,outcome,provider_evidence,retry_after_seconds,
	 latency_millis,observed_at,internal_key_id,attempt_id,core_request_id,producer_generation)
	 VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,TRUE,$14,10,clock_timestamp(),1,$1::text,$1::text,$15)
	 RETURNING observed_at`, id, cfg.CoreInstance, target.Ref, cfg.ConfigRevision, target.AccountID, target.CredentialRef,
		target.CredentialRevision, target.ProviderRef, target.PoolRef, target.PolicyRevision, target.Model, target.Region,
		string(outcome), retry, producer.Source().ProducerGeneration).Scan(&at)
	require.NoError(t, err)
	return at.UTC()
}

func TestDispatchPassiveStartupFactsAffectHealth(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	var last time.Time
	for range 5 {
		last = insertTimedPassiveFact(t, db, producer, cfg, dispatch.UpstreamFailure, 0)
	}
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	initial := dispatchHealth(t, db, "core", "a")
	require.True(t, last.Before(initial.UpdatedAt))
	reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), "core", 1)
	require.NoError(t, err)
	batch, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, batch, 5)
	count, err := store.ObservePassive(t.Context(), "core", 1, batch)
	require.NoError(t, err)
	require.Equal(t, 5, count)
	health := dispatchHealth(t, db, "core", "a")
	require.Equal(t, dispatch.Tripped, health.State)
	require.Equal(t, 5, health.Samples)
	require.Equal(t, 5, health.Failures)
	require.Equal(t, dispatch.Tripped, dispatchHealth(t, db, "core", "alias").State)
	var original time.Time
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT (payload->>'at')::timestamptz FROM dispatch.observations WHERE event_id=$1`, batch[0].EventID).Scan(&original))
	require.True(t, original.Equal(batch[0].At), "audit retains source event time")
	count, err = store.ObservePassive(t.Context(), "core", 1, batch)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, health, dispatchHealth(t, db, "core", "a"))
}

func TestDispatchPassiveLateCommitUsesConfirmationTime(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outcome      dispatch.Outcome
		facts, retry int
	}{
		{"five_failures", dispatch.UpstreamFailure, 5, 0}, {"rate_limit", dispatch.UpstreamRateLimit, 1, 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dispatchDB(t)
			cfg, producer := passiveConfig(t, db)
			store := dispatch.NewStore(db)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), "core", 1)
			require.NoError(t, err)
			pending, err := db.BeginTx(t.Context(), nil)
			require.NoError(t, err)
			defer func() { _ = pending.Rollback() }()
			var last time.Time
			for range tc.facts {
				last = insertTimedPassiveFact(t, pending, producer, cfg, tc.outcome, tc.retry)
			}
			successAt := insertTimedPassiveFact(t, db, producer, cfg, dispatch.Success, 0)
			require.True(t, last.Before(successAt))
			first, err := reader.Read(t.Context(), cfg)
			require.NoError(t, err)
			require.Len(t, first, 1)
			count, err := store.ObservePassive(t.Context(), "core", 1, first)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			require.NoError(t, pending.Commit())
			second, err := reader.Read(t.Context(), cfg)
			require.NoError(t, err)
			require.Len(t, second, tc.facts+1)
			count, err = store.ObservePassive(t.Context(), "core", 1, second)
			require.NoError(t, err)
			require.Equal(t, tc.facts, count)
			health := dispatchHealth(t, db, "core", "a")
			require.Equal(t, dispatch.Tripped, health.State)
			require.Equal(t, dispatch.Tripped, dispatchHealth(t, db, "core", "alias").State)
			if tc.retry > 0 {
				require.True(t, health.CoolUntil.After(dispatchNow(t, db).Add(100*time.Second)))
			}
			count, err = store.ObservePassive(t.Context(), "core", 1, second)
			require.NoError(t, err)
			require.Zero(t, count)
			require.Equal(t, health, dispatchHealth(t, db, "core", "a"), "repeat imports must not extend cooldown")
		})
	}
}

func TestDispatchPassiveLateSuccessCannotRecoverTrip(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), "core", 1)
	require.NoError(t, err)
	pending, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = pending.Rollback() }()
	insertTimedPassiveFact(t, pending, producer, cfg, dispatch.Success, 0)
	for range 5 {
		insertTimedPassiveFact(t, db, producer, cfg, dispatch.UpstreamFailure, 0)
	}
	first, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	count, err := store.ObservePassive(t.Context(), "core", 1, first)
	require.NoError(t, err)
	require.Equal(t, 5, count)
	before := dispatchHealth(t, db, "core", "a")
	require.Equal(t, dispatch.Tripped, before.State)
	require.NoError(t, pending.Commit())
	second, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	count, err = store.ObservePassive(t.Context(), "core", 1, second)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	after := dispatchHealth(t, db, "core", "a")
	require.Equal(t, dispatch.Tripped, after.State)
	require.Equal(t, 6, after.Samples)
	require.Equal(t, 5, after.Failures)
	require.False(t, after.UpdatedAt.Before(before.UpdatedAt))
}

func TestDispatchPassiveDoesNotDropLeasedProbe(t *testing.T) {
	for _, tc := range []struct {
		name           string
		probe, passive dispatch.Outcome
		state          dispatch.State
		successes      int
		reason         string
	}{
		{"success", dispatch.Success, dispatch.Success, dispatch.Degraded, 2, ""},
		{"unknown", dispatch.Unknown, dispatch.Success, dispatch.Tripped, 0, "probe_unattributed"},
		{"passive_failure_success_probe", dispatch.Success, dispatch.UpstreamFailure, dispatch.Tripped, 0, "upstream_failure"},
		{"passive_failure_unknown_probe", dispatch.Unknown, dispatch.UpstreamFailure, dispatch.Tripped, 0, "upstream_failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dispatchDB(t)
			cfg, producer := passiveConfig(t, db)
			cfg.Probe = &dispatch.ProbeConfig{LeaseSeconds: 60, GlobalConcurrent: 10, ProviderRPM: 100, DailyRequests: 1000}
			store := dispatch.NewStore(db)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			target := cfg.Targets[0]
			_, err := store.Observe(t.Context(), "core", dispatchObservation(t, db, target, "initial-trip", dispatch.UpstreamRateLimit))
			require.NoError(t, err)
			dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health,'{cool_until}',to_jsonb(clock_timestamp()-interval '1 second')) WHERE core_instance='core'`)
			first, err := store.ClaimProbe(t.Context(), "core", target.Ref, "first-owner", cfg.ProbeLimits())
			require.NoError(t, err)
			require.NotNil(t, first)
			require.NoError(t, store.FinishProbe(t.Context(), *first, dispatchObservation(t, db, target, first.AttemptID, dispatch.Success)))
			require.Equal(t, 1, dispatchHealth(t, db, "core", target.Ref).ProbeSuccesses)
			// Only advance synthetic cooldown/slot eligibility; all lease fences remain real.
			dispatchExec(t, db, `UPDATE dispatch.probes SET slot=slot-10 WHERE core_instance='core'`)
			dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health,'{cool_until}',to_jsonb(clock_timestamp()-interval '1 second')) WHERE core_instance='core'`)
			sourceAt := insertTimedPassiveFact(t, db, producer, cfg, tc.passive, 0)
			second, err := store.ClaimProbe(t.Context(), "core", target.Ref, "second-owner", cfg.ProbeLimits())
			require.NoError(t, err)
			require.NotNil(t, second)
			result := dispatchObservation(t, db, target, second.AttemptID, tc.probe)
			result.ProviderEvidence = tc.probe == dispatch.Success
			require.True(t, sourceAt.Before(result.At))
			reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), "core", 1)
			require.NoError(t, err)
			batch, err := reader.Read(t.Context(), cfg)
			require.NoError(t, err)
			require.Len(t, batch, 1)
			count, err := store.ObservePassive(t.Context(), "core", 1, batch)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			confirmed := dispatchHealth(t, db, "core", target.Ref)
			if tc.passive == dispatch.UpstreamFailure {
				require.Equal(t, dispatch.Tripped, confirmed.State)
				require.Zero(t, confirmed.ProbeSuccesses)
			} else {
				require.Equal(t, dispatch.Probing, confirmed.State)
			}
			require.True(t, result.At.Before(confirmed.UpdatedAt))
			require.NoError(t, store.FinishProbe(t.Context(), *second, result))
			require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.probes WHERE token=$1 AND finished_at IS NOT NULL`, second.Token))
			var source dispatch.Health
			for _, ref := range []string{target.Ref, cfg.Targets[1].Ref} {
				health := dispatchHealth(t, db, "core", ref)
				require.Equal(t, tc.state, health.State)
				require.Equal(t, tc.successes, health.ProbeSuccesses)
				if tc.reason != "" {
					require.Equal(t, tc.reason, health.Reason)
				}
				if ref == target.Ref {
					source = health
				}
			}
			var original time.Time
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT (payload->>'at')::timestamptz FROM dispatch.observations WHERE event_id=$1`, result.EventID).Scan(&original))
			require.True(t, original.Equal(result.At))
			require.ErrorIs(t, store.FinishProbe(t.Context(), *second, result), dispatch.ErrProbeLease)
			require.Equal(t, source, dispatchHealth(t, db, "core", target.Ref))
		})
	}
}

func TestDispatchPassiveFailureResetsRecoveryBetweenProbes(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	cfg.Probe = &dispatch.ProbeConfig{LeaseSeconds: 60, GlobalConcurrent: 10, ProviderRPM: 100, DailyRequests: 1000}
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	target := cfg.Targets[0]
	_, err := store.Observe(t.Context(), "core", dispatchObservation(t, db, target, "initial-trip", dispatch.UpstreamRateLimit))
	require.NoError(t, err)
	dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health,'{cool_until}',to_jsonb(clock_timestamp()-interval '1 second')) WHERE core_instance='core'`)
	first, err := store.ClaimProbe(t.Context(), "core", target.Ref, "first-owner", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NoError(t, store.FinishProbe(t.Context(), *first, dispatchObservation(t, db, target, first.AttemptID, dispatch.Success)))
	require.Equal(t, 1, dispatchHealth(t, db, "core", target.Ref).ProbeSuccesses)
	insertTimedPassiveFact(t, db, producer, cfg, dispatch.UpstreamFailure, 0)
	reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), "core", 1)
	require.NoError(t, err)
	batch, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	count, err := store.ObservePassive(t.Context(), "core", 1, batch)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	for _, ref := range []string{target.Ref, cfg.Targets[1].Ref} {
		health := dispatchHealth(t, db, "core", ref)
		require.Equal(t, dispatch.Tripped, health.State)
		require.Zero(t, health.ProbeSuccesses)
	}
	dispatchExec(t, db, `UPDATE dispatch.probes SET slot=slot-10 WHERE core_instance='core'`)
	dispatchExec(t, db, `UPDATE dispatch.targets SET health=jsonb_set(health,'{cool_until}',to_jsonb(clock_timestamp()-interval '1 second')) WHERE core_instance='core'`)
	second, err := store.ClaimProbe(t.Context(), "core", target.Ref, "second-owner", cfg.ProbeLimits())
	require.NoError(t, err)
	require.NotNil(t, second)
	require.NoError(t, store.FinishProbe(t.Context(), *second, dispatchObservation(t, db, target, second.AttemptID, dispatch.Success)))
	for _, ref := range []string{target.Ref, cfg.Targets[1].Ref} {
		health := dispatchHealth(t, db, "core", ref)
		require.Equal(t, dispatch.Tripped, health.State)
		require.Equal(t, 1, health.ProbeSuccesses)
	}
}
