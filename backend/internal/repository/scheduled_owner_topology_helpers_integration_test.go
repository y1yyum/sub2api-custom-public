//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const (
	ownerTopologyArtPlan       = 901
	ownerTopologyQuizPlan      = 902
	ownerTopologyUnmanagedPlan = 903
	ownerTopologyCrashPlan     = 904
	ownerTopologyQuestionA     = 911
	ownerTopologyQuestionB     = 912
	ownerTopologyExtraFirst    = 950
	ownerTopologyCachedPlan    = 957
	ownerTopologyLegacyCalls   = 10
)

type scheduledOwnerTopology struct {
	f                                        *bridgeTopology
	databaseID                               string
	coreDSNFile, dispatchDSNFile, configPath string
	dispatchProxy                            *bridgeObservationProxy
	history                                  map[int64]string
	staticPlans                              map[int64]string
	questions, settings                      string
	configRevision                           int64
	queueReader                              *sql.DB
}

type ownerTopologyStatus struct {
	DatabaseID    string `json:"database_id"`
	PlanID        int64  `json:"plan_id"`
	Owner         string `json:"owner"`
	State         string `json:"state"`
	Epoch         int64  `json:"epoch"`
	PlanDigest    string `json:"plan_digest"`
	ResultsCount  int64  `json:"results_count"`
	ResultsMaxID  int64  `json:"results_max_id"`
	ResultsDigest string `json:"results_digest"`
	Queued        int    `json:"queued"`
	Running       int    `json:"running"`
}

func newScheduledOwnerTopology(t *testing.T, ctx context.Context) *scheduledOwnerTopology {
	t.Helper()
	f := newBridgeTopology(t, ctx)
	configPath := filepath.Join(f.dir, "config.yaml")
	configuration, err := os.ReadFile(configPath)
	require.NoError(t, err)
	// AccountTest uses a separate policy from ordinary gateway requests.
	configuration = []byte(strings.Replace(string(configuration), "security:\n", "security:\n  account_probe:\n    allow_private_hosts: true\n", 1))
	require.Contains(t, string(configuration), "account_probe:\n    allow_private_hosts: true")
	require.NoError(t, os.WriteFile(configPath, configuration, 0600))
	for _, command := range []string{"scheduled-owner", "dispatchd"} {
		f.build(t, command)
	}
	s := &scheduledOwnerTopology{f: f, coreDSNFile: filepath.Join(f.dir, "owner-dsn"), dispatchDSNFile: filepath.Join(f.dir, "dispatch-dsn"), configPath: filepath.Join(f.dir, "owner-dispatch.json"), history: map[int64]string{}, staticPlans: map[int64]string{}}
	require.NoError(t, os.WriteFile(s.coreDSNFile, []byte(f.sourceDSN), 0600))
	require.NoError(t, os.WriteFile(s.dispatchDSNFile, []byte(f.sinkDSN), 0600))
	return s
}

func (s *scheduledOwnerTopology) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := s.f.source.ExecContext(s.f.ctx, query, args...)
	require.NoError(t, err)
}

