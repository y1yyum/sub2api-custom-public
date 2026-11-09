package edge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
)

func TestEdgeStrictSelectsOnlyPermittedSignedAccount(t *testing.T) {
	verifier := fixtureVerifier(t)
	envelopes := make(chan custombridge.ForwardEnvelope, 2)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
		if err != nil {
			t.Error(err)
			w.WriteHeader(401)
			return
		}
		if envelope.AccountID != 7 || envelope.InternalKeyID != 101 || envelope.GroupID != 7 ||
			envelope.PoolRef != "pool-fixture" || envelope.PolicyRevision != "policy-1" ||
			envelope.PayerRef != "payer-fixture" || envelope.ActorRef != "actor-fixture" ||
			r.Header.Get("Authorization") != "Bearer "+testCoreKey {
			t.Error("strict 选号改变了授权边界")
		}
		for _, name := range []string{"X-Account-ID", "X-Region", "X-Bridge-Forged", "Idempotency-Key", "X-Idempotency-Key"} {
			if r.Header.Get(name) != "" {
				t.Errorf("客户端路由头被转发: %s", name)
			}
		}
		envelopes <- envelope
		if strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: permitted-a\n\n")
		} else {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"error":"permitted-a"}`)
		}
	}))
	defer core.Close()
	cfg := strictConfig(t, core.URL)
	writeShadowSnapshot(t, cfg.StrictDispatch.SnapshotFile, shadowSnapshot(time.Now()))
	handler, server := newTestEdge(t, cfg)
	handler.strict.cache.close()
	if err := os.Remove(cfg.StrictDispatch.SnapshotFile); err != nil {
		t.Fatal(err)
	}
	// 调用方随后改配置，不得影响冻结的授权或请求路径。
	cfg.Grants[0].StrictTargets[0].AccountID = 999
	cfg.StrictDispatch.CoreInstance = "forged-core"
	before := handler.StrictStats()
	for _, body := range []string{strictBody, strings.TrimSuffix(strictBody, "}") + `,"stream":true}`} {
		response := edgePost(t, server.URL, body, http.Header{
			"X-Account-ID": {"999"}, "X-Region": {"forged-region"}, "X-Bridge-Forged": {"forged"},
			"Idempotency-Key": {"stable-key"}, "X-Idempotency-Key": {"forged"},
		})
		result := readBody(t, response)
		if strings.Contains(body, `"stream":true`) {
			if response.StatusCode != 200 || result != "data: permitted-a\n\n" {
				t.Fatal("strict SSE 改写")
			}
		} else if response.StatusCode != 429 || result != `{"error":"permitted-a"}` || response.Header.Get("Retry-After") != "3" {
			t.Fatal("strict 错误响应改写或 fallback")
		}
	}
	first, second := <-envelopes, <-envelopes
	if first.AttemptID != second.AttemptID || first.ReservationID != second.ReservationID || first.RequestID == second.RequestID {
		t.Fatal("strict 破坏原持久幂等身份")
	}
	stats := waitStrictStats(t, handler, func(s StrictStats) bool { return s.Released == 2 })
	if stats.Selected != 2 || stats.Inflight != 0 || stats.Unavailable != 0 || stats.RefreshFailures != before.RefreshFailures || stats.RefreshSuccesses != before.RefreshSuccesses || handler.ShadowStats() != (ShadowStats{}) {
		t.Fatalf("strict 统计错误或请求读取文件: %+v", stats)
	}
}

func TestEdgeStrictUnavailableNeverFallsBack(t *testing.T) {
	for name, change := range map[string]func(*dispatch.Snapshot){
		"no_snapshot": nil,
		"expired": func(s *dispatch.Snapshot) {
			s.GeneratedAt = time.Now().Add(-25 * time.Second)
			s.ExpiresAt = time.Now().Add(-time.Second)
		},
		"candidate_ref":       func(s *dispatch.Snapshot) { s.Candidates[0].Ref = "forged" },
		"account":             func(s *dispatch.Snapshot) { s.Candidates[0].AccountID = 42 },
		"credential":          func(s *dispatch.Snapshot) { s.Candidates[0].CredentialRef = "forged" },
		"credential_revision": func(s *dispatch.Snapshot) { s.Candidates[0].CredentialRevision++ },
		"provider":            func(s *dispatch.Snapshot) { s.Candidates[0].ProviderRef = "forged" },
		"model":               func(s *dispatch.Snapshot) { s.Candidates[0].Model = "other" },
		"pool":                func(s *dispatch.Snapshot) { s.Candidates[0].PoolRef = "other" },
		"policy":              func(s *dispatch.Snapshot) { s.Candidates[0].PolicyRevision = "other" },
		"region":              func(s *dispatch.Snapshot) { s.Candidates[0].Region = "other" },
		"full":                func(s *dispatch.Snapshot) { s.Candidates[0].Active = 1 },
		"tripped": func(s *dispatch.Snapshot) {
			s.Candidates[0].State = dispatch.Tripped
			s.Candidates[0].EffectiveWeight = 0
		},
		"disabled": func(s *dispatch.Snapshot) { s.Candidates[0].Enabled = false; s.Candidates[0].EffectiveWeight = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) }))
			defer core.Close()
			cfg := strictConfig(t, core.URL)
			handler, server := newTestEdge(t, cfg)
			handler.strict.cache.close()
			if change != nil {
				snapshot := shadowSnapshot(time.Now())
				change(&snapshot)
				if err := handler.strict.cache.selector.Update(snapshot, snapshot.GeneratedAt.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			response := edgePost(t, server.URL, strictBody, nil)
			result := readBody(t, response)
			if response.StatusCode != 503 || !strings.Contains(result, `"code":"strict_dispatch_unavailable"`) || calls.Load() != 0 {
				t.Fatalf("strict 失败回退或响应不正确: %d %s calls=%d", response.StatusCode, result, calls.Load())
			}
			stats := handler.StrictStats()
			if stats.Acquisitions != 1 || stats.Unavailable != 1 || stats.Selected != 0 || stats.Inflight != 0 {
				t.Fatalf("失败统计错误: %+v", stats)
			}
			switch name {
			case "no_snapshot":
				if stats.SnapshotUnavailable != 1 {
					t.Fatal("没有无快照原因")
				}
			case "expired":
				if stats.SnapshotExpired != 1 {
					t.Fatal("没有到期原因")
				}
			default:
				if stats.NoEligibleRoute != 1 {
					t.Fatal("没有不可选原因")
				}
			}
		})
	}
}

func waitStrictStats(t *testing.T, handler *Handler, condition func(StrictStats) bool) StrictStats {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		stats := handler.StrictStats()
		if condition(stats) {
			return stats
		}
		if time.Now().After(deadline) {
			t.Fatalf("strict 统计未达到预期: %+v", stats)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEdgeStrictReservationHeldThroughStreamAndCancel(t *testing.T) {
	for _, ending := range []string{"complete", "cancel", "truncated"} {
		t.Run(ending, func(t *testing.T) {
			finish := make(chan struct{})
			canceled := make(chan struct{}, 1)
			var calls atomic.Int64
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if calls.Add(1) > 1 {
					_, _ = io.WriteString(w, "next")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if ending == "truncated" {
					w.Header().Set("Content-Length", "1000")
				}
				_, _ = io.WriteString(w, "data: first\n\n")
				if err := http.NewResponseController(w).Flush(); err != nil {
					return
				}
				select {
				case <-finish:
					if ending == "complete" {
						_, _ = io.WriteString(w, "data: done\n\n")
					}
				case <-r.Context().Done():
					canceled <- struct{}{}
				}
			}))
			defer core.Close()
			cfg := strictConfig(t, core.URL)
			cfg.Grants[0].StrictTargets[0].MaxInflight = 2
			addStrictGrant(&cfg)
			snapshot := shadowSnapshot(time.Now())
			snapshot.Candidates[0].MaxInflight = 2
			alias := snapshot.Candidates[0]
			alias.Ref, alias.Model, alias.MaxInflight = "unpermitted-alias", "other-model", 1
			snapshot.Candidates = append(snapshot.Candidates, alias)
			writeShadowSnapshot(t, cfg.StrictDispatch.SnapshotFile, snapshot)
			handler, server := newTestEdge(t, cfg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", strings.NewReader(strings.TrimSuffix(strictBody, "}")+`,"stream":true}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+testBearer)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			line, err := bufio.NewReader(response.Body).ReadString('\n')
			if err != nil || line != "data: first\n" {
				t.Fatalf("未收到 SSE 首块: %q %v", line, err)
			}
			// 新快照不能清掉在途占位，另一 grant 也不能复用账号容量。
			snapshot.Revision++
			if err := handler.strict.cache.selector.Update(snapshot, time.Now()); err != nil {
				t.Fatal(err)
			}
			blocked := edgePost(t, server.URL, strictBody, http.Header{"Authorization": {"Bearer second-fixture-bearer"}})
			if blocked.StatusCode != 503 {
				t.Fatal("流完成前释放了共享账号占位")
			}
			_ = readBody(t, blocked)
			if stats := handler.StrictStats(); stats.Inflight != 1 || stats.Selected != 1 {
				t.Fatalf("流在途计数错误: %+v", stats)
			}
			if ending == "cancel" {
				cancel()
				select {
				case <-canceled:
				case <-time.After(3 * time.Second):
					t.Fatal("取消未传播到 Core")
				}
			} else {
				close(finish)
				_, err := io.ReadAll(response.Body)
				if ending == "complete" && err != nil {
					t.Fatal(err)
				}
				if ending == "truncated" && err == nil {
					t.Fatal("断流被伪装为完整响应")
				}
			}
			waitStrictStats(t, handler, func(s StrictStats) bool { return s.Inflight == 0 && s.Released == 1 })
			next := edgePost(t, server.URL, strictBody, nil)
			if next.StatusCode != 200 || readBody(t, next) != "next" || calls.Load() != 2 {
				t.Fatal("流结束后未释放或出现重发")
			}
		})
	}
}

