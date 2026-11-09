//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custom/quality"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const qualityTopologyGrid = "#####\n# # #\n#####\n# # #\n#####"

// Only saved, synthetic results are inserted. No scheduled runner, Core server,
// provider, or model credentials are started by this topology.
func TestDispatchQualityTopologyScheduledResults(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	q := newQualityTopology(t, ctx)
	q.prepareSource(t)
	q.writeConfig(t)
	var legacy dispatch.Snapshot
	t.Run("default_off_and_legacy_schema", func(t *testing.T) {
		body := q.command(t, true, []string{"DISPATCH_DSN=invalid", "DISPATCH_QUALITY_DSN=invalid"})
		require.Contains(t, string(body), "默认关闭")
		require.False(t, q.hasQualitySchema(t))
		q.action(t, true, nil, "--apply-schema")
		legacy = q.round(t, false)
		require.False(t, q.hasQualitySchema(t), "legacy --once must not migrate optional quality tables")
		q.requireNeutral(t, legacy)
	})
	if t.Failed() {
		return
	}

	q.cfg.ConfigRevision++
	q.cfg.Quality = &dispatch.QualityConfig{BaselineFile: filepath.Join(q.f.dir, "baseline.json"), Bindings: q.bindings, ApplyWeight: true}
	q.writeConfig(t)
	t.Run("explicit_quality_schema", func(t *testing.T) {
		before := q.snapshotBytes(t)
		q.action(t, false, q.sourceEnv(), "--once", "--config", q.configPath)
		require.Equal(t, before, q.snapshotBytes(t), "quality cannot implicitly migrate or renew a legacy snapshot")
		require.False(t, q.hasQualitySchema(t))
		q.action(t, true, nil, "--apply-quality-schema")
		require.True(t, q.hasQualitySchema(t))
		q.action(t, true, nil, "--apply-quality-schema")
		q.action(t, true, nil, "--apply-schema")
		snapshot := q.round(t, true)
		q.requireNeutral(t, snapshot)
		require.Greater(t, snapshot.Revision, legacy.Revision)
		require.Zero(t, q.scoreCount(t, 0), "runs started before activation must not inherit a new binding")
	})
	if t.Failed() {
		return
	}

	var health dispatch.Health
	t.Run("uncalibrated_saved_results_and_restart_dedup", func(t *testing.T) {
		health = dispatchHealth(t, q.db, q.cfg.CoreInstance, q.cfg.Targets[0].Ref)
		q.seedResults(t, 5)
		snapshot := q.round(t, true)
		q.requireNeutral(t, snapshot)
		require.Equal(t, 20, q.scoreCount(t, q.cfg.ConfigRevision))
		q.requireScores(t)
		reports := q.reports(t)
		q.requireReports(t, reports, 10)
		restarted := q.round(t, true)
		require.Greater(t, restarted.Revision, snapshot.Revision)
		require.Equal(t, 20, q.scoreCount(t, 0), "a new CLI process must not append duplicate scores")
		q.requireNeutral(t, restarted)
		require.Equal(t, health, dispatchHealth(t, q.db, q.cfg.CoreInstance, q.cfg.Targets[0].Ref))
		require.Zero(t, dispatchCount(t, q.db, `SELECT count(*) FROM dispatch.observations`))
		require.Zero(t, dispatchCount(t, q.db, `SELECT count(*) FROM dispatch.probes`))
	})
	if t.Failed() {
		return
	}

	t.Run("query_only_role_and_permission_failure", func(t *testing.T) {
		for _, query := range []string{
			`SELECT credentials FROM accounts LIMIT 1`,
			`SELECT error_message FROM scheduled_test_results LIMIT 1`,
			`UPDATE scheduled_test_results SET verdict='pass' WHERE false`,
			`DELETE FROM scheduled_test_plans WHERE false`,
			`INSERT INTO scheduled_test_results(plan_id) SELECT 0 WHERE false`,
		} {
			rows, err := q.reader.QueryContext(ctx, query)
			if rows != nil {
				require.NoError(t, rows.Close())
			}
			require.Error(t, err, "restricted source: %s", query)
		}
		before := q.snapshotBytes(t)
		dispatchExec(t, q.db, `REVOKE SELECT(response_text) ON scheduled_test_results FROM `+pq.QuoteIdentifier(q.role))
		defer dispatchExec(t, q.db, `GRANT SELECT(response_text) ON scheduled_test_results TO `+pq.QuoteIdentifier(q.role))
		q.action(t, false, q.sourceEnv(), "--once", "--config", q.configPath)
		require.Equal(t, before, q.snapshotBytes(t), "permission failure cannot renew admission")
		require.Equal(t, 20, q.scoreCount(t, 0))
		require.Equal(t, health, dispatchHealth(t, q.db, q.cfg.CoreInstance, q.cfg.Targets[0].Ref))
	})
	if t.Failed() {
		return
	}

	t.Run("disconnected_source_and_independent_kind_reports", func(t *testing.T) {
		before := q.snapshotBytes(t)
		q.proxy.block(true)
		defer q.proxy.block(false)
		q.action(t, false, q.sourceEnv(), "--once", "--config", q.configPath)
		require.Equal(t, before, q.snapshotBytes(t), "source outage cannot redate or replace the last snapshot")
		q.requireReports(t, q.reports(t), 10)
		require.Equal(t, before, q.snapshotBytes(t), "--quality-report must not publish or require its disconnected source")
		require.Equal(t, 20, q.scoreCount(t, 0))
		var old dispatch.Snapshot
		require.NoError(t, json.Unmarshal(before, &old))
		require.Error(t, old.Validate(old.ExpiresAt), "unchanged snapshot must become inadmissible at its original expiry")
	})
	if t.Failed() {
		return
	}

	t.Run("revision_and_credential_cutoffs", func(t *testing.T) {
		var previous time.Time
		require.NoError(t, q.db.QueryRowContext(ctx, `SELECT activated_at FROM dispatch.quality_configs WHERE core_instance=$1`, q.cfg.CoreInstance).Scan(&previous))
		for _, rotateCredential := range []bool{false, true} {
			q.cfg.ConfigRevision++
			if rotateCredential {
				for i := range q.cfg.Targets {
					q.cfg.Targets[i].CredentialRevision++
				}
				q.expectedState = dispatch.Tripped
			}
			q.writeConfig(t)
			q.requireNeutral(t, q.round(t, true))
			var activated time.Time
			require.NoError(t, q.db.QueryRowContext(ctx, `SELECT activated_at FROM dispatch.quality_configs WHERE core_instance=$1`, q.cfg.CoreInstance).Scan(&activated))
			require.True(t, activated.After(previous))
			previous = activated
			require.Zero(t, q.scoreCount(t, q.cfg.ConfigRevision), "older saved runs cannot be relabeled into a new revision or credential")
			q.requireReports(t, q.reports(t), 0)
			q.seedResults(t, 1)
			q.requireNeutral(t, q.round(t, true))
			require.Equal(t, 4, q.scoreCount(t, q.cfg.ConfigRevision))
			q.requireNeutral(t, q.round(t, true))
			require.Equal(t, 4, q.scoreCount(t, q.cfg.ConfigRevision))
		}
		require.Equal(t, 28, q.scoreCount(t, 0), "historical scores remain auditable but are not reused by the current report")
		require.Equal(t, 20, q.scoreCount(t, 2))
	})
	if t.Failed() {
		return
	}

	t.Run("deferral_backlog_report", func(t *testing.T) {
		q.cfg.ConfigRevision++
		q.cfg.Quality.Deferral = &dispatch.QualityDeferralConfig{BudgetMilliseconds: 1000, TTLMarginMilliseconds: 1000, MaxPending: 1}
		q.writeConfig(t)
		q.requireNeutral(t, q.round(t, true))
		q.seedResults(t, 1)
		pending := 0
		for _, report := range q.reports(t) {
			require.NotNil(t, report.PendingSamples)
			require.True(t, *report.BacklogExceeded)
			require.Equal(t, 1000, report.FactorMilli)
			pending += *report.PendingSamples
		}
		require.Equal(t, 4, pending)
		require.Zero(t, q.scoreCount(t, q.cfg.ConfigRevision), "the backlog report reads but never scores")
		q.proxy.block(true)
		q.action(t, false, q.sourceEnv(), "--quality-report", "--config", q.configPath)
		q.proxy.block(false)
		q.requireNeutral(t, q.round(t, true))
		require.Equal(t, 4, q.scoreCount(t, q.cfg.ConfigRevision))
		for _, report := range q.reports(t) {
			require.Zero(t, *report.PendingSamples)
			require.Nil(t, report.OldestPendingSeconds)
			require.False(t, *report.BacklogExceeded)
		}
	})
	if t.Failed() {
		return
	}

	pendingSamples := func(t *testing.T) int {
		pending := 0
		for _, report := range q.reports(t) {
			pending += *report.PendingSamples
		}
		return pending
	}
	disableDeferral := func(t *testing.T) {
		q.cfg.ConfigRevision++
		q.cfg.Quality.Deferral = nil
		q.writeConfig(t)
		q.round(t, true)
	}
	t.Run("deferral_rollback_after_drain", func(t *testing.T) {
		q.seedResults(t, 1)
		require.Equal(t, 4, pendingSamples(t))
		q.round(t, true)
		require.Zero(t, pendingSamples(t), "the operator drains the backlog on the current revision first")
		scored := q.scoreCount(t, 0)
		disableDeferral(t)
		require.Equal(t, scored, q.scoreCount(t, 0), "the rollback neither drops nor rescores recorded results")
	})
	if t.Failed() {
		return
	}

	// Known behavior: a new config revision re-activates quality, so results
	// started before it are never scored. Rolling back with a backlog drops it.
	t.Run("deferral_rollback_with_backlog_discards_pending", func(t *testing.T) {
		q.cfg.ConfigRevision++
		// The publication margin ends 1ms after each round starts: scoring starves.
		q.cfg.Quality.Deferral = &dispatch.QualityDeferralConfig{BudgetMilliseconds: 1, TTLMarginMilliseconds: 9899, MaxPending: 1}
		q.writeConfig(t)
		q.round(t, true)
		q.seedResults(t, 1)
		q.round(t, true)
		require.Zero(t, q.scoreCount(t, q.cfg.ConfigRevision))
		require.Equal(t, 4, pendingSamples(t))
		scored := q.scoreCount(t, 0)
		disableDeferral(t)
		require.Zero(t, q.scoreCount(t, q.cfg.ConfigRevision), "the pending backlog is not inherited by the new revision")
		require.Equal(t, scored, q.scoreCount(t, 0))
	})
	t.Logf("actual_dispatchd scheduled_source=restricted_PostgreSQL score_rows=%d config_revision=%d synthetic_fixture_only=true production_calibration=Unknown provider_calls=0", q.scoreCount(t, 0), q.cfg.ConfigRevision)
}