func (s *scheduledOwnerTopology) seed(t *testing.T) *sql.Tx {
	t.Helper()
	s.exec(t, `INSERT INTO settings(key,value) VALUES ('owner_topology_preserved','operator-setting-before-migration')`)
	s.exec(t, `INSERT INTO intelligence_questions(id,prompt,prompt_image,answer_rule,expected_answers,enabled) VALUES
	 ($1,'owner topology question first',$3,'exact','["provider-A"]',true),
	 ($2,'owner topology question second',$3,'exact','["provider-A"]',true)`, ownerTopologyQuestionA, ownerTopologyQuestionB,
		"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4//8/AAX+Av4N70a4AAAAAElFTkSuQmCC")
	for _, plan := range []struct {
		id           int64
		kind, prompt string
	}{{ownerTopologyArtPlan, "char_art", "owner topology draw a square"}, {ownerTopologyQuizPlan, "quiz", ""}, {ownerTopologyUnmanagedPlan, "char_art", "owner topology import while running"}} {
		s.exec(t, `INSERT INTO scheduled_test_plans(id,account_id,model_id,cron_expression,enabled,max_results,auto_recover,kind,prompt,pick_mode,rotation_cursor,show_in_catalog,failure_action,next_run_at)
		 VALUES ($1,704,'gpt-5.4','* * * * *',true,20,false,$2,$3,'rotate',4,true,'none',clock_timestamp()-interval '1 minute')`, plan.id, plan.kind, plan.prompt)
		if plan.id == ownerTopologyUnmanagedPlan {
			continue
		}
		for n := range 24 {
			s.exec(t, `INSERT INTO scheduled_test_results(plan_id,kind,model_id,prompt,response_text,status,started_at,finished_at,latency_ms)
			 VALUES($1,$2,'gpt-5.4','historical prompt',$3,'success',clock_timestamp()-interval '2 days',clock_timestamp()-interval '2 days',1)`, plan.id, plan.kind, fmt.Sprintf("historical-%d-%d", plan.id, n))
		}
	}
	s.exec(t, `INSERT INTO scheduled_test_plan_questions(plan_id,question_id,position) VALUES ($1,$2,0),($1,$3,1)`, ownerTopologyQuizPlan, ownerTopologyQuestionA, ownerTopologyQuestionB)
	for id := int64(ownerTopologyExtraFirst); id <= ownerTopologyCachedPlan; id++ {
		s.exec(t, `INSERT INTO scheduled_test_plans(id,account_id,model_id,cron_expression,enabled,max_results,auto_recover,kind,prompt,pick_mode,show_in_catalog,failure_action,next_run_at)
		 VALUES($1,704,'gpt-5.4','* * * * *',true,20,false,'char_art','owner topology cached task','rotate',true,'none',clock_timestamp()-interval '1 minute')`, id)
	}
	late, err := s.f.source.BeginTx(s.f.ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = late.Rollback() })
	_, err = late.ExecContext(s.f.ctx, `INSERT INTO scheduled_test_results(plan_id,kind,model_id,prompt,response_text,status,started_at,finished_at,latency_ms)
	 VALUES($1,'char_art','gpt-5.4','historical prompt','late-committed-low-id','success',clock_timestamp()-interval '1 day',clock_timestamp()-interval '1 day',1)`, ownerTopologyArtPlan)
	require.NoError(t, err)
	s.exec(t, `INSERT INTO scheduled_test_results(plan_id,kind,model_id,prompt,response_text,status,started_at,finished_at,latency_ms)
	 VALUES($1,'char_art','gpt-5.4','historical prompt','earlier-visible-high-id','success',clock_timestamp()-interval '1 day',clock_timestamp()-interval '1 day',1)`, ownerTopologyArtPlan)
	return late
}

