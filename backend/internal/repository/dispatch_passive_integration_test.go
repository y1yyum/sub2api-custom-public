//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func passiveReadOnlyDB(t *testing.T, db *sql.DB) *sql.DB {
	t.Helper()
	role := "dispatch_obs_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := "synthetic-" + uuid.NewString()
	_, err := db.ExecContext(t.Context(), `CREATE ROLE `+pq.QuoteIdentifier(role)+` LOGIN PASSWORD `+pq.QuoteLiteral(password))
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `GRANT USAGE ON SCHEMA bridge TO `+pq.QuoteIdentifier(role)+`; GRANT SELECT ON bridge.dispatch_telemetry_source,bridge.dispatch_observation TO `+pq.QuoteIdentifier(role))
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
		_, err := db.ExecContext(ctx, `DROP OWNED BY `+pq.QuoteIdentifier(role)+`; DROP ROLE `+pq.QuoteIdentifier(role))
		require.NoError(t, err)
	})
	return reader
}

func passiveConfig(t *testing.T, db *sql.DB) (dispatch.Config, *custombridge.TelemetryProducer) {
	t.Helper()
	require.NoError(t, custombridge.ApplyTelemetrySchema(t.Context(), db))
	_, err := db.ExecContext(t.Context(), `TRUNCATE bridge.dispatch_observation,bridge.dispatch_telemetry_source`)
	require.NoError(t, err)
	credentials, err := custombridge.TelemetryCredentialSHA256(map[string]any{"api_key": "synthetic-passive-test"})
	require.NoError(t, err)
	a := dispatchTarget("a", 1)
	b := a
	b.Ref = "alias"
	b.PoolRef = "other-pool"
	config := custombridge.TelemetryConfig{SchemaVersion: 1, CoreInstance: "core", ConfigRevision: 1, Targets: []custombridge.TelemetryTarget{
		{TargetRef: a.Ref, AccountID: a.AccountID, CredentialRef: a.CredentialRef, CredentialRevision: 1, CredentialSHA256: credentials,
			ProviderRef: a.ProviderRef, PoolRef: a.PoolRef, PolicyRevision: a.PolicyRevision, Model: a.Model, Region: a.Region},
	}}
	producer, err := custombridge.ActivateTelemetry(t.Context(), db, config, 1800)
	require.NoError(t, err)
	cfg := dispatch.Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, Targets: []dispatch.Target{a, b},
		SnapshotTTLSeconds: 30, PollIntervalMilliseconds: 1000, SnapshotFile: t.TempDir() + "/snapshot.json",
		Telemetry: &dispatch.TelemetryConfig{SlotTTLSeconds: 1800, LoadMaxAgeSeconds: 5, MaxObservations: 100}}
	return cfg, producer
}

func insertPassiveEvent(t *testing.T, db *sql.DB, producer *custombridge.TelemetryProducer, cfg dispatch.Config, outcome dispatch.Outcome) dispatch.PassiveEvent {
	t.Helper()
	a := cfg.Targets[0]
	id := uuid.NewString()
	_, err := db.ExecContext(t.Context(), `INSERT INTO bridge.dispatch_observation
	 (event_id,core_instance,target_ref,config_revision,account_id,credential_ref,credential_revision,
	 provider_ref,pool_ref,policy_revision,model,region,outcome,provider_evidence,retry_after_seconds,
	 latency_millis,observed_at,internal_key_id,attempt_id,core_request_id,producer_generation)
	 VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,TRUE,0,10,clock_timestamp(),1,$1::text,$1::text,$14)`,
		id, cfg.CoreInstance, a.Ref, cfg.ConfigRevision, a.AccountID, a.CredentialRef, a.CredentialRevision, a.ProviderRef,
		a.PoolRef, a.PolicyRevision, a.Model, a.Region, string(outcome), producer.Source().ProducerGeneration)
	require.NoError(t, err)
	return dispatch.PassiveEvent{Observation: dispatch.Observation{EventID: id, TargetRef: a.Ref, AccountID: a.AccountID, CredentialRevision: a.CredentialRevision,
		Outcome: outcome, At: dispatchNow(t, db), ProviderEvidence: true, LatencyMillis: 10}, CoreInstance: cfg.CoreInstance, ConfigRevision: cfg.ConfigRevision,
		CredentialRef: a.CredentialRef, ProviderRef: a.ProviderRef, PoolRef: a.PoolRef, PolicyRevision: a.PolicyRevision, Model: a.Model, Region: a.Region}
}

