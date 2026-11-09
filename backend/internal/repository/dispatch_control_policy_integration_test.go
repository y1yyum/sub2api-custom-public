//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func controlPolicyFixture(t *testing.T, db *sql.DB) (dispatch.Config, []dispatch.PassiveEvent) {
	t.Helper()
	cfg, _ := passiveConfig(t, db)
	cfg.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}
	cfg.HealthWindow = &dispatch.HealthWindowConfig{}
	target := cfg.Targets[0]
	events := make([]dispatch.PassiveEvent, 20)
	for n := range events {
		outcome, timedOut := dispatch.Success, false
		if n%5 == 4 {
			outcome, timedOut = dispatch.UpstreamFailure, true
		}
		events[n] = dispatch.PassiveEvent{Observation: dispatch.Observation{
			EventID: uuid.NewString(), TargetRef: target.Ref, AccountID: target.AccountID,
			CredentialRevision: target.CredentialRevision, Outcome: outcome,
			ProviderEvidence: true, TimedOut: timedOut, At: dispatchNow(t, db)},
			CoreInstance: cfg.CoreInstance, ConfigRevision: cfg.ConfigRevision,
			CredentialRef: target.CredentialRef, ProviderRef: target.ProviderRef,
			PoolRef: target.PoolRef, PolicyRevision: target.PolicyRevision,
			Model: target.Model, Region: target.Region}
	}
	return cfg, events
}

func TestDispatchControlPolicyWeightsFencingAndDisable(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg, events := controlPolicyFixture(t, db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	count, err := store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, events)
	require.NoError(t, err)
	require.Equal(t, 20, count)
	count, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, events)
	require.NoError(t, err)
	require.Zero(t, count)
	reports, err := store.HealthWindowReports(t.Context(), cfg.CoreInstance, cfg.ConfigRevision)
	require.NoError(t, err)
	require.True(t, reports[0].PolicyEnabled)
	require.NotNil(t, reports[0].Window)
	require.Equal(t, 20, reports[0].Window.Hot.Samples)
	require.Equal(t, 4, reports[0].Window.Hot.Failures)
	require.Equal(t, 4, reports[0].Window.Hot.Timeouts)
	require.Equal(t, 20, reports[0].Window.Hot.PassiveSamples)
	require.EqualValues(t, 840, reports[0].Window.Hot.Factor)
	load := dispatch.LoadSample{ObservedAt: dispatchNow(t, db), Accounts: map[int64]int{1: 0}}
	snapshot, err := store.PublishRuntime(t.Context(), cfg, &load)
	require.NoError(t, err)
	require.EqualValues(t, 84, snapshot.Candidates[0].EffectiveWeight, "H is applied exactly once after routing")
	changed := cfg
	changed.HealthWindow = nil
	require.ErrorIs(t, store.SyncRuntimeConfig(t.Context(), changed), dispatch.ErrConfigRevision)
	_, err = store.PublishRuntime(t.Context(), changed, &load)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	_, err = store.Publish(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, time.Second)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	changed.ConfigRevision++
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), changed))
	_, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, events)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	reports, err = store.HealthWindowReports(t.Context(), changed.CoreInstance, changed.ConfigRevision)
	require.NoError(t, err)
	require.False(t, reports[0].PolicyEnabled)
	require.Nil(t, reports[0].Window)
	require.Nil(t, dispatchHealth(t, db, cfg.CoreInstance, "a").Rolling)
	load.ObservedAt = dispatchNow(t, db)
	snapshot, err = store.PublishRuntime(t.Context(), changed, &load)
	require.NoError(t, err)
	require.EqualValues(t, 100, snapshot.Candidates[0].EffectiveWeight)
}

func TestDispatchControlPolicyReportRequiresOnlyReadPrivileges(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg, events := controlPolicyFixture(t, db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	_, err := store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, events)
	require.NoError(t, err)
	before := dispatchHealth(t, db, cfg.CoreInstance, "a")
	role := "health_report_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := "synthetic-" + uuid.NewString()
	dispatchExec(t, db, `CREATE ROLE `+pq.QuoteIdentifier(role)+` LOGIN PASSWORD `+pq.QuoteLiteral(password))
	dispatchExec(t, db, `GRANT USAGE ON SCHEMA dispatch TO `+pq.QuoteIdentifier(role)+`;
	 GRANT SELECT ON dispatch.cores,dispatch.targets TO `+pq.QuoteIdentifier(role))
	dsn, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	dsn.User = url.UserPassword(role, password)
	readerDB, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, readerDB.Close())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, `DROP OWNED BY `+pq.QuoteIdentifier(role)+`; DROP ROLE `+pq.QuoteIdentifier(role))
		require.NoError(t, err)
	})
	reports, err := dispatch.NewStore(readerDB).HealthWindowReports(t.Context(), cfg.CoreInstance, cfg.ConfigRevision)
	require.NoError(t, err)
	require.Len(t, reports, 2)
	require.True(t, reports[0].PolicyEnabled)
	require.Equal(t, before, dispatchHealth(t, db, cfg.CoreInstance, "a"), "reporting must not advance persisted state")
	_, err = dispatch.NewStore(readerDB).HealthWindowReports(t.Context(), cfg.CoreInstance, cfg.ConfigRevision+1)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	encoded, err := json.Marshal(reports)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), password)
}

func TestDispatchControlPolicySwitchCannotReuseLegacyRecoveryWindow(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg, events := controlPolicyFixture(t, db)
	cfg.HealthWindow = nil
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	health := dispatchHealth(t, db, cfg.CoreInstance, "a")
	health.State, health.Reason = dispatch.Degraded, "degraded_window"
	health.WindowAt = dispatchNow(t, db).Add(-2 * time.Minute)
	health.Samples, health.Failures, health.GoodWindows = 20, 0, 1
	raw, err := json.Marshal(health)
	require.NoError(t, err)
	dispatchExec(t, db, `UPDATE dispatch.targets SET health=$3 WHERE core_instance=$1 AND target_ref=$2`, cfg.CoreInstance, "a", string(raw))
	cfg.ConfigRevision++
	cfg.HealthWindow = &dispatch.HealthWindowConfig{}
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	cfg.ConfigRevision++
	cfg.HealthWindow = nil
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	event := events[0]
	event.ConfigRevision, event.At = cfg.ConfigRevision, dispatchNow(t, db)
	_, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, []dispatch.PassiveEvent{event})
	require.NoError(t, err)
	require.Equal(t, dispatch.Degraded, dispatchHealth(t, db, cfg.CoreInstance, "a").State,
		"one new success cannot combine with an obsolete pre-policy good window to restore routing")
}