func (s *scheduledOwnerTopology) capture(t *testing.T) {
	t.Helper()
	rows, err := s.f.source.QueryContext(s.f.ctx, `SELECT id,to_jsonb(r)::text FROM scheduled_test_results r ORDER BY id`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		var raw string
		require.NoError(t, rows.Scan(&id, &raw))
		s.history[id] = raw
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	for _, id := range []int64{ownerTopologyArtPlan, ownerTopologyQuizPlan} {
		s.staticPlans[id] = s.staticPlan(t, id)
	}
	s.questions = s.queryJSON(t, `SELECT jsonb_build_object('questions',(SELECT jsonb_agg(to_jsonb(q) ORDER BY q.id) FROM intelligence_questions q),'links',(SELECT jsonb_agg(to_jsonb(l) ORDER BY l.plan_id,l.position) FROM scheduled_test_plan_questions l))::text`)
	s.settings = s.queryJSON(t, `SELECT jsonb_agg(to_jsonb(v) ORDER BY v.key)::text FROM settings v WHERE key IN ('owner_topology_preserved','openai_codex_version_auto_sync_enabled','claude_code_version_auto_sync_enabled')`)
}

func (s *scheduledOwnerTopology) staticPlan(t *testing.T, id int64) string {
	t.Helper()
	return s.queryJSON(t, `SELECT (to_jsonb(p)-ARRAY['last_run_at','next_run_at','updated_at','rotation_cursor'])::text FROM scheduled_test_plans p WHERE id=$1`, id)
}

func (s *scheduledOwnerTopology) queryJSON(t *testing.T, query string, args ...any) string {
	t.Helper()
	var result string
	require.NoError(t, s.f.source.QueryRowContext(s.f.ctx, query, args...).Scan(&result))
	return result
}

func (s *scheduledOwnerTopology) assertPreserved(t *testing.T) {
	t.Helper()
	for id, want := range s.history {
		require.JSONEq(t, want, s.queryJSON(t, `SELECT to_jsonb(r)::text FROM scheduled_test_results r WHERE id=$1`, id), "原历史 id=%d 必须逐列保留", id)
	}
	for id, want := range s.staticPlans {
		require.JSONEq(t, want, s.staticPlan(t, id), "计划设置不能因切换回退被旧快照覆盖")
	}
	require.JSONEq(t, s.questions, s.queryJSON(t, `SELECT jsonb_build_object('questions',(SELECT jsonb_agg(to_jsonb(q) ORDER BY q.id) FROM intelligence_questions q),'links',(SELECT jsonb_agg(to_jsonb(l) ORDER BY l.plan_id,l.position) FROM scheduled_test_plan_questions l))::text`))
	require.JSONEq(t, s.settings, s.queryJSON(t, `SELECT jsonb_agg(to_jsonb(v) ORDER BY v.key)::text FROM settings v WHERE key IN ('owner_topology_preserved','openai_codex_version_auto_sync_enabled','claude_code_version_auto_sync_enabled')`))
}

func (s *scheduledOwnerTopology) startCore(t *testing.T, enabled bool) *bridgeTopologyProcess {
	t.Helper()
	p := s.f.start(t, "server", []string{"SUB2API_BRIDGE_ENABLED=false", "SUB2API_SCHEDULED_OWNER_ENABLED=" + strconv.FormatBool(enabled), "SUB2API_SCHEDULED_OWNER_POLL_INTERVAL=100ms"})
	s.f.waitHTTP(t, p, s.f.coreURL+"/health", http.StatusOK)
	return p
}

func (s *scheduledOwnerTopology) blockProvider() func() {
	gate := make(chan struct{})
	s.f.mu.Lock()
	s.f.providerStreamGate = gate
	s.f.mu.Unlock()
	return sync.OnceFunc(func() { close(gate); s.f.mu.Lock(); s.f.providerStreamGate = nil; s.f.mu.Unlock() })
}

func (s *scheduledOwnerTopology) command(t *testing.T, name string, success bool, env []string, args ...string) []byte {
	t.Helper()
	p := s.f.start(t, name, env, args...)
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
		t.Fatalf("%s 未在边界内退出", name)
	}
	body, err := os.ReadFile(p.logPath)
	require.NoError(t, err)
	for _, secret := range []string{s.f.sourceDSN, s.f.sinkDSN, "topology-synthetic-provider-A", "topology-synthetic-provider-B"} {
		require.NotContains(t, string(body), secret, "控制命令不能输出合成凭据")
	}
	if success {
		require.NoError(t, p.err, "%s: %s", name, body)
	} else {
		require.Error(t, p.err, "%s 必须拒绝不合法转换: %s", name, body)
	}
	t.Logf("actual_cmd_%s pid=%d success=%t output=%s", strings.ReplaceAll(name, "-", "_"), p.cmd.Process.Pid, p.err == nil, strings.TrimSpace(string(body)))
	return body
}