func TestDispatchPassiveRestrictedSourceAndDedup(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	_, err := dispatch.NewPassiveReader(t.Context(), db, "core", 1)
	require.Error(t, err, "source owner cannot masquerade as restricted reader")
	readerDB := passiveReadOnlyDB(t, db)
	reader, err := dispatch.NewPassiveReader(t.Context(), readerDB, "core", 1)
	require.NoError(t, err)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	for range 5 {
		insertPassiveEvent(t, db, producer, cfg, dispatch.UpstreamFailure)
	}
	batch, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, batch, 5)
	count, err := store.ObservePassive(t.Context(), cfg.CoreInstance, 1, batch)
	require.NoError(t, err)
	require.Equal(t, 5, count)
	require.Equal(t, dispatch.Tripped, dispatchHealth(t, db, "core", "a").State)
	require.Equal(t, dispatch.Tripped, dispatchHealth(t, db, "core", "alias").State)
	count, err = dispatch.NewStore(db).ObservePassive(t.Context(), cfg.CoreInstance, 1, batch)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, 5, dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations`))
	changed := append([]dispatch.PassiveEvent(nil), batch...)
	changed[0].LatencyMillis++
	_, err = store.ObservePassive(t.Context(), cfg.CoreInstance, 1, changed)
	require.ErrorIs(t, err, dispatch.ErrObservationConflict)
	wrong := cfg
	limits := *cfg.Telemetry
	limits.SlotTTLSeconds = 900
	wrong.Telemetry = &limits
	_, err = reader.Read(t.Context(), wrong)
	require.Error(t, err, "native Core TTL mismatch must not undercount long slots")
	_, err = readerDB.ExecContext(t.Context(), `UPDATE bridge.dispatch_telemetry_source SET config_revision=2`)
	require.Error(t, err)
	_, err = readerDB.QueryContext(t.Context(), `SELECT credentials FROM accounts LIMIT 1`)
	require.Error(t, err, "source reader must not access provider secrets")
	_, err = db.ExecContext(t.Context(), `UPDATE bridge.dispatch_telemetry_source SET config_revision=2`)
	require.NoError(t, err)
	_, err = reader.Read(t.Context(), cfg)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
}

func TestDispatchPassiveIdentityWindowAndConfigFencing(t *testing.T) {
	db := dispatchDB(t)
	cfg, producer := passiveConfig(t, db)
	reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), "core", 1)
	require.NoError(t, err)
	store := dispatch.NewStore(db)
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	insertPassiveEvent(t, db, producer, cfg, dispatch.UpstreamAuth)
	batch, err := reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	for name, change := range map[string]func(*dispatch.PassiveEvent){
		"core": func(e *dispatch.PassiveEvent) { e.CoreInstance = "other" }, "generation": func(e *dispatch.PassiveEvent) { e.ConfigRevision++ },
		"account": func(e *dispatch.PassiveEvent) { e.AccountID++ }, "credential": func(e *dispatch.PassiveEvent) { e.CredentialRef = "other" },
		"credential_revision": func(e *dispatch.PassiveEvent) { e.CredentialRevision++ }, "provider": func(e *dispatch.PassiveEvent) { e.ProviderRef = "other" },
		"pool": func(e *dispatch.PassiveEvent) { e.PoolRef = "other" }, "policy": func(e *dispatch.PassiveEvent) { e.PolicyRevision = "other" },
		"model": func(e *dispatch.PassiveEvent) { e.Model = "other" }, "region": func(e *dispatch.PassiveEvent) { e.Region = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			e := batch[0]
			change(&e)
			count, err := store.ObservePassive(t.Context(), "core", 1, []dispatch.PassiveEvent{e})
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
	require.Equal(t, dispatch.Healthy, dispatchHealth(t, db, "core", "a").State)
	invalid := batch[0]
	invalid.ProviderEvidence = false
	_, err = store.ObservePassive(t.Context(), "core", 1, []dispatch.PassiveEvent{invalid})
	require.Error(t, err)
	for _, outcome := range []dispatch.Outcome{dispatch.LocalRejection, dispatch.Unknown, dispatch.ClientCanceled} {
		e := batch[0]
		e.EventID = uuid.NewString()
		e.Outcome = outcome
		e.ProviderEvidence = false
		count, err := store.ObservePassive(t.Context(), "core", 1, []dispatch.PassiveEvent{e})
		require.NoError(t, err)
		require.Equal(t, 1, count)
	}
	require.Equal(t, dispatch.Healthy, dispatchHealth(t, db, "core", "a").State)
	limited := cfg
	limits := *cfg.Telemetry
	limits.MaxObservations = 1
	limited.Telemetry = &limits
	insertPassiveEvent(t, db, producer, cfg, dispatch.UpstreamFailure)
	_, err = reader.Read(t.Context(), limited)
	require.Error(t, err, "overflow must not silently truncate")
	newConfig := cfg
	newConfig.ConfigRevision = 2
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), newConfig))
	_, err = store.ObservePassive(t.Context(), "core", 1, batch)
	require.ErrorIs(t, err, dispatch.ErrConfigRevision)
	_, err = db.ExecContext(t.Context(), `UPDATE bridge.dispatch_observation SET observed_at=clock_timestamp()-interval '6 minutes'`)
	require.NoError(t, err)
	batch, err = reader.Read(t.Context(), cfg)
	require.NoError(t, err)
	require.Empty(t, batch)
}

func TestDispatchPassiveLoadPublishAfterTargetRemoval(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := dispatch.Config{CoreInstance: "core", ConfigRevision: 1, Targets: []dispatch.Target{dispatchTarget("a", 1), dispatchTarget("b", 2)}, Telemetry: &dispatch.TelemetryConfig{SlotTTLSeconds: 1800, LoadMaxAgeSeconds: 5, MaxObservations: 100}}
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	cfg.ConfigRevision = 2
	cfg.Targets = cfg.Targets[:1]
	require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
	path := t.TempDir() + "/snapshot.json"
	now := dispatchNow(t, db)
	snapshot, err := store.PublishWithLoad(t.Context(), "core", 2, 30*time.Second, path, dispatch.LoadSample{ObservedAt: now, Accounts: map[int64]int{1: 2}}, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, snapshot.Candidates, 2)
	require.Equal(t, 2, snapshot.Candidates[0].Active)
	require.False(t, snapshot.Candidates[1].Enabled)
	before, err := json.Marshal(snapshot)
	require.NoError(t, err)
	_, err = store.PublishWithLoad(t.Context(), "core", 2, 30*time.Second, path, dispatch.LoadSample{ObservedAt: now, Accounts: map[int64]int{}}, 5*time.Second)
	require.Error(t, err)
	loaded, err := dispatch.LoadSnapshot(path, dispatchNow(t, db))
	require.NoError(t, err)
	after, err := json.Marshal(loaded)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	t.Log(fmt.Sprintf("published revision=%d with removed account disabled", loaded.Revision))
}