type qualityTopology struct {
	f                                     *bridgeTopology
	db, reader                            *sql.DB
	cfg                                   dispatch.Config
	configPath, dsnFile, sourceFile, role string
	proxy                                 *bridgeObservationProxy
	plans                                 []*service.ScheduledTestPlan
	bindings                              []dispatch.QualityBinding
	expectedState                         dispatch.State
}

func newQualityTopology(t *testing.T, ctx context.Context) *qualityTopology {
	t.Helper()
	f := &bridgeTopology{owner: t, ctx: ctx, dir: t.TempDir()}
	var err error
	f.backend, err = filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DISPATCH_") {
			f.env = append(f.env, entry)
		}
	}
	f.build(t, "dispatchd")
	q := &qualityTopology{f: f, db: dispatchDB(t), configPath: filepath.Join(f.dir, "quality.json"), dsnFile: filepath.Join(f.dir, "dispatch-dsn"), expectedState: dispatch.Healthy}
	require.NoError(t, os.WriteFile(q.dsnFile, []byte(integrationDSN), 0600))
	target := dispatchTarget("quality", 1)
	q.cfg = dispatch.Config{Version: 1, CoreInstance: "quality-topology", ConfigRevision: 1, SnapshotFile: filepath.Join(f.dir, "snapshot.json"), SnapshotTTLSeconds: 10, PollIntervalMilliseconds: 100, Targets: []dispatch.Target{target}}
	q.writeConfig(t)
	return q
}