func ownerTopologyWait(t *testing.T, ctx context.Context, bound time.Duration, check func() error) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(bound)
	defer timeout.Stop()
	var last error
	for {
		if last = check(); last == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("真实进程上下文结束: %v; last=%v", ctx.Err(), last)
		case <-timeout.C:
			t.Fatalf("真实进程状态超时: %v", last)
		case <-ticker.C:
		}
	}
}

func (s *scheduledOwnerTopology) waitResults(t *testing.T, planID int64, want int) {
	t.Helper()
	ownerTopologyWait(t, s.f.ctx, 85*time.Second, func() error {
		var count int
		if err := s.f.source.QueryRowContext(s.f.ctx, `SELECT COUNT(*) FROM scheduled_test_results WHERE plan_id=$1 AND response_text='provider-A' AND status='success'`, planID).Scan(&count); err != nil {
			return err
		}
		if count != want {
			return fmt.Errorf("plan=%d 实际新成功结果=%d want=%d", planID, count, want)
		}
		return nil
	})
}

func (s *scheduledOwnerTopology) resultCount(t *testing.T, planID int64) int {
	t.Helper()
	var count int
	require.NoError(t, s.f.source.QueryRowContext(s.f.ctx, `SELECT COUNT(*) FROM scheduled_test_results WHERE plan_id=$1`, planID).Scan(&count))
	return count
}

func (s *scheduledOwnerTopology) requireNoDuplicates(t *testing.T) {
	t.Helper()
	var duplicates int
	require.NoError(t, s.f.source.QueryRowContext(s.f.ctx, `SELECT COUNT(*) FROM (SELECT plan_id,slot FROM scheduled_owner.runs GROUP BY plan_id,slot HAVING COUNT(*)>1) d`).Scan(&duplicates))
	require.Zero(t, duplicates, "slot 唯一性必须跨 owner 和 epoch")
}

func ownerTopologyIDs(ids ...int64) string {
	var values []string
	for _, id := range ids {
		values = append(values, strconv.FormatInt(id, 10))
	}
	return strings.Join(values, ",")
}