func TestEdgeStrictConcurrentCapacityAndStats(t *testing.T) {
	cfg := strictConfig(t, "https://core.test")
	writeShadowSnapshot(t, cfg.StrictDispatch.SnapshotFile, shadowSnapshot(time.Now()))
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	grant := cfg.Grants[0]
	var acquired atomic.Int64
	var workers sync.WaitGroup
	start, finish := make(chan struct{}), make(chan struct{})
	for range 64 {
		workers.Go(func() {
			<-start
			_, release := handler.strict.acquire(grant, "fixture-model", handler.permits[grant.ID], time.Now())
			if release != nil {
				acquired.Add(1)
				<-finish
				release()
				release()
			}
			_ = handler.StrictStats()
		})
	}
	close(start)
	waitStrictStats(t, handler, func(s StrictStats) bool { return s.Acquisitions == 64 })
	if stats := handler.StrictStats(); stats.Selected != 1 || stats.Inflight != 1 {
		t.Fatalf("并发容量越界: %+v", stats)
	}
	close(finish)
	workers.Wait()
	if acquired.Load() != 1 {
		t.Fatal("并发获得多个账号占位")
	}
	stats := handler.StrictStats()
	if stats.Released != 1 || stats.Inflight != 0 || stats.Unavailable != 63 {
		t.Fatalf("重复释放或统计不一致: %+v", stats)
	}
	raw, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{testBearer, testCoreKey, testSecret, "candidate-a", "credential-a", "fixture-model", "pool-fixture", "payer-fixture", "actor-fixture", "region-a", "grant-fixture", cfg.Grants[0].BearerSHA256} {
		if strings.Contains(string(raw), value) {
			t.Fatal("统计包含身份或高基数字段")
		}
	}
}

