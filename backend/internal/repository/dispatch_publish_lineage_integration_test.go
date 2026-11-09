//go:build integration

package repository

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func lineageRound(t *testing.T, store *dispatch.Store, cfg dispatch.Config) dispatch.PublishedSnapshot {
	t.Helper()
	result, err := dispatch.RunRound(t.Context(), cfg, store, nil)
	require.NoError(t, err)
	published, ok := result.Published()
	require.True(t, ok)
	require.Equal(t, result.Revision, published.Snapshot.Revision)
	return published
}

func lineageRow(t *testing.T, db *sql.DB) (string, string) {
	t.Helper()
	var lineage, reason string
	require.NoError(t, db.QueryRowContext(t.Context(),
		`SELECT lineage::text, reason FROM dispatch.publish_lineage WHERE singleton AND database_oid=(SELECT oid::bigint FROM pg_database WHERE datname=current_database())`).
		Scan(&lineage, &reason))
	return lineage, reason
}

func TestDispatchPublishLineagePersistsAndRotates(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)

	plain := lineageRound(t, store, cfg)
	require.Empty(t, plain.Lineage, "lineage stays out of the default file mode")
	require.Zero(t, dispatchCount(t, db, `SELECT count(*) FROM dispatch.publish_lineage`))

	cfg.SnapshotServer = &dispatch.SnapshotServerConfig{SocketPath: "/run/dispatchd/snapshot.sock", KeysFile: "unused.json"}
	first := lineageRound(t, store, cfg)
	_, err := uuid.Parse(first.Lineage)
	require.NoError(t, err)
	lineage, reason := lineageRow(t, db)
	require.Equal(t, first.Lineage, lineage)
	require.Equal(t, "initial", reason)
	require.Greater(t, first.Snapshot.Revision, plain.Snapshot.Revision)

	second := lineageRound(t, store, cfg)
	require.Equal(t, first.Lineage, second.Lineage, "the lineage is stable across rounds")
	require.Greater(t, second.Snapshot.Revision, first.Snapshot.Revision)
	body, err := os.ReadFile(cfg.SnapshotFile)
	require.NoError(t, err)
	require.NotContains(t, string(body), first.Lineage)
	require.NotContains(t, string(body), "lineage", "the file snapshot format is unchanged")
	file, err := dispatch.LoadSnapshot(cfg.SnapshotFile, time.Now())
	require.NoError(t, err)
	require.Equal(t, second.Snapshot.Revision, file.Revision)

	dispatchExec(t, db, `UPDATE dispatch.publish_lineage SET system_identifier=system_identifier+1`)
	clusterSwitched := lineageRound(t, store, cfg)
	require.NotEqual(t, second.Lineage, clusterSwitched.Lineage, "a different cluster with the same database OID starts a new lineage")
	_, reason = lineageRow(t, db)
	require.Equal(t, "database_identity_changed", reason)

	dispatchExec(t, db, `UPDATE dispatch.publish_lineage SET database_oid=database_oid+1`)
	switched := lineageRound(t, store, cfg)
	require.NotEqual(t, clusterSwitched.Lineage, switched.Lineage, "a different database identity starts a new lineage")
	lineage, reason = lineageRow(t, db)
	require.Equal(t, switched.Lineage, lineage)
	require.Equal(t, "database_identity_changed", reason)

	rotated, err := store.RotatePublishLineage(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, switched.Lineage, rotated)
	_, reason = lineageRow(t, db)
	require.Equal(t, "operator_rotation", reason)
	after := lineageRound(t, store, cfg)
	require.Equal(t, rotated, after.Lineage)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.publish_lineage`))

	selector := dispatch.NewSelector(cfg.CoreInstance)
	require.NoError(t, selector.UpdateEpoch(after.Lineage, after.Snapshot, time.Now()))
	require.ErrorContains(t, selector.UpdateEpoch(after.Lineage, second.Snapshot, time.Now()), "rollback")
}

func TestDispatchPublishLineageRequiresSchema005OnlyWhenEnabled(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)
	rollbackMetricsSchema(t, db)
	dispatchExec(t, db, `DROP TABLE dispatch.publish_lineage`)
	dispatchExec(t, db, `DELETE FROM dispatch.schema_migrations WHERE version='005'`)

	plain := lineageRound(t, store, cfg)
	require.Empty(t, plain.Lineage, "file mode does not depend on migration 005")

	cfg.SnapshotServer = &dispatch.SnapshotServerConfig{SocketPath: "/run/dispatchd/snapshot.sock", KeysFile: "unused.json"}
	_, err := dispatch.RunRound(t.Context(), cfg, store, nil)
	require.ErrorIs(t, err, dispatch.ErrPublishLineageSchema)
	_, err = store.RotatePublishLineage(t.Context())
	require.ErrorIs(t, err, dispatch.ErrPublishLineageSchema)
	require.ErrorIs(t, store.CheckPublishLineageSchema(t.Context()), dispatch.ErrPublishLineageSchema, "startup pre-check reports missing 005")
	file, err := dispatch.LoadSnapshot(cfg.SnapshotFile, time.Now())
	require.NoError(t, err)
	require.Equal(t, plain.Snapshot.Revision, file.Revision, "a failed publish leaves the last file snapshot in place")

	require.NoError(t, dispatch.ApplySchema(t.Context(), db))
	require.NoError(t, store.CheckPublishLineageSchema(t.Context()))
	recovered := lineageRound(t, store, cfg)
	require.NotEmpty(t, recovered.Lineage)
	_, reason := lineageRow(t, db)
	require.Equal(t, "initial", reason)
}

func TestDispatchPublishLineageRotationSerializesWithPublish(t *testing.T) {
	db := dispatchDB(t)
	store := dispatch.NewStore(db)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)
	cfg.SnapshotServer = &dispatch.SnapshotServerConfig{SocketPath: "/run/dispatchd/snapshot.sock", KeysFile: "unused.json"}
	original := lineageRound(t, store, cfg).Lineage

	started := make(chan struct{})
	var rotated string
	var rotateErr error
	var group sync.WaitGroup
	group.Go(func() {
		<-started
		rotated, rotateErr = store.RotatePublishLineage(t.Context())
	})
	rounds := make([]dispatch.PublishedSnapshot, 0, 12)
	for i := range 12 {
		if i == 2 {
			close(started)
		}
		rounds = append(rounds, lineageRound(t, store, cfg))
	}
	group.Wait()
	require.NoError(t, rotateErr)
	rounds = append(rounds, lineageRound(t, store, cfg))
	require.Equal(t, rotated, rounds[len(rounds)-1].Lineage, "rounds after the rotation commit use the new lineage")

	var oldMax, newMin int64
	for i, round := range rounds {
		if i > 0 {
			require.Greater(t, round.Snapshot.Revision, rounds[i-1].Snapshot.Revision)
		}
		switch round.Lineage {
		case original:
			require.Zero(t, newMin, "no round returns to the old lineage after the rotation")
			oldMax = round.Snapshot.Revision
		case rotated:
			if newMin == 0 {
				newMin = round.Snapshot.Revision
			}
		default:
			t.Fatalf("unexpected lineage %q", round.Lineage)
		}
	}
	require.Less(t, oldMax, newMin, "every old-lineage revision precedes every rotated-lineage revision")
	require.True(t, slices.ContainsFunc(rounds, func(r dispatch.PublishedSnapshot) bool { return r.Lineage == original }))
	lineage, reason := lineageRow(t, db)
	require.Equal(t, rotated, lineage)
	require.Equal(t, "operator_rotation", reason)
	require.Equal(t, 1, dispatchCount(t, db, `SELECT count(*) FROM dispatch.publish_lineage`))
}

func TestDispatchPublishLineageIdentityReadableOrFailsEarly(t *testing.T) {
	db := dispatchDB(t)
	const role = "dispatch_lineage_probe"
	dispatchExec(t, db, `CREATE ROLE `+role+` LOGIN PASSWORD 'probe'`)
	dispatchExec(t, db, `GRANT USAGE ON SCHEMA dispatch TO `+role)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `GRANT EXECUTE ON FUNCTION pg_control_system() TO PUBLIC`)
		_, _ = integrationDB.ExecContext(context.Background(), `DROP OWNED BY `+role)
		_, _ = integrationDB.ExecContext(context.Background(), `DROP ROLE IF EXISTS `+role)
	})
	require.True(t, strings.Contains(integrationDSN, "postgres:postgres@"))
	plain, err := sql.Open("postgres", strings.Replace(integrationDSN, "postgres:postgres@", role+":probe@", 1))
	require.NoError(t, err)
	t.Cleanup(func() { _ = plain.Close() })
	plain.SetMaxOpenConns(1)

	store := dispatch.NewStore(plain)
	require.NoError(t, store.CheckPublishLineageSchema(t.Context()), "a role without extra grants reads the cluster identity by default")
	dispatchExec(t, db, `REVOKE EXECUTE ON FUNCTION pg_control_system() FROM PUBLIC`)
	require.ErrorContains(t, store.CheckPublishLineageSchema(t.Context()), "identity unavailable",
		"startup fails instead of reusing the old lineage when the identity is unreadable")
}