func (s *scheduledOwnerTopology) control(t *testing.T, success bool, args ...string) json.RawMessage {
	t.Helper()
	args = append(args, "--dsn-file", s.coreDSNFile)
	body := s.command(t, "scheduled-owner", success, nil, args...)
	if !success {
		return nil
	}
	var response struct {
		DatabaseID string          `json:"database_id"`
		Result     json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	require.NotEmpty(t, response.DatabaseID)
	if s.databaseID != "" {
		require.Equal(t, s.databaseID, response.DatabaseID)
	} else {
		s.databaseID = response.DatabaseID
	}
	return response.Result
}

func (s *scheduledOwnerTopology) status(t *testing.T, id int64) ownerTopologyStatus {
	t.Helper()
	var values []ownerTopologyStatus
	require.NoError(t, json.Unmarshal(s.control(t, true, "--status", "--plans", ownerTopologyIDs(id)), &values))
	require.Len(t, values, 1)
	return values[0]
}

func (s *scheduledOwnerTopology) change(t *testing.T, action string, current ownerTopologyStatus, success bool, extra ...string) ownerTopologyStatus {
	t.Helper()
	args := []string{action, "--plan", strconv.FormatInt(current.PlanID, 10), "--epoch", strconv.FormatInt(current.Epoch, 10), "--database-id", s.databaseID}
	args = append(args, extra...)
	result := s.control(t, success, args...)
	if !success {
		return current
	}
	var status ownerTopologyStatus
	require.NoError(t, json.Unmarshal(result, &status))
	require.Equal(t, current.PlanID, status.PlanID)
	require.Equal(t, s.databaseID, status.DatabaseID)
	return status
}

func (s *scheduledOwnerTopology) configureDispatch(t *testing.T, statuses ...ownerTopologyStatus) {
	t.Helper()
	bindings := make([]map[string]any, 0, len(statuses))
	for _, status := range statuses {
		bindings = append(bindings, map[string]any{"plan_id": status.PlanID, "owner_epoch": status.Epoch})
	}
	s.configRevision++
	s.f.writeJSON(t, filepath.Base(s.configPath), map[string]any{"version": 1, "config_revision": s.configRevision, "core_instance": "scheduled-owner-topology", "snapshot_file": filepath.Join(s.f.dir, "owner-snapshot.json"), "snapshot_ttl_seconds": 3, "poll_interval_milliseconds": 100, "targets": []any{}, "scheduled": map[string]any{"database_id": s.databaseID, "bindings": bindings}})
	if s.dispatchProxy == nil {
		u, err := url.Parse(s.f.sourceDSN)
		require.NoError(t, err)
		s.exec(t, `CREATE ROLE owner_topology_queue LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT PASSWORD 'owner-topology-synthetic-only';
		 GRANT USAGE ON SCHEMA scheduled_owner TO owner_topology_queue;
		 GRANT SELECT ON scheduled_owner.database_identity,scheduled_owner.schema_migrations,scheduled_owner.plans,scheduled_owner.runs,
		 public.scheduled_test_plans,public.scheduled_test_plan_questions,public.intelligence_questions TO owner_topology_queue;
		 GRANT INSERT ON scheduled_owner.queue TO owner_topology_queue`)
		u.User = url.UserPassword("owner_topology_queue", "owner-topology-synthetic-only")
		s.queueReader, err = sql.Open("postgres", u.String())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.queueReader.Close()) })
		require.NoError(t, s.queueReader.PingContext(s.f.ctx))
		s.dispatchProxy = newBridgeObservationProxy(t, u.Host)
		u.Host = s.dispatchProxy.listener.Addr().String()
		q := u.Query()
		q.Set("connect_timeout", "2")
		u.RawQuery = q.Encode()
		require.NoError(t, os.WriteFile(filepath.Join(s.f.dir, "scheduled-source-dsn"), []byte(u.String()), 0600))
	}
}

func (s *scheduledOwnerTopology) assertQueueRole(t *testing.T) {
	t.Helper()
	for _, query := range []string{
		`SELECT credentials FROM public.accounts`,
		`SELECT response_text FROM public.scheduled_test_results`,
		`UPDATE public.scheduled_test_plans SET enabled=false WHERE id=901`,
		`UPDATE scheduled_owner.plans SET owner='dispatch' WHERE plan_id=901`,
		`INSERT INTO scheduled_owner.runs(plan_id,slot,epoch,owner,state,plan_digest) VALUES(901,clock_timestamp(),1,'dispatch','completed','denied')`,
		`CREATE TABLE public.owner_topology_denied(id bigint)`,
	} {
		_, err := s.queueReader.ExecContext(s.f.ctx, query)
		require.Error(t, err, query)
		var pg *pq.Error
		require.True(t, errors.As(err, &pg))
		require.Equal(t, pq.ErrorCode("42501"), pg.Code, query)
	}
	_, err := s.queueReader.ExecContext(s.f.ctx, `INSERT INTO scheduled_owner.queue(plan_id,slot,epoch,owner,state,plan_digest) VALUES(901,clock_timestamp(),1,'dispatch','completed','denied')`)
	require.Error(t, err, "queue view 不能伪造完成状态")
	var pg *pq.Error
	require.True(t, errors.As(err, &pg))
	require.Equal(t, pq.ErrorCode("44000"), pg.Code)
	t.Log("actual_queue_role=restricted credentials_read_denied=true historical_results_read_denied=true public_write_denied=true owner_write_denied=true completed_insert_denied=true view_check_enforced=true")
}

func (s *scheduledOwnerTopology) dispatchEnv() []string {
	return []string{"DISPATCH_SCHEDULED_DSN_FILE=" + filepath.Join(s.f.dir, "scheduled-source-dsn")}
}