func TestEdgeStrictChangedSelectionKeepsPersistentAttemptIdentity(t *testing.T) {
	verifier := fixtureVerifier(t)
	envelopes := make(chan custombridge.ForwardEnvelope, 2)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
		if err != nil {
			t.Error(err)
			w.WriteHeader(401)
			return
		}
		envelopes <- envelope
		w.WriteHeader(409)
	}))
	defer core.Close()
	cfg := strictConfig(t, core.URL)
	second := cfg.Grants[0].StrictTargets[0]
	second.CandidateRef, second.AccountID, second.CredentialRef = "candidate-b", 8, "credential-b"
	cfg.Grants[0].StrictTargets = append(cfg.Grants[0].StrictTargets, second)
	snapshot := shadowSnapshot(time.Now())
	writeShadowSnapshot(t, cfg.StrictDispatch.SnapshotFile, snapshot)
	handler, server := newTestEdge(t, cfg)
	for i := range 2 {
		if i == 1 {
			snapshot.Revision++
			snapshot.Candidates[0].Ref, snapshot.Candidates[0].AccountID, snapshot.Candidates[0].CredentialRef = "candidate-b", 8, "credential-b"
			if err := handler.strict.cache.selector.Update(snapshot, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		response := edgePost(t, server.URL, strictBody, http.Header{"Idempotency-Key": {"same-key"}})
		_ = readBody(t, response)
		if response.StatusCode != 409 {
			t.Fatal("Core 持久冲突应原样透传")
		}
	}
	a, b := <-envelopes, <-envelopes
	if a.AccountID != 7 || b.AccountID != 8 || a.AttemptID != b.AttemptID || a.ReservationID != b.ReservationID || custombridge.ForwardDigest(a) == custombridge.ForwardDigest(b) {
		t.Fatal("变更选择未保持同一持久 attempt 或未改变其摘要")
	}
	// 本测试只验证送往 Core 的持久身份契约；真实持久拒绝由整链测试验收。
}

func TestEdgeStrictExecutionDeadlineReleasesReservation(t *testing.T) {
	canceled := make(chan struct{}, 1)
	var calls atomic.Int64
	core := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	defer core.Close()
	cfg := strictConfig(t, core.URL)
	cfg.SignatureTTLSeconds, cfg.RequestTimeoutSeconds = 1, 2
	writeShadowSnapshot(t, cfg.StrictDispatch.SnapshotFile, shadowSnapshot(time.Now()))
	handler, server := newTestEdge(t, cfg)
	response := edgePost(t, server.URL, strictBody, nil)
	result := readBody(t, response)
	if response.StatusCode != 504 || !strings.Contains(result, "request_timeout") {
		t.Fatalf("执行截止语义错误: %d %s", response.StatusCode, result)
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("执行截止未取消 Core")
	}
	stats := waitStrictStats(t, handler, func(s StrictStats) bool { return s.Released == 1 })
	if stats.Inflight != 0 || calls.Load() != 1 {
		t.Fatal("执行截止未释放或重新发送")
	}
}