func (q *qualityTopology) prepareSource(t *testing.T) {
	t.Helper()
	cutoff := dispatchNow(t, q.db).Add(-time.Minute)
	var account int64
	for i, kind := range []string{"char_art", "char_art", "quiz", "quiz"} {
		plan, target, binding := qualitySourcePlan(t, kind, account, cutoff)
		account = target.AccountID
		if i == 1 {
			plan.Prompt = "draw a second square"
			dispatchExec(t, q.db, `UPDATE scheduled_test_plans SET prompt=$2 WHERE id=$1`, plan.ID, plan.Prompt)
		}
		q.plans = append(q.plans, plan)
		q.bindings = append(q.bindings, binding)
		q.cfg.Targets[0] = target
	}
	disabled := q.cfg.Targets[0]
	disabled.Ref, disabled.Enabled = "disabled", false
	q.cfg.Targets = append(q.cfg.Targets, disabled)
	file := quality.BaselineFile{SchemaVersion: 1, Revision: "q-topology-uncalibrated"}
	for i, plan := range q.plans[:2] {
		file.Templates = append(file.Templates, quality.Template{ID: []string{"first", "second"}[i], QuestionRef: quality.QuestionRef("char_art", "", plan.Prompt, ""), Target: "田", Grid: qualityTopologyGrid})
	}
	q.f.writeJSON(t, "baseline.json", file)
	encoded, err := os.ReadFile(filepath.Join(q.f.dir, "baseline.json"))
	require.NoError(t, err)
	baseline, err := quality.ParseBaseline(encoded)
	require.NoError(t, err)
	require.Equal(t, "Unknown", baseline.Report().ProductionData)
	require.Zero(t, baseline.Report().CoveredReferences)
	q.seedResults(t, 1)
	q.role = "q_topology_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := "synthetic-" + uuid.NewString()
	dispatchExec(t, q.db, `CREATE ROLE `+pq.QuoteIdentifier(q.role)+` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT PASSWORD `+pq.QuoteLiteral(password))
	dispatchExec(t, q.db, `ALTER ROLE `+pq.QuoteIdentifier(q.role)+` SET default_transaction_read_only=on;
	 GRANT USAGE ON SCHEMA public TO `+pq.QuoteIdentifier(q.role)+`;
	 GRANT SELECT(id,account_id,model_id,kind,prompt,enabled,created_at,updated_at,policy_version) ON scheduled_test_plans TO `+pq.QuoteIdentifier(q.role)+`;
	 GRANT SELECT(id,plan_id,model_id,kind,question_id,prompt,response_text,expected_answer,verdict,status,started_at,finished_at)
	 ON scheduled_test_results TO `+pq.QuoteIdentifier(q.role))
	dsn, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	q.proxy = newBridgeObservationProxy(t, dsn.Host)
	dsn.Host = q.proxy.listener.Addr().String()
	dsn.User = url.UserPassword(q.role, password)
	query := dsn.Query()
	query.Set("connect_timeout", "2")
	dsn.RawQuery = query.Encode()
	q.sourceFile = filepath.Join(q.f.dir, "quality-source-dsn")
	require.NoError(t, os.WriteFile(q.sourceFile, []byte(dsn.String()), 0600))
	q.reader, err = sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	require.NoError(t, q.reader.PingContext(q.f.ctx))
	t.Cleanup(func() {
		require.NoError(t, q.reader.Close())
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(cleanup, `DROP OWNED BY `+pq.QuoteIdentifier(q.role)+`; DROP ROLE `+pq.QuoteIdentifier(q.role))
		require.NoError(t, err)
	})
}

