//go:build integration

package repository

import (
	"database/sql"
	"io"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const restoreDatabase = "dispatch_restore"

func restoreCluster(t *testing.T) (*tcpostgres.PostgresContainer, *sql.DB) {
	t.Helper()
	ctx := t.Context()
	container, err := tcpostgres.Run(ctx, selectDockerImage(ctx, postgresImageTag),
		tcpostgres.WithDatabase(restoreDatabase), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(t.Context()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := openSQLWithRetry(ctx, dsn, 30*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return container, db
}

func restoreExec(t *testing.T, container *tcpostgres.PostgresContainer, command string) {
	t.Helper()
	code, output, err := container.Exec(t.Context(), []string{"sh", "-c", command})
	require.NoError(t, err)
	body, _ := io.ReadAll(output)
	require.Zero(t, code, string(body))
}

func clusterIdentity(t *testing.T, db *sql.DB) (int64, int64) {
	t.Helper()
	var system, database int64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT (SELECT system_identifier FROM pg_control_system()),
	 (SELECT oid::bigint FROM pg_database WHERE datname=current_database())`).Scan(&system, &database))
	return system, database
}

// 两个独立集群可分配到相同的库 OID；逻辑恢复到另一集群后，恢复库的 revision 序列回到备份点，
// 必须换新 lineage，否则已接受更高 revision 的 Edge 会把恢复后的发布判为回滚。
func TestDispatchPublishLineageRotatesOnLogicalRestoreToAnotherCluster(t *testing.T) {
	source, sourceDB := restoreCluster(t)
	target, targetDB := restoreCluster(t)
	sourceSystem, sourceOID := clusterIdentity(t, sourceDB)
	targetSystem, targetOID := clusterIdentity(t, targetDB)
	require.NotEqual(t, sourceSystem, targetSystem, "the clusters are independent")
	require.Equal(t, sourceOID, targetOID, "independent clusters reuse the same database OID")

	require.NoError(t, dispatch.ApplySchema(t.Context(), sourceDB))
	store := dispatch.NewStore(sourceDB)
	cfg := failureConfig(t, []dispatch.Target{dispatchTarget("a", 1)}, false)
	cfg.SnapshotServer = &dispatch.SnapshotServerConfig{SocketPath: "/run/dispatchd/snapshot.sock", KeysFile: "unused.json"}
	backup := lineageRound(t, store, cfg)
	restoreExec(t, source, "pg_dump -U postgres -d "+restoreDatabase+" --schema=dispatch --no-owner -f /tmp/dispatch.sql")
	dump, err := source.CopyFileFromContainer(t.Context(), "/tmp/dispatch.sql")
	require.NoError(t, err)
	script, err := io.ReadAll(dump)
	require.NoError(t, err)
	require.NoError(t, dump.Close())

	selector := dispatch.NewSelector(cfg.CoreInstance)
	var accepted dispatch.PublishedSnapshot
	for range 20 {
		accepted = lineageRound(t, store, cfg)
		require.NoError(t, selector.UpdateEpoch(accepted.Lineage, accepted.Snapshot, time.Now()))
	}
	require.Equal(t, backup.Lineage, accepted.Lineage)
	require.Greater(t, accepted.Snapshot.Revision, backup.Snapshot.Revision+1)

	require.NoError(t, target.CopyToContainer(t.Context(), script, "/tmp/dispatch.sql", 0o644))
	restoreExec(t, target, "psql -v ON_ERROR_STOP=1 -q -U postgres -d "+restoreDatabase+" -f /tmp/dispatch.sql")
	restoredStore := dispatch.NewStore(targetDB)
	require.NoError(t, restoredStore.CheckPublishLineageSchema(t.Context()))
	restored := lineageRound(t, restoredStore, cfg)
	require.LessOrEqual(t, restored.Snapshot.Revision, accepted.Snapshot.Revision, "the restored sequence resumes from the backup point")
	require.NotEqual(t, backup.Lineage, restored.Lineage, "a restore into another cluster starts a new lineage")
	lineage, reason := lineageRow(t, targetDB)
	require.Equal(t, restored.Lineage, lineage)
	require.Equal(t, "database_identity_changed", reason)
	require.NoError(t, selector.UpdateEpoch(restored.Lineage, restored.Snapshot, time.Now()),
		"an Edge that accepted a higher source revision accepts the restored publish")
}
