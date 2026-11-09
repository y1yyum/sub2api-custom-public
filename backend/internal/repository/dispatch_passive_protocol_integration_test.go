//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func insertPassiveProtocolEvents(t *testing.T, db *sql.DB, producer *custombridge.TelemetryProducer, cfg dispatch.Config) {
	t.Helper()
	a := cfg.Targets[0]
	for _, row := range []struct {
		outcome      dispatch.Outcome
		evidence     bool
		phase, fact  string
		usageMissing bool
	}{
		{dispatch.Unknown, false, "content", "stream_break", false},
		{dispatch.Unknown, false, "terminal", "semantic_failure", false},
		{dispatch.Success, true, "terminal", "non_text_terminal", true},
		{dispatch.ClientCanceled, false, "connect", "bridge_deadline", false},
		{dispatch.UpstreamFailure, true, "headers", "", false},
	} {
		id := uuid.NewString()
		_, err := db.ExecContext(t.Context(), `INSERT INTO bridge.dispatch_observation
		 (event_id,core_instance,target_ref,config_revision,account_id,credential_ref,credential_revision,
		 provider_ref,pool_ref,policy_revision,model,region,outcome,provider_evidence,retry_after_seconds,
		 latency_millis,observed_at,internal_key_id,attempt_id,core_request_id,producer_generation,phase,protocol_fact,usage_missing)
		 VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,0,10,clock_timestamp(),1,$1::text,$1::text,$15,$16,$17,$18)`,
			id, cfg.CoreInstance, a.Ref, cfg.ConfigRevision, a.AccountID, a.CredentialRef, a.CredentialRevision, a.ProviderRef,
			a.PoolRef, a.PolicyRevision, a.Model, a.Region, string(row.outcome), row.evidence, producer.Source().ProducerGeneration,
			row.phase, row.fact, row.usageMissing)
		require.NoError(t, err)
	}
}

func passiveProtocolFacts(t *testing.T, db *sql.DB, ref string) map[string]int {
	t.Helper()
	health := dispatchHealth(t, db, "core", ref)
	if health.Rolling == nil {
		return nil
	}
	var facts map[string]int
	for _, bucket := range health.Rolling.Buckets {
		for key, count := range bucket.Facts {
			if facts == nil {
				facts = map[string]int{}
			}
			facts[key] += count
		}
	}
	return facts
}

func TestDispatchPassiveProtocolFactsGateAndDedup(t *testing.T) {
	for _, mode := range []string{"disabled", "enabled", "legacy_columns"} {
		t.Run(mode, func(t *testing.T) {
			db := dispatchDB(t)
			cfg, producer := passiveConfig(t, db)
			cfg.Routing = &dispatch.RoutingConfig{TTFTTargetMillis: 1000, WeightSmoothingSeconds: 10}
			cfg.HealthWindow = &dispatch.HealthWindowConfig{MinSamples: 20, ProtocolFacts: mode != "disabled"}
			insertPassiveProtocolEvents(t, db, producer, cfg)
			if mode == "legacy_columns" {
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					require.NoError(t, custombridge.ApplyTelemetrySchema(ctx, db))
				})
				dispatchExec(t, db, `ALTER TABLE bridge.dispatch_observation DROP COLUMN usage_missing, DROP COLUMN protocol_fact, DROP COLUMN phase`)
			}
			reader, err := dispatch.NewPassiveReader(t.Context(), passiveReadOnlyDB(t, db), cfg.CoreInstance, cfg.ConfigRevision)
			require.NoError(t, err)
			batch, err := reader.Read(t.Context(), cfg)
			require.NoError(t, err)
			require.Len(t, batch, 5)
			for _, event := range batch {
				raw, err := json.Marshal(event.Observation)
				require.NoError(t, err)
				if mode != "enabled" {
					require.NotContains(t, string(raw), "phase", "projected defaults keep legacy dedup JSON")
					require.NotContains(t, string(raw), "protocol_fact")
					require.NotContains(t, string(raw), "usage_missing")
				}
			}
			store := dispatch.NewStore(db)
			require.NoError(t, store.SyncRuntimeConfig(t.Context(), cfg))
			count, err := store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, batch)
			require.NoError(t, err)
			require.Equal(t, 5, count)
			legacyPayloads := dispatchCount(t, db, `SELECT count(*) FROM dispatch.observations
			 WHERE NOT (payload ? 'phase') AND NOT (payload ? 'protocol_fact') AND NOT (payload ? 'usage_missing')`)
			want := map[string]int(nil)
			if mode == "enabled" {
				want = map[string]int{"stream_break.content": 1, "semantic_failure": 1, "non_text_terminal": 1, "usage_missing": 1, "bridge_deadline.connect": 1}
				require.Zero(t, legacyPayloads)
			} else {
				require.Equal(t, 5, legacyPayloads)
			}
			require.Equal(t, want, passiveProtocolFacts(t, db, "a"))
			require.Nil(t, passiveProtocolFacts(t, db, "alias"), "facts never fan out to peers")
			health := dispatchHealth(t, db, "core", "a")
			var samples, failures, passive int
			for _, bucket := range health.Rolling.Buckets {
				samples, failures, passive = samples+bucket.Samples, failures+bucket.Failures, passive+bucket.PassiveSamples
			}
			require.Equal(t, []int{2, 1, 2}, []int{samples, failures, passive}, "only attributed outcomes are health samples")
			reports, err := store.HealthWindowReports(t.Context(), cfg.CoreInstance, cfg.ConfigRevision)
			require.NoError(t, err)
			require.Equal(t, "a", reports[0].TargetRef)
			require.Equal(t, want, reports[0].Window.Baseline.Facts)

			rescan, err := reader.Read(t.Context(), cfg)
			require.NoError(t, err)
			count, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, rescan)
			require.NoError(t, err)
			require.Zero(t, count)
			require.Equal(t, want, passiveProtocolFacts(t, db, "a"), "rescans never double count")
			if mode == "legacy_columns" {
				return
			}
			dispatchExec(t, db, `UPDATE bridge.dispatch_observation SET protocol_fact='' WHERE protocol_fact='stream_break'`)
			changed, err := reader.Read(t.Context(), cfg)
			require.NoError(t, err)
			_, err = store.ObservePassive(t.Context(), cfg.CoreInstance, cfg.ConfigRevision, changed)
			if mode == "enabled" {
				require.ErrorIs(t, err, dispatch.ErrObservationConflict)
			} else {
				require.NoError(t, err, "disabled facts are not part of the dedup payload")
			}
		})
	}
}