func (q *qualityTopology) writeConfig(t *testing.T) {
	t.Helper()
	q.f.writeJSON(t, filepath.Base(q.configPath), q.cfg)
	loaded, err := dispatch.LoadConfig(q.configPath)
	require.NoError(t, err)
	q.cfg = loaded
}

func (q *qualityTopology) seedResults(t *testing.T, n int) {
	t.Helper()
	for i, plan := range q.plans {
		response, expected, verdict := qualityTopologyGrid, "", ""
		if i == 1 {
			response = "#"
		}
		if plan.Kind == "quiz" {
			response, expected, verdict = "2", "2", "pass"
			if i == 3 {
				response, verdict = "3", "fail"
			}
		}
		_, err := q.db.ExecContext(q.f.ctx, `INSERT INTO scheduled_test_results(plan_id,model_id,kind,prompt,response_text,expected_answer,verdict,status,started_at,finished_at)
		 SELECT $1,$2,$3,$4,$5,$6,$7,'success',clock_timestamp(),clock_timestamp() FROM generate_series(1,$8::int)`,
			plan.ID, plan.ModelID, plan.Kind, plan.Prompt, response, expected, verdict, n)
		require.NoError(t, err)
	}
}

func (q *qualityTopology) sourceEnv() []string {
	return []string{"DISPATCH_QUALITY_DSN_FILE=" + q.sourceFile}
}

func (q *qualityTopology) command(t *testing.T, success bool, env []string, args ...string) []byte {
	t.Helper()
	p := q.f.start(t, "dispatchd", env, args...)
	select {
	case <-p.done:
	case <-time.After(35 * time.Second):
		t.Fatal("actual dispatchd did not exit within its source-read bound")
	}
	body, err := os.ReadFile(p.logPath)
	require.NoError(t, err)
	t.Logf("actual_cmd_dispatchd pid=%d success=%t output=%s", p.cmd.Process.Pid, p.err == nil, strings.TrimSpace(string(body)))
	if success {
		require.NoError(t, p.err, "dispatchd: %s", body)
	} else {
		require.Error(t, p.err, "dispatchd must fail closed: %s", body)
	}
	return body
}

func (q *qualityTopology) action(t *testing.T, success bool, env []string, args ...string) []byte {
	t.Helper()
	return q.command(t, success, env, append(args, "--dsn-file", q.dsnFile)...)
}

