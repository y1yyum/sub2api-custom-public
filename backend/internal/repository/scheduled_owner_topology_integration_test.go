//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 所有执行均来自真实 cmd/server；迁移、切主和入队分别来自真实管理命令。
// SQL 只种子、查询事实及模拟晚提交，不手工写 owner/runs 的终态。
func TestScheduledOwnerTopology(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("真实 Core 和 SIGKILL 演练要求 Linux")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 9*time.Minute)
	defer cancel()
	s := newScheduledOwnerTopology(t, ctx)
	f := s.f
	var art, quiz ownerTopologyStatus
	if !t.Run("default_off_needs_no_owner_schema", func(t *testing.T) {
		f.core = s.startCore(t, false)
		var schemaExists bool
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT to_regnamespace('scheduled_owner') IS NOT NULL`).Scan(&schemaExists))
		require.False(t, schemaExists)
		s.command(t, "scheduled-owner", true, []string{"SCHEDULED_OWNER_DSN=invalid"})
		s.command(t, "dispatchd", true, []string{"DISPATCH_DSN=invalid", "DISPATCH_SCHEDULED_DSN=invalid"})
		f.core.stop(t)
		t.Logf("actual_cmd_server pid=%d owner_default_off=true owner_schema_absent=true", f.core.cmd.Process.Pid)
	}) {
		return
	}
	late := s.seed(t)
	if !t.Run("explicit_import_is_idempotent_and_rescans_late_commit", func(t *testing.T) {
		s.control(t, true, "--apply-schema")
		databaseID := s.databaseID
		s.control(t, true, "--apply-schema")
		require.Equal(t, databaseID, s.databaseID)
		raw := s.control(t, true, "--import", "--plans", ownerTopologyIDs(ownerTopologyArtPlan, ownerTopologyQuizPlan), "--database-id", s.databaseID)
		var statuses []ownerTopologyStatus
		require.NoError(t, json.Unmarshal(raw, &statuses))
		require.Len(t, statuses, 2)
		art, quiz = statuses[0], statuses[1]
		var extraIDs []int64
		for id := int64(ownerTopologyExtraFirst); id <= ownerTopologyCachedPlan; id++ {
			extraIDs = append(extraIDs, id)
		}
		s.control(t, true, "--import", "--plans", ownerTopologyIDs(extraIDs...), "--database-id", s.databaseID)
		require.EqualValues(t, 25, art.ResultsCount)
		require.EqualValues(t, 24, quiz.ResultsCount)
		require.EqualValues(t, 1, art.Epoch)
		maxID, digest := art.ResultsMaxID, art.ResultsDigest
		s.control(t, true, "--import", "--plans", ownerTopologyIDs(ownerTopologyArtPlan, ownerTopologyQuizPlan), "--database-id", s.databaseID)
		require.Equal(t, art, s.status(t, ownerTopologyArtPlan))
		require.NoError(t, late.Commit())
		s.capture(t)
		art = s.change(t, "--freeze", art, true)
		art = s.change(t, "--activate", art, true, "--owner", "legacy")
		require.EqualValues(t, 26, art.ResultsCount)
		require.Equal(t, maxID, art.ResultsMaxID, "晚提交低 ID 必须改变 count/digest 而非 max ID")
		require.NotEqual(t, digest, art.ResultsDigest)
		s.assertPreserved(t)
		t.Logf("late_commit_low_id_included=true count=26 unchanged_max_id=%d digest_changed=true imported_plans=2 original_history_rows=%d", maxID, len(s.history))
	}) {
		return
	}
	release := s.blockProvider()
	defer release()
	f.core = s.startCore(t, true)
	if !t.Run("legacy_inflight_freeze_blocks_switch_and_import", func(t *testing.T) {
		activeIDs := []int64{ownerTopologyArtPlan, ownerTopologyQuizPlan, ownerTopologyUnmanagedPlan}
		for id := int64(ownerTopologyExtraFirst); id < ownerTopologyCachedPlan; id++ {
			activeIDs = append(activeIDs, id)
		}
		s.waitRunning(t, activeIDs, "legacy")
		ownerTopologyWait(t, ctx, 10*time.Second, func() error {
			if n := f.providerCount("A"); n != ownerTopologyLegacyCalls {
				return fmt.Errorf("实际 legacy Provider calls=%d want=%d", n, ownerTopologyLegacyCalls)
			}
			return nil
		})
		s.control(t, false, "--import", "--plans", ownerTopologyIDs(ownerTopologyUnmanagedPlan), "--database-id", s.databaseID)
		art = s.change(t, "--freeze", art, true)
		quiz = s.change(t, "--freeze", quiz, true)
		cached := s.status(t, ownerTopologyCachedPlan)
		s.change(t, "--freeze", cached, true)
		require.Equal(t, "draining", art.State)
		s.change(t, "--activate", art, false, "--owner", "dispatch")
		s.change(t, "--activate", quiz, false, "--owner", "dispatch")
		require.Equal(t, 26, s.resultCount(t, ownerTopologyArtPlan))
		require.Equal(t, 24, s.resultCount(t, ownerTopologyQuizPlan))
		release()
		s.waitResults(t, ownerTopologyArtPlan, 1)
		s.waitResults(t, ownerTopologyQuizPlan, 1)
		s.waitResults(t, ownerTopologyUnmanagedPlan, 1)
		s.waitDrained(t, activeIDs)
		for id := int64(ownerTopologyExtraFirst); id < ownerTopologyCachedPlan; id++ {
			s.waitResults(t, id, 1)
			current := s.status(t, id)
			s.change(t, "--freeze", current, true)
		}
		require.Never(t, func() bool {
			return s.resultCount(t, ownerTopologyCachedPlan) != 0 || f.providerCount("A") != ownerTopologyLegacyCalls
		}, time.Second, 50*time.Millisecond, "第11个已被legacy ListDue缓存的任务必须在取得worker后重新通过fence")
		var cachedRuns int
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_owner.runs WHERE plan_id=$1`, ownerTopologyCachedPlan).Scan(&cachedRuns))
		require.Zero(t, cachedRuns)
		s.assertRunResults(t, ownerTopologyArtPlan, 1)
		s.assertRunResults(t, ownerTopologyQuizPlan, 1)
		s.control(t, true, "--import", "--plans", ownerTopologyIDs(ownerTopologyUnmanagedPlan), "--database-id", s.databaseID)
		other := s.status(t, ownerTopologyUnmanagedPlan)
		s.change(t, "--freeze", other, true)
		art = s.change(t, "--activate", art, true, "--owner", "dispatch")
		quiz = s.change(t, "--activate", quiz, true, "--owner", "dispatch")
		require.Equal(t, "dispatch", art.Owner)
		require.EqualValues(t, 27, art.ResultsCount)
		require.EqualValues(t, 25, quiz.ResultsCount)
		s.assertPreserved(t)
		t.Logf("actual_legacy_cron=true provider_calls=%d cached_due_plans=11 blocked_workers=10 frozen_cached_plan_calls=0 import_running_rejected=true activation_while_locked_rejected=true dispatch_epoch_art=%d dispatch_epoch_quiz=%d", ownerTopologyLegacyCalls, art.Epoch, quiz.Epoch)
	}) {
		return
	}
	s.command(t, "dispatchd", true, nil, "--apply-schema", "--dsn-file", s.dispatchDSNFile)
	s.configureDispatch(t, art, quiz)
	s.assertQueueRole(t)
	release = s.blockProvider()
	defer release()
	first, second := s.startDispatch(t), s.startDispatch(t)
	if !t.Run("two_dispatchers_restart_and_source_outage_never_duplicate", func(t *testing.T) {
		s.waitRunning(t, []int64{ownerTopologyArtPlan, ownerTopologyQuizPlan}, "dispatch")
		ownerTopologyWait(t, ctx, 10*time.Second, func() error {
			if n := f.providerCount("A"); n != ownerTopologyLegacyCalls+2 {
				return fmt.Errorf("实际 Provider calls=%d want=%d", n, ownerTopologyLegacyCalls+2)
			}
			return nil
		})
		killed := first.kill(t)
		first = s.startDispatch(t)
		require.NotEqual(t, killed, first.cmd.Process.Pid)
		s.requireNoDuplicates(t)
		require.Equal(t, ownerTopologyLegacyCalls+2, f.providerCount("A"))
		s.dispatchProxy.block(true)
		defer s.dispatchProxy.block(false)
		s.dispatchOnce(t, false)
		for _, p := range []*bridgeTopologyProcess{first, second} {
			ownerTopologyWait(t, ctx, 10*time.Second, func() error {
				body, err := os.ReadFile(p.logPath)
				if err != nil {
					return err
				}
				if !strings.Contains(string(body), "现有快照不延长有效期") {
					return fmt.Errorf("等待实际 dispatchd 断库失败输出")
				}
				return nil
			})
		}
		snapshotFile := filepath.Join(f.dir, "owner-snapshot.json")
		before, err := os.ReadFile(snapshotFile)
		require.NoError(t, err)
		var freshness struct {
			ExpiresAt time.Time `json:"expires_at"`
		}
		require.NoError(t, json.Unmarshal(before, &freshness))
		ownerTopologyWait(t, ctx, 5*time.Second, func() error {
			if time.Now().Before(freshness.ExpiresAt) {
				return fmt.Errorf("等待断库前快照自然到期")
			}
			return nil
		})
		after, err := os.ReadFile(snapshotFile)
		require.NoError(t, err)
		require.Equal(t, before, after, "断库不能发布或续期快照")
		require.Equal(t, ownerTopologyLegacyCalls+2, f.providerCount("A"))
		s.requireNoDuplicates(t)
		s.dispatchProxy.block(false)
		t.Logf("actual_dispatchd_SIGKILL old_pid=%d fresh_pid=%d concurrent_dispatchers=2 scheduled_database_outage=true expired_snapshot_unchanged=true provider_calls=%d duplicate_slots=0", killed, first.cmd.Process.Pid, ownerTopologyLegacyCalls+2)
	}) {
		return
	}
	if !t.Run("drain_dispatch_and_rollback_legacy_preserves_progress", func(t *testing.T) {
		art = s.change(t, "--freeze", art, true)
		quiz = s.change(t, "--freeze", quiz, true)
		s.change(t, "--activate", art, false, "--owner", "legacy")
		s.change(t, "--activate", quiz, false, "--owner", "legacy")
		first.stop(t)
		second.stop(t)
		release()
		s.waitResults(t, ownerTopologyArtPlan, 2)
		s.waitResults(t, ownerTopologyQuizPlan, 2)
		s.waitDrained(t, []int64{ownerTopologyArtPlan, ownerTopologyQuizPlan})
		s.assertRunResults(t, ownerTopologyArtPlan, 2)
		s.assertRunResults(t, ownerTopologyQuizPlan, 2)
		var beforeNext time.Time
		var cursor int
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT next_run_at,rotation_cursor FROM scheduled_test_plans WHERE id=$1`, ownerTopologyQuizPlan).Scan(&beforeNext, &cursor))
		require.Equal(t, 6, cursor)
		previousEpoch := art.Epoch
		art = s.change(t, "--activate", art, true, "--owner", "legacy")
		quiz = s.change(t, "--activate", quiz, true, "--owner", "legacy")
		require.Greater(t, art.Epoch, previousEpoch)
		s.assertPreserved(t)
		f.core.stop(t)
		f.core = s.startCore(t, true)
		s.waitResults(t, ownerTopologyArtPlan, 3)
		s.waitResults(t, ownerTopologyQuizPlan, 3)
		s.waitDrained(t, []int64{ownerTopologyArtPlan, ownerTopologyQuizPlan})
		s.assertRunResults(t, ownerTopologyArtPlan, 3)
		s.assertRunResults(t, ownerTopologyQuizPlan, 3)
		var afterNext time.Time
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT next_run_at,rotation_cursor FROM scheduled_test_plans WHERE id=$1`, ownerTopologyQuizPlan).Scan(&afterNext, &cursor))
		require.Equal(t, 7, cursor)
		require.True(t, afterNext.After(beforeNext))
		require.Equal(t, 29, s.resultCount(t, ownerTopologyArtPlan))
		require.Equal(t, 27, s.resultCount(t, ownerTopologyQuizPlan))
		var questions string
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT string_agg(question_id::text,',' ORDER BY id) FROM scheduled_test_results WHERE plan_id=$1 AND response_text='provider-A'`, ownerTopologyQuizPlan).Scan(&questions))
		require.Equal(t, fmt.Sprintf("%d,%d,%d", ownerTopologyQuestionA, ownerTopologyQuestionB, ownerTopologyQuestionA), questions)
		var graded, drawn int
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_results WHERE plan_id=$1 AND kind='quiz' AND model_id='gpt-5.4' AND response_text='provider-A' AND expected_answer='provider-A' AND verdict='pass'`, ownerTopologyQuizPlan).Scan(&graded))
		require.Equal(t, 3, graded, "真实 Core 必须执行旧题库判题")
		require.NoError(t, f.source.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_results WHERE plan_id=$1 AND kind='char_art' AND model_id='gpt-5.4' AND prompt='owner topology draw a square' AND response_text='provider-A' AND expected_answer='' AND verdict=''`, ownerTopologyArtPlan).Scan(&drawn))
		require.Equal(t, 3, drawn, "char_art 结果与 quiz 判题不可混淆")
		s.assertPreserved(t)
		s.requireNoDuplicates(t)
		require.Equal(t, ownerTopologyLegacyCalls+4, f.providerCount("A"))
		require.Zero(t, f.providerCount("B"))
		f.core.stop(t)
		t.Logf("actual_core_restart_pid=%d normal_rollback=legacy legacy_resumed_new_slots=2 quiz_cursor=7 quiz_question_order=%s original_history_rows=%d preserved=true provider_A=%d provider_B=0", f.core.cmd.Process.Pid, questions, len(s.history), ownerTopologyLegacyCalls+4)
	}) {
		return
	}
	if !t.Run("kill_core_inflight_requires_explicit_unknown_and_new_due", func(t *testing.T) { s.killCoreScenario(t) }) {
		return
	}
}

func (s *scheduledOwnerTopology) killCoreScenario(t *testing.T) {
	t.Helper()
	f := s.f
	s.exec(t, `INSERT INTO scheduled_test_plans(id,account_id,model_id,cron_expression,enabled,max_results,auto_recover,kind,prompt,pick_mode,show_in_catalog,failure_action,next_run_at) VALUES ($1,704,'gpt-5.4','* * * * *',true,20,false,'char_art','owner topology crash window','rotate',true,'none',clock_timestamp()-interval '1 minute')`, ownerTopologyCrashPlan)
	for _, id := range []int64{ownerTopologyArtPlan, ownerTopologyQuizPlan} {
		current := s.status(t, id)
		s.change(t, "--freeze", current, true)
	}
	s.control(t, true, "--import", "--plans", ownerTopologyIDs(ownerTopologyCrashPlan), "--database-id", s.databaseID)
	status := s.status(t, ownerTopologyCrashPlan)
	status = s.change(t, "--freeze", status, true)
	status = s.change(t, "--activate", status, true, "--owner", "dispatch")
	s.configureDispatch(t, status)
	var due time.Time
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT next_run_at FROM scheduled_test_plans WHERE id=$1`, ownerTopologyCrashPlan).Scan(&due))
	release := s.blockProvider()
	defer release()
	f.core = s.startCore(t, true)
	before := f.providerCount("A")
	require.Equal(t, 1, s.dispatchOnce(t, true).ScheduledPlans)
	s.waitRunning(t, []int64{ownerTopologyCrashPlan}, "dispatch")
	ownerTopologyWait(t, f.ctx, 10*time.Second, func() error {
		if f.providerCount("A") != before+1 {
			return fmt.Errorf("等待实际 Core 出站已到阻塞 Provider")
		}
		return nil
	})
	status = s.change(t, "--freeze", status, true)
	killed := f.core.kill(t)
	release()
	s.change(t, "--activate", status, false, "--owner", "legacy")
	s.change(t, "--ack-unknown", status, false, "--reason", "")
	reason := "isolated drill: Core SIGKILL after synthetic provider accepted; original due remains non-replayable"
	status = s.change(t, "--ack-unknown", status, true, "--reason", reason)
	require.Zero(t, status.Running)
	require.Zero(t, s.resultCount(t, ownerTopologyCrashPlan))
	var state string
	var slot time.Time
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT state,slot FROM scheduled_owner.runs WHERE plan_id=$1`, ownerTopologyCrashPlan).Scan(&state, &slot))
	require.Equal(t, "unknown", state)
	require.True(t, due.UTC().Truncate(time.Minute).Equal(slot))
	var audit int
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM scheduled_owner.audit WHERE plan_id=$1 AND action='ack_unknown' AND reason=$2`, ownerTopologyCrashPlan, reason).Scan(&audit))
	require.Equal(t, 1, audit)
	status = s.change(t, "--activate", status, true, "--owner", "legacy")
	f.core = s.startCore(t, true)
	require.NotEqual(t, killed, f.core.cmd.Process.Pid)
	// 跨一个真实 legacy cron tick；旧 due 即使跨分钟仍不能变成新 slot。
	nextTick := time.Now().UTC().Truncate(time.Minute).Add(time.Minute + 11*time.Second)
	ownerTopologyWait(t, f.ctx, 85*time.Second, func() error {
		if time.Now().Before(nextTick) {
			return fmt.Errorf("等待重启旧 owner 经过下一真实 cron tick")
		}
		return nil
	})
	require.Equal(t, before+1, f.providerCount("A"))
	require.Zero(t, s.resultCount(t, ownerTopologyCrashPlan))
	var next time.Time
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT next_run_at FROM scheduled_test_plans WHERE id=$1`, ownerTopologyCrashPlan).Scan(&next))
	require.True(t, due.Equal(next), "Unknown ACK 不得悄悄重排业务计划")
	status = s.change(t, "--freeze", status, true)
	s.exec(t, `UPDATE users SET role='admin' WHERE id=701`)
	ack, err := json.Marshal(service.AdminComplianceAcknowledgement{Version: service.AdminComplianceVersion, AdminUserID: 701, AcceptedAt: time.Now().UTC()})
	require.NoError(t, err)
	s.exec(t, `INSERT INTO settings(key,value) VALUES ('admin_compliance_acknowledgement:701',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, string(ack))
	s.adminRequest(t, fmt.Sprintf("/api/v1/admin/scheduled-test-plans/%d", ownerTopologyCrashPlan), `{"cron_expression":"* * * * *"}`)
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT next_run_at FROM scheduled_test_plans WHERE id=$1`, ownerTopologyCrashPlan).Scan(&next))
	require.True(t, next.After(due))
	s.change(t, "--activate", status, false, "--owner", "legacy")
	status = s.change(t, "--review-edit", status, true, "--reason", "isolated drill: original Core administrator explicitly rescheduled after Unknown acknowledgement")
	status = s.change(t, "--activate", status, true, "--owner", "legacy")
	s.waitResults(t, ownerTopologyCrashPlan, 1)
	s.waitDrained(t, []int64{ownerTopologyCrashPlan})
	s.assertRunResults(t, ownerTopologyCrashPlan, 1)
	var oldUnknown, newComplete int
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FILTER (WHERE state='unknown' AND slot=$2),COUNT(*) FILTER (WHERE state='completed' AND slot=$3) FROM scheduled_owner.runs WHERE plan_id=$1`, ownerTopologyCrashPlan, slot, next.UTC().Truncate(time.Minute)).Scan(&oldUnknown, &newComplete))
	require.Equal(t, 1, oldUnknown)
	require.Equal(t, 1, newComplete)
	require.Equal(t, before+2, f.providerCount("A"))
	s.requireNoDuplicates(t)
	s.assertPreserved(t)
	var completed, unknown, results int
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FILTER (WHERE state='completed'),COUNT(*) FILTER (WHERE state='unknown') FROM scheduled_owner.runs`).Scan(&completed, &unknown))
	require.Equal(t, 15, completed)
	require.Equal(t, 1, unknown)
	require.NoError(t, f.source.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM scheduled_test_results`).Scan(&results))
	require.Equal(t, 65, results)
	f.core.stop(t)
	t.Logf("actual_cmd_server_SIGKILL pid=%d fresh_pid=%d blocked_provider_call=1 result_before_ack=0 unknown_ack_audit=1 old_due_cross_minute_replays=0 original_Core_admin_reschedule=true new_legacy_slot_completed=1 epoch=%d", killed, f.core.cmd.Process.Pid, status.Epoch)
}