func (s *scheduledOwnerTopology) dispatchOnce(t *testing.T, success bool) dispatch.RoundResult {
	t.Helper()
	body := s.command(t, "dispatchd", success, s.dispatchEnv(), "--once", "--config", s.configPath, "--dsn-file", s.dispatchDSNFile)
	var result dispatch.RoundResult
	if success {
		require.NoError(t, json.Unmarshal(body, &result))
	}
	return result
}

func (s *scheduledOwnerTopology) startDispatch(t *testing.T) *bridgeTopologyProcess {
	t.Helper()
	p := s.f.start(t, "dispatchd", s.dispatchEnv(), "--run", "--config", s.configPath, "--dsn-file", s.dispatchDSNFile)
	ownerTopologyWait(t, s.f.ctx, 20*time.Second, func() error {
		select {
		case <-p.done:
			return fmt.Errorf("实际 dispatchd 在首个工作轮次前退出: %v", p.err)
		default:
		}
		body, err := os.ReadFile(p.logPath)
		if err != nil {
			return err
		}
		if !strings.Contains(string(body), `"revision":`) {
			return fmt.Errorf("等待实际 dispatchd 首个成功工作轮次")
		}
		return nil
	})
	t.Logf("actual_cmd_dispatchd pid=%d explicit_scheduled_source=true", p.cmd.Process.Pid)
	return p
}

func (s *scheduledOwnerTopology) waitRunning(t *testing.T, ids []int64, owner string) {
	t.Helper()
	ownerTopologyWait(t, s.f.ctx, 85*time.Second, func() error {
		for _, id := range ids {
			var count int
			err := s.f.source.QueryRowContext(s.f.ctx, `SELECT COUNT(*) FROM scheduled_owner.runs WHERE plan_id=$1 AND owner=$2 AND state='running'`, id, owner).Scan(&count)
			if err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("plan=%d owner=%s running=%d", id, owner, count)
			}
		}
		return nil
	})
}

func (s *scheduledOwnerTopology) waitDrained(t *testing.T, ids []int64) {
	t.Helper()
	ownerTopologyWait(t, s.f.ctx, 20*time.Second, func() error {
		for _, id := range ids {
			var count int
			err := s.f.source.QueryRowContext(s.f.ctx, `SELECT COUNT(*) FROM scheduled_owner.runs WHERE plan_id=$1 AND state IN ('queued','running')`, id).Scan(&count)
			if err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("plan=%d pending=%d", id, count)
			}
		}
		return nil
	})
}

func (s *scheduledOwnerTopology) assertRunResults(t *testing.T, planID int64, want int) {
	t.Helper()
	var completed, linked int
	require.NoError(t, s.f.source.QueryRowContext(s.f.ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE r.result_id IS NOT NULL AND x.id IS NOT NULL AND x.plan_id=r.plan_id AND x.response_text='provider-A' AND x.status='success') FROM scheduled_owner.runs r LEFT JOIN scheduled_test_results x ON x.id=r.result_id WHERE r.plan_id=$1 AND r.state='completed'`, planID).Scan(&completed, &linked))
	require.Equal(t, want, completed)
	require.Equal(t, want, linked, "每个持久成功 slot 必须关联真实 Core 写入的旧 result")
}

func (s *scheduledOwnerTopology) adminRequest(t *testing.T, path string, body string) []byte {
	t.Helper()
	var user service.User
	require.NoError(t, s.f.source.QueryRowContext(s.f.ctx, `SELECT id,email,password_hash,role FROM users WHERE id=701`).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role))
	cfg := &config.Config{}
	cfg.JWT.Secret, cfg.JWT.ExpireHour = strings.Repeat("t", 40), 1
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	token, err := auth.GenerateToken(s.f.ctx, &user)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(s.f.ctx, http.MethodPut, s.f.coreURL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.f.client.Do(req)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	result, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, string(result))
	return result
}