func (q *qualityTopology) round(t *testing.T, qualityEnabled bool) dispatch.Snapshot {
	t.Helper()
	var env []string
	if qualityEnabled {
		env = q.sourceEnv()
	}
	q.action(t, true, env, "--once", "--config", q.configPath)
	snapshot, err := dispatch.LoadSnapshot(q.cfg.SnapshotFile, time.Now())
	require.NoError(t, err)
	return snapshot
}

func (q *qualityTopology) reports(t *testing.T) []dispatch.QualityReport {
	t.Helper()
	body := q.action(t, true, q.sourceEnv(), "--quality-report", "--config", q.configPath)
	var reports []dispatch.QualityReport
	require.NoError(t, json.Unmarshal(body, &reports))
	return reports
}

func (q *qualityTopology) requireReports(t *testing.T, reports []dispatch.QualityReport, samples int) {
	t.Helper()
	require.Len(t, reports, len(q.cfg.Targets)*2)
	seen := map[string]bool{}
	for _, report := range reports {
		if report.TargetRef != q.cfg.Targets[0].Ref {
			continue
		}
		require.False(t, seen[report.Kind])
		seen[report.Kind] = true
		require.Equal(t, samples, report.Samples)
		require.Zero(t, report.QualifiedSamples)
		require.Equal(t, 1000, report.FactorMilli)
		require.Equal(t, "insufficient_evidence", report.Reason)
		require.Equal(t, quality.ScorerRevision, report.ScorerRevision)
	}
	require.Equal(t, map[string]bool{"char_art": true, "quiz": true}, seen)
}

func (q *qualityTopology) requireNeutral(t *testing.T, snapshot dispatch.Snapshot) {
	t.Helper()
	require.Len(t, snapshot.Candidates, len(q.cfg.Targets))
	for _, candidate := range snapshot.Candidates {
		found := false
		for _, target := range q.cfg.Targets {
			if target.Ref != candidate.Ref {
				continue
			}
			found = true
			require.Equal(t, target, candidate.Target, "quality cannot rewrite eligibility, account, credential, or route identity")
			require.Equal(t, q.expectedState, candidate.State)
			if target.Enabled && q.expectedState == dispatch.Healthy {
				require.Equal(t, target.BaseWeight, candidate.EffectiveWeight)
			} else {
				require.Zero(t, candidate.EffectiveWeight)
			}
			if target.Enabled && q.expectedState == dispatch.Tripped {
				require.Equal(t, "credential_rotated", candidate.Reason, "quality cannot recover the native credential rotation gate")
			}
		}
		require.True(t, found)
	}
}

func (q *qualityTopology) requireScores(t *testing.T) {
	t.Helper()
	rows, err := q.db.QueryContext(q.f.ctx, `SELECT score FROM dispatch.quality_results WHERE core_instance=$1 AND config_revision=$2`, q.cfg.CoreInstance, q.cfg.ConfigRevision)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	values := map[string][]int{}
	for rows.Next() {
		var body []byte
		var evaluation struct {
			Result    quality.Result    `json:"result"`
			Reference quality.Reference `json:"reference"`
		}
		require.NoError(t, rows.Scan(&body))
		require.NoError(t, json.Unmarshal(body, &evaluation))
		require.Equal(t, 40, evaluation.Result.Confidence)
		require.False(t, evaluation.Reference.CalibratedCoverage)
		values[evaluation.Result.Kind] = append(values[evaluation.Result.Kind], evaluation.Result.Score)
	}
	require.NoError(t, rows.Err())
	for _, kind := range []string{"char_art", "quiz"} {
		require.Len(t, values[kind], 10)
		good, bad := 0, 0
		for _, score := range values[kind] {
			if score == 100 {
				good++
			} else {
				bad++
				require.Less(t, score, 100)
			}
		}
		require.Equal(t, 5, good)
		require.Equal(t, 5, bad)
	}
}

func (q *qualityTopology) snapshotBytes(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(q.cfg.SnapshotFile)
	require.NoError(t, err)
	return body
}

func (q *qualityTopology) hasQualitySchema(t *testing.T) bool {
	t.Helper()
	var exists bool
	require.NoError(t, q.db.QueryRowContext(q.f.ctx, `SELECT to_regclass('dispatch.quality_results') IS NOT NULL`).Scan(&exists))
	return exists
}

func (q *qualityTopology) scoreCount(t *testing.T, revision int64) int {
	t.Helper()
	return dispatchCount(t, q.db, `SELECT count(*) FROM dispatch.quality_results WHERE core_instance=$1 AND ($2::bigint=0 OR config_revision=$2)`, q.cfg.CoreInstance, revision)
}
