package edge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
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

const textBody = `{"model":"fixture-model","input":"hello"}`

func TestEdgeShadowSelectionPreservesFixedForwarding(t *testing.T) {
	for name, change := range map[string]func(*dispatch.Snapshot){
		"different_account": func(*dispatch.Snapshot) {},
		"same_account":      func(s *dispatch.Snapshot) { s.Candidates[0].AccountID = 42 },
		"pool":              func(s *dispatch.Snapshot) { s.Candidates[0].PoolRef = "other-pool" },
		"policy":            func(s *dispatch.Snapshot) { s.Candidates[0].PolicyRevision = "other-policy" },
		"model":             func(s *dispatch.Snapshot) { s.Candidates[0].Model = "other-model" },
		"region":            func(s *dispatch.Snapshot) { s.Candidates[0].Region = "other-region" },
		"tripped": func(s *dispatch.Snapshot) {
			s.Candidates[0].State = dispatch.Tripped
			s.Candidates[0].EffectiveWeight = 0
		},
		"probing": func(s *dispatch.Snapshot) {
			s.Candidates[0].State = dispatch.Probing
			s.Candidates[0].EffectiveWeight = 0
		},
		"disabled":         func(s *dispatch.Snapshot) { s.Candidates[0].Enabled = false; s.Candidates[0].EffectiveWeight = 0 },
		"full":             func(s *dispatch.Snapshot) { s.Candidates[0].Active = s.Candidates[0].MaxInflight },
		"missing":          nil,
		"default_disabled": nil,
	} {
		t.Run(name, func(t *testing.T) {
			verifier := fixtureVerifier(t)
			attempts := make(chan string, 2)
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
				if envelope.AccountID != 42 || envelope.InternalKeyID != 101 || envelope.GroupID != 7 ||
					envelope.PoolRef != "pool-fixture" || envelope.PolicyRevision != "policy-1" ||
					envelope.PayerRef != "payer-fixture" || envelope.ActorRef != "actor-fixture" ||
					envelope.CanonicalModel != "fixture-model" || r.Header.Get("Authorization") != "Bearer "+testCoreKey {
					t.Error("影子结果改变了实际授权或签名")
				}
				if r.Header.Get("X-Region") != "" || r.Header.Get("Idempotency-Key") != "" || r.GetBody != nil {
					t.Error("影子接入改变了出站头或重放边界")
				}
				attempts <- envelope.AttemptID
				if strings.Contains(string(body), `"stream":true`) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: fixed-account-b\n\n")
				} else {
					w.Header().Set("Retry-After", "3")
					w.WriteHeader(429)
					_, _ = io.WriteString(w, `{"error":"fixed-account-b"}`)
				}
			}))
			defer core.Close()
			cfg := shadowConfig(t, core.URL)
			if change != nil {
				snapshot := shadowSnapshot(time.Now())
				change(&snapshot)
				writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, snapshot)
			}
			if name == "default_disabled" {
				cfg.ShadowDispatch, cfg.Grants[0].Region = nil, ""
			}
			handler, server := newTestEdge(t, cfg)
			// 停止刷新并删除文件，证明请求只读内存，不能逐请求读取快照。
			handler.shadow.close()
			if change != nil {
				if err := os.Remove(cfg.ShadowDispatch.SnapshotFile); err != nil {
					t.Fatal(err)
				}
			}
			before := handler.ShadowStats()
			for _, body := range []string{textBody, `{"model":"fixture-model","input":"hello","stream":true}`} {
				response := edgePost(t, server.URL, body, http.Header{"Idempotency-Key": {"stable-fixture-key"}, "X-Region": {"other-region"}})
				result := readBody(t, response)
				if strings.Contains(body, `"stream":true`) {
					if response.StatusCode != 200 || result != "data: fixed-account-b\n\n" {
						t.Fatal("影子接入改变了 SSE")
					}
				} else if response.StatusCode != 429 || result != `{"error":"fixed-account-b"}` || response.Header.Get("Retry-After") != "3" {
					t.Fatal("影子接入改变了错误语义")
				}
			}
			first, second := <-attempts, <-attempts
			if first != identityHash("attempt", cfg.Grants[0].ID, "stable-fixture-key") || second != first {
				t.Fatal("影子结果改变了幂等身份或发送次数")
			}
			stats := handler.ShadowStats()
			if stats.RefreshFailures != before.RefreshFailures || stats.RefreshSuccesses != before.RefreshSuccesses {
				t.Fatal("请求路径进行了文件刷新")
			}
			switch name {
			case "default_disabled":
				if stats != (ShadowStats{}) {
					t.Fatal("默认产生了影子观察")
				}
			case "different_account":
				if stats.Mismatches != 2 {
					t.Fatalf("未记录 A/B 选号差异: %+v", stats)
				}
			case "same_account":
				if stats.Matches != 2 {
					t.Fatalf("未记录相同账号: %+v", stats)
				}
			case "missing":
				if stats.SnapshotUnavailable != 2 {
					t.Fatalf("缺少快照仍参与选号: %+v", stats)
				}
			default:
				if stats.NoEligibleRoute != 2 || stats.Unavailable != 2 {
					t.Fatalf("越界候选被选择: %+v", stats)
				}
			}
		})
	}
}

func TestEdgeShadowDoesNotObserveRejectedRequests(t *testing.T) {
	cfg := shadowConfig(t, "https://core.test")
	writeShadowSnapshot(t, cfg.ShadowDispatch.SnapshotFile, shadowSnapshot(time.Now()))
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	for _, test := range []struct{ body, token, key string }{
		{textBody, "bad-bearer", "valid"}, {`{"model":"fixture-model","tools":[]}`, testBearer, "valid"},
		{textBody, testBearer, ""}, {`{"model":"","input":"hello"}`, testBearer, "valid"},
	} {
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer "+test.token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", test.key)
		writer := httptest.NewRecorder()
		handler.ServeHTTP(writer, request)
		if writer.Code < 400 {
			t.Fatal("无效请求被接受")
		}
	}
	if handler.ShadowStats().Observations != 0 {
		t.Fatal("未通过校验的请求进入影子选号")
	}
}

func newTestEdge(t *testing.T, cfg Config) (*Handler, *httptest.Server) {
	t.Helper()
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.Close)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return handler, server
}

func edgePost(t *testing.T, target, body string, headers http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, target+"/v1/responses", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		request.Header[key] = values
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return string(body)
}

func fixtureVerifier(t *testing.T) *custombridge.ForwardVerifier {
	t.Helper()
	verifier, err := custombridge.NewForwardVerifier("core-fixture", []custombridge.SigningKey{{ID: "edge-fixture", Secret: []byte(testSecret), InternalKeyID: 101, GroupID: 7}})
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	setWriteDeadline func(time.Time) error
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	if w.setWriteDeadline != nil {
		return w.setWriteDeadline(deadline)
	}
	return nil
}

func TestEdgeSignedForwardUsesOnlyImmutableGrant(t *testing.T) {
	verifier := fixtureVerifier(t)
	envelopes := make(chan custombridge.ForwardEnvelope, 1)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
		if err != nil {
			t.Errorf("真实签名验证失败: %v", err)
			w.WriteHeader(401)
			return
		}
		if string(body) != textBody || r.Header.Get("Authorization") != "Bearer "+testCoreKey || r.URL.Path != custombridge.ForwardPath {
			t.Error("出站认证、路径或正文发生变化")
		}
		for _, name := range []string{"X-Bridge-Forged", "X-Account-ID", "X-API-Key", "Forwarded", "X-Forwarded-For", "X-Request-ID", "Cookie", "Idempotency-Key", "X-Idempotency-Key"} {
			if _, exists := r.Header[name]; exists || r.Header.Get(name) != "" {
				t.Errorf("客户端头被转发: %s", name)
			}
		}
		envelopes <- envelope
		w.Header().Set("X-Request-ID", "core-native-id")
		w.Header().Set("X-Bridge-Signature", "must-not-leak")
		w.Header().Set("Set-Cookie", "internal-session=must-not-leak")
		w.Header().Set("Connection", "X-Private-Hop")
		w.Header().Set("X-Private-Hop", "must-not-leak")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"core_rate_limit"}}`)
	}))
	defer core.Close()
	cfg := testConfig(core.URL)
	handler, server := newTestEdge(t, cfg)
	// 构造后修改调用方切片，不能改变进程内的授权快照。
	cfg.Grants[0].AccountID = 999
	cfg.SigningKeys[0].SecretBase64 = base64.StdEncoding.EncodeToString([]byte("replacement-signing-secret-32-bytes"))
	response := edgePost(t, server.URL, textBody, http.Header{
		"X-Bridge-Envelope": {"forged"}, "X-Bridge-Signature": {"forged"}, "X-Bridge-Forged": {"forged"},
		"X-Account-Id": {"999"}, "X-Api-Key": {"forged"}, "Forwarded": {"forged"}, "X-Forwarded-For": {"192.0.2.1"},
		"X-Request-Id": {"forged"}, "X-Edge-Request-Id": {"forged"}, "Cookie": {"forged"}, "Idempotency-Key": {"client-key"}, "X-Idempotency-Key": {"forged"},
	})
	if response.StatusCode != 429 || readBody(t, response) != `{"error":{"code":"core_rate_limit"}}` {
		t.Fatal("Core 状态或错误体未保持")
	}
	if response.Header.Get("X-Request-ID") != "core-native-id" || response.Header.Get("Retry-After") != "3" {
		t.Fatal("Core 响应头未保持")
	}
	for _, name := range []string{"X-Bridge-Signature", "Set-Cookie", "X-Private-Hop", "Connection"} {
		if response.Header.Get(name) != "" {
			t.Errorf("内部响应头泄漏: %s", name)
		}
	}
	envelope := <-envelopes
	if envelope.AccountID != 42 || envelope.InternalKeyID != 101 || envelope.GroupID != 7 || envelope.PolicyRevision != "policy-1" || envelope.PoolRef != "pool-fixture" || envelope.ActorRef != "actor-fixture" || envelope.PayerRef != "payer-fixture" {
		t.Fatal("信封未使用固定授权")
	}
	if response.Header.Get(edgeRequestIDHeader) != envelope.RequestID || envelope.RequestID == "forged" {
		t.Fatal("Edge 请求 ID 不匹配")
	}
	if envelope.ExpiresAt-envelope.IssuedAt != 30 || envelope.Deadline-envelope.IssuedAt != 300 || envelope.Deadline < envelope.ExpiresAt {
		t.Fatal("签名接收窗口与请求执行预算没有分离")
	}
	if handler.transport.Proxy != nil || handler.transport.DisableKeepAlives || handler.client.Timeout != 0 {
		t.Fatal("传输边界或连接池配置不正确")
	}
}

func TestEdgeRejectsUnsupportedRequestsBeforeCore(t *testing.T) {
	var calls atomic.Int64
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer core.Close()
	cfg := testConfig(core.URL)
	cfg.MaxBodyBytes = 256
	_, server := newTestEdge(t, cfg)
	cases := []struct {
		name, method, path, body string
		headers                  http.Header
		status                   int
	}{
		{"unknown_path", "POST", "/responses", textBody, nil, 404},
		{"trailing_slash", "POST", "/v1/responses/", textBody, nil, 404},
		{"escaped_alias", "POST", "/v1/%72esponses", textBody, nil, 404},
		{"query", "POST", "/v1/responses?account=999", textBody, nil, 404},
		{"get", "GET", "/v1/responses", "", nil, 404},
		{"unknown_token", "POST", "/v1/responses", textBody, http.Header{"Authorization": {"Bearer other"}}, 401},
		{"duplicate_auth", "POST", "/v1/responses", textBody, http.Header{"Authorization": {"Bearer " + testBearer, "Bearer " + testBearer}}, 401},
		{"malformed_auth", "POST", "/v1/responses", textBody, http.Header{"Authorization": {"Basic " + testBearer}}, 401},
		{"missing_auth", "POST", "/v1/responses", textBody, http.Header{"Authorization": {""}}, 401},
		{"content_type", "POST", "/v1/responses", textBody, http.Header{"Content-Type": {"text/plain"}}, 415},
		{"encoding", "POST", "/v1/responses", textBody, http.Header{"Content-Encoding": {"gzip"}}, 400},
		{"upgrade", "POST", "/v1/responses", textBody, http.Header{"Upgrade": {"websocket"}}, 400},
		{"bad_json", "POST", "/v1/responses", `{`, nil, 400},
		{"null_input", "POST", "/v1/responses", `{"model":"fixture-model","input":null}`, nil, 400},
		{"duplicate_json", "POST", "/v1/responses", `{"model":"fixture-model","model":"other","input":"hello"}`, nil, 400},
		{"tools", "POST", "/v1/responses", `{"model":"fixture-model","input":"hello","tools":[]}`, nil, 400},
		{"tool_choice", "POST", "/v1/responses", `{"model":"fixture-model","input":"hello","tool_choice":"none"}`, nil, 400},
		{"previous", "POST", "/v1/responses", `{"model":"fixture-model","input":"hello","previous_response_id":null}`, nil, 400},
		{"conversation", "POST", "/v1/responses", `{"model":"fixture-model","input":"hello","conversation":"x"}`, nil, 400},
		{"background", "POST", "/v1/responses", `{"model":"fixture-model","input":"hello","background":false}`, nil, 400},
		{"include", "POST", "/v1/responses", `{"model":"fixture-model","input":"hello","include":[]}`, nil, 400},
		{"image", "POST", "/v1/responses", `{"model":"fixture-model","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.invalid/image"}]}]}`, nil, 400},
		{"too_large", "POST", "/v1/responses", `{"model":"fixture-model","input":"` + strings.Repeat("x", 256) + `"}`, nil, 413},
		{"empty_idempotency", "POST", "/v1/responses", textBody, http.Header{"Idempotency-Key": {""}}, 400},
		{"duplicate_idempotency", "POST", "/v1/responses", textBody, http.Header{"Idempotency-Key": {"one", "two"}}, 400},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(test.method, server.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+testBearer)
			request.Header.Set("Content-Type", "application/json")
			for key, values := range test.headers {
				request.Header[key] = values
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body := readBody(t, response)
			if response.StatusCode != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.StatusCode, test.status, body)
			}
			for _, secret := range []string{testBearer, testCoreKey, testSecret} {
				if strings.Contains(body, secret) {
					t.Fatal("错误泄漏凭据")
				}
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("不支持的请求到达 Core")
	}
}

func TestEdgeIdempotencyPreservesAttemptAcrossPayloadConflict(t *testing.T) {
	verifier := fixtureVerifier(t)
	var mu sync.Mutex
	var received []custombridge.ForwardEnvelope
	var originalDigest string
	var effects int
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		e, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
		if err != nil {
			t.Error(err)
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		received = append(received, e)
		digest := custombridge.ForwardDigest(e)
		if originalDigest == "" {
			originalDigest = digest
			effects++
			_, _ = io.WriteString(w, "first")
			return
		}
		w.WriteHeader(409)
		if originalDigest == digest {
			_, _ = io.WriteString(w, "attempt_replay")
		} else {
			_, _ = io.WriteString(w, "attempt_conflict")
		}
	}))
	defer core.Close()
	_, server := newTestEdge(t, testConfig(core.URL))
	for i, body := range []string{textBody, textBody, `{"model":"fixture-model","input":"changed"}`} {
		response := edgePost(t, server.URL, body, http.Header{"Idempotency-Key": {"stable-client-key"}})
		actual := readBody(t, response)
		want := []string{"first", "attempt_replay", "attempt_conflict"}[i]
		if actual != want || i > 0 && response.StatusCode != 409 {
			t.Fatalf("第 %d 次请求未保持 Core 判定: %s", i, actual)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if effects != 1 || len(received) != 3 {
		t.Fatal("模拟 Core 生效次数不正确")
	}
	for i := 1; i < len(received); i++ {
		if received[i].AttemptID != received[0].AttemptID || received[i].ReservationID != received[0].ReservationID || received[i].RequestID == received[0].RequestID {
			t.Fatal("正文变化绕过幂等身份，或外侧请求 ID 被复用")
		}
	}
}

func TestEdgeRequestIdentitySeparatesGrantsAndUnkeyedRequests(t *testing.T) {
	_, res1, attempt1, err := requestIdentity("grant-a", []string{"bc"})
	if err != nil {
		t.Fatal(err)
	}
	_, res2, attempt2, err := requestIdentity("grant-ab", []string{"c"})
	if err != nil {
		t.Fatal(err)
	}
	if res1 == res2 || attempt1 == attempt2 || res1 == attempt1 {
		t.Fatal("幂等身份域没有隔离")
	}
	_, res1, attempt1, err = requestIdentity("grant-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, res2, attempt2, err = requestIdentity("grant-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res1 == res2 || attempt1 == attempt2 {
		t.Fatal("无幂等键的新请求复用了身份")
	}
	if _, _, _, err := requestIdentity("grant-a", []string{strings.Repeat("x", 257)}); err == nil {
		t.Fatal("接受超长幂等键")
	}
}

func TestEdgeDoesNotFollow307(t *testing.T) {
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Location", target.URL+"/capture")
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = io.WriteString(w, "core redirect")
	}))
	defer core.Close()
	_, server := newTestEdge(t, testConfig(core.URL))
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(textBody))
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 307 || readBody(t, response) != "core redirect" || targetCalls.Load() != 0 {
		t.Fatal("Edge 跟随了 Core 重定向或改变响应")
	}
}

func TestEdgeReusesConnectionWithoutRetryAfterCoreReceivesBody(t *testing.T) {
	var calls, connections atomic.Int64
	core := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, "warm")
			return
		}
		connection, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	core.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	core.Start()
	defer core.Close()
	_, server := newTestEdge(t, testConfig(core.URL))
	if body := readBody(t, edgePost(t, server.URL, textBody, nil)); body != "warm" {
		t.Fatal("连接预热失败")
	}
	response := edgePost(t, server.URL, textBody, http.Header{"Idempotency-Key": {"outer-idempotency"}})
	body := readBody(t, response)
	if response.StatusCode != 502 || calls.Load() != 2 || connections.Load() != 1 {
		t.Fatalf("连接未复用或发生重试: status=%d calls=%d connections=%d", response.StatusCode, calls.Load(), connections.Load())
	}
	for _, secret := range []string{testBearer, testCoreKey, testSecret, "X-Bridge"} {
		if strings.Contains(body, secret) {
			t.Fatal("网络错误泄漏凭据")
		}
	}
}

func TestEdgeSSEFlushesBeforeCoreCompletes(t *testing.T) {
	finish := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		select {
		case <-finish:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: done\n\n")
	}))
	defer func() { release(); core.Close() }()
	_, server := newTestEdge(t, testConfig(core.URL))
	response := edgePost(t, server.URL, `{"model":"fixture-model","input":"hello","stream":true}`, nil)
	reader := bufio.NewReader(response.Body)
	first := make(chan string, 1)
	go func() { line, _ := reader.ReadString('\n'); first <- line }()
	select {
	case line := <-first:
		if line != "data: first\n" {
			t.Fatalf("首块错误: %q", line)
		}
	case <-time.After(3 * time.Second):
		release()
		t.Fatal("Core 未结束时读不到首块 SSE")
	}
	release()
	rest, err := io.ReadAll(reader)
	if err != nil || string(rest) != "\ndata: done\n\n" {
		t.Fatalf("SSE 内容变化: %q %v", rest, err)
	}
}

func TestEdgeSSEContinuesAfterSignatureExpires(t *testing.T) {
	verifier := fixtureVerifier(t)
	received := make(chan custombridge.ForwardEnvelope, 1)
	finish := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
		if err != nil {
			t.Error(err)
			w.WriteHeader(401)
			return
		}
		received <- envelope
		ctx, cancel := context.WithDeadline(r.Context(), time.Unix(envelope.Deadline, 0))
		defer cancel()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		select {
		case <-finish:
		case <-ctx.Done():
			return
		}
		_, _ = io.WriteString(w, "data: after-signature-expiry\n\n")
	}))
	defer func() { release(); core.Close() }()
	cfg := testConfig(core.URL)
	cfg.SignatureTTLSeconds, cfg.RequestTimeoutSeconds = 2, 5
	_, server := newTestEdge(t, cfg)
	response := edgePost(t, server.URL, `{"model":"fixture-model","input":"hello","stream":true}`, nil)
	if response.StatusCode != 200 {
		t.Fatalf("签名未受理: status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: first\n" {
		t.Fatalf("未收到首块: %q %v", line, err)
	}
	envelope := <-received
	if envelope.Deadline-envelope.IssuedAt != 5 || envelope.ExpiresAt-envelope.IssuedAt != 2 {
		t.Fatal("执行预算或签名窗口错误")
	}
	timer := time.NewTimer(time.Until(time.Unix(envelope.ExpiresAt, 0)) + 50*time.Millisecond)
	defer timer.Stop()
	<-timer.C
	release()
	rest, err := io.ReadAll(reader)
	if err != nil || string(rest) != "\ndata: after-signature-expiry\n\n" {
		t.Fatalf("SSE 被签名接收窗口截断: %q %v", rest, err)
	}
}

func TestEdgeEarlierParentDeadlineIsPreserved(t *testing.T) {
	handler, err := NewHandler(testConfig("https://core.test"))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	parentDeadline := time.Now().Add(10 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), parentDeadline)
	defer cancel()
	verifier := fixtureVerifier(t)
	handler.client.Transport = captureTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || !deadline.Equal(parentDeadline) {
			t.Error("出站上下文未保留更早的父 deadline")
		}
		body, _ := io.ReadAll(r.Body)
		envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
		if err != nil {
			t.Error(err)
		}
		if envelope.Deadline-envelope.IssuedAt != 300 {
			t.Error("签名预算应独立于客户端更早的取消约束")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(textBody)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	deadlines := make(chan time.Time, 4)
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder(), setWriteDeadline: func(deadline time.Time) error {
		deadlines <- deadline
		return nil
	}}
	handler.ServeHTTP(writer, request)
	if writer.Code != 200 || writer.Body.String() != "ok" {
		t.Fatal("受父 deadline 约束的请求失败")
	}
	select {
	case got := <-deadlines:
		if !got.Equal(parentDeadline) {
			t.Fatalf("响应写期限错误: got=%v want=%v", got, parentDeadline)
		}
	default:
		t.Fatal("响应写期限未设置")
	}
	select {
	case got := <-deadlines:
		t.Fatalf("handler 提前改写了服务器收尾所需的写期限: %v", got)
	default:
	}
}

func TestEdgeKeepAliveResetsDeadlineAfterResponse(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Trailer", "X-Usage-Fact")
		_, _ = io.WriteString(w, "data: done\n\n")
		w.Header().Set("X-Usage-Fact", "complete")
	}))
	defer core.Close()
	cfg := testConfig(core.URL)
	cfg.SignatureTTLSeconds, cfg.RequestTimeoutSeconds = 1, 2
	handler, server := newTestEdge(t, cfg)
	deadlines := make(chan time.Time, 1)
	handler.client.Transport = captureTransport(func(r *http.Request) (*http.Response, error) {
		deadline, _ := r.Context().Deadline()
		deadlines <- deadline
		return handler.transport.RoundTrip(r)
	})
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(textBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	if err := request.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || readBody(t, response) != "data: done\n\n" || response.Trailer.Get("X-Usage-Fact") != "complete" || response.Close {
		t.Fatal("首个响应未完整输出 SSE、终结块和 trailer，或连接不可复用")
	}
	deadline := <-deadlines
	timer := time.NewTimer(time.Until(deadline) + 50*time.Millisecond)
	defer timer.Stop()
	<-timer.C
	// 第二个请求走不安装写期限的错误分支，验证 net/http 已复位首个请求的期限。
	request, err = http.NewRequest(http.MethodGet, server.URL+"/missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := request.Write(conn); err != nil {
		t.Fatal(err)
	}
	response, err = http.ReadResponse(reader, request)
	if err != nil {
		t.Fatalf("同一 TCP 连接的第二个请求失败: %v", err)
	}
	if response.StatusCode != http.StatusNotFound || !strings.Contains(readBody(t, response), "not_found") {
		t.Fatal("首个请求的写期限影响了同一连接的后续请求")
	}
}

func TestEdgeExecutionBudgetBeforeHeadersReturns504(t *testing.T) {
	cfg := testConfig("https://core.test")
	cfg.SignatureTTLSeconds, cfg.RequestTimeoutSeconds = 1, 1
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	handler.client.Transport = captureTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(textBody))
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, request)
	if writer.Code != http.StatusGatewayTimeout || !strings.Contains(writer.Body.String(), "request_timeout") {
		t.Fatalf("执行预算耗尽被转为成功或无错误: status=%d body=%s", writer.Code, writer.Body.String())
	}
}

func TestEdgeCancelPropagatesToCore(t *testing.T) {
	canceled := make(chan struct{})
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-time.After(5 * time.Second):
		}
	}))
	defer core.Close()
	_, server := newTestEdge(t, testConfig(core.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"hello","stream":true}`))
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatal("取消前未收到 SSE")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("取消未传到 Core")
	}
}

type observedStreamWriter struct {
	http.ResponseWriter
	activity *atomic.Int64
}

func (w observedStreamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w observedStreamWriter) Write(body []byte) (int, error) {
	w.activity.Add(1)
	defer w.activity.Add(1)
	return w.ResponseWriter.Write(body)
}

func (w observedStreamWriter) FlushError() error {
	w.activity.Add(1)
	defer w.activity.Add(1)
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func TestEdgeSlowReaderReleasesHandler(t *testing.T) {
	for _, test := range []struct {
		name           string
		requestSeconds int
		parentTimeout  time.Duration
		cancelEarly    bool
	}{
		{name: "execution_deadline", requestSeconds: 3},
		{name: "earlier_parent_deadline", requestSeconds: 30, parentTimeout: 2 * time.Second},
		{name: "early_cancel", requestSeconds: 30, cancelEarly: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				chunk := []byte("data: " + strings.Repeat("x", 32<<10) + "\n\n")
				for {
					if _, err := w.Write(chunk); err != nil {
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						return
					}
				}
			}))
			t.Cleanup(func() { core.CloseClientConnections(); core.Close() })
			cfg := testConfig(core.URL)
			cfg.SignatureTTLSeconds, cfg.RequestTimeoutSeconds = 1, test.requestSeconds
			handler, err := NewHandler(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(handler.Close)
			executing := make(chan context.Context, 1)
			handler.client.Transport = captureTransport(func(r *http.Request) (*http.Response, error) {
				executing <- r.Context()
				return handler.transport.RoundTrip(r)
			})
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.parentTimeout > 0 {
				var cancelDeadline context.CancelFunc
				parent, cancelDeadline = context.WithTimeout(parent, test.parentTimeout)
				defer cancelDeadline()
			}
			var activity atomic.Int64
			done := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				handler.ServeHTTP(observedStreamWriter{w, &activity}, r)
			}))
			server.Config.BaseContext = func(net.Listener) context.Context { return parent }
			server.Config.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
				tcp, ok := conn.(*net.TCPConn)
				if !ok {
					t.Error("测试连接不是 TCP")
				} else if err := tcp.SetWriteBuffer(1024); err != nil {
					t.Error(err)
				}
				return ctx
			}
			server.Start()
			t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
			conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			tcp, ok := conn.(*net.TCPConn)
			if !ok {
				t.Fatal("客户端连接不是 TCP")
			}
			if err := tcp.SetReadBuffer(1024); err != nil {
				t.Fatal(err)
			}
			if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintf(conn, "POST /v1/responses HTTP/1.1\r\nHost: edge.test\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", testBearer, len(textBody), textBody); err != nil {
				t.Fatal(err)
			}
			var execution context.Context
			select {
			case execution = <-executing:
			case <-time.After(time.Second):
				t.Fatal("请求未到达 Core")
			}
			// 客户端不读取响应；同一次真实 Write/Flush 持续未返回才算复现阻塞。
			stallDeadline := time.Now().Add(time.Second)
			for {
				before := activity.Load()
				time.Sleep(100 * time.Millisecond)
				if before%2 == 1 && activity.Load() == before {
					break
				}
				if time.Now().After(stallDeadline) {
					t.Fatal("未复现真实 TCP 响应写入阻塞")
				}
			}
			t.Log("已确认真实 TCP Write/Flush 阻塞，客户端连接保持打开")
			if test.cancelEarly {
				cancel()
			}
			select {
			case <-execution.Done():
			case <-time.After(4 * time.Second):
				t.Fatal("执行上下文未按期结束")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("执行上下文已结束，慢读客户端仍使 handler 阻塞")
			}
		})
	}
}

func TestEdgeWriteDeadlineUnsupportedAbortsResponse(t *testing.T) {
	handler, err := NewHandler(testConfig("https://core.test"))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	handler.client.Transport = captureTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("must-not-send"))}, nil
	})
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(textBody))
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	writer := httptest.NewRecorder()
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Errorf("不支持写期限时未中止响应: %v", got)
		}
		if writer.Flushed || writer.Body.Len() != 0 {
			t.Error("无写期限保护时仍发送响应")
		}
	}()
	handler.ServeHTTP(writer, request)
}

func TestEdgeWriteCancellationWaitsForCallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	callbackStarted, releaseCallback := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseCallback) }) }
	defer release()
	deadlines := make(chan time.Time, 4)
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder(), setWriteDeadline: func(deadline time.Time) error {
		deadlines <- deadline
		if !deadline.IsZero() && !deadline.After(time.Now()) {
			close(callbackStarted)
			<-releaseCallback
		}
		return nil
	}}
	reader, body := io.Pipe()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- copyResponse(ctx, writer, &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader})
	}()
	t.Cleanup(func() {
		cancel()
		release()
		_ = reader.Close()
		_ = body.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("写入测试 goroutine 未退出")
		}
	})
	select {
	case <-deadlines:
	case <-time.After(time.Second):
		t.Fatal("未设置初始写期限")
	}
	cancel()
	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		t.Fatal("取消未触发写期限回调")
	}
	_ = body.Close()
	select {
	case err := <-done:
		t.Fatalf("取消回调未结束就返回: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消回调结束后未返回")
	}
	select {
	case deadline := <-deadlines:
		if deadline.IsZero() || deadline.After(time.Now()) {
			t.Fatal("取消期限未保留到服务器收尾")
		}
	default:
		t.Fatal("未设置取消写期限")
	}
	select {
	case deadline := <-deadlines:
		t.Fatalf("返回时改写了取消写期限: %v", deadline)
	default:
	}
}

func TestEdgePreservesTruncationAfterHeaders(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: partial\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		panic(http.ErrAbortHandler)
	}))
	defer core.Close()
	_, server := newTestEdge(t, testConfig(core.URL))
	response := edgePost(t, server.URL, textBody, nil)
	body, err := io.ReadAll(response.Body)
	if err == nil || string(body) != "data: partial\n\n" || response.StatusCode != 200 {
		t.Fatalf("断流被转换为成功或追加错误: %q %v", body, err)
	}
}

func TestEdgeSeparateBearerGrantsHaveSeparateAttempts(t *testing.T) {
	var mu sync.Mutex
	var received []custombridge.ForwardEnvelope
	verifier := fixtureVerifier(t)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
		if err != nil {
			t.Error(err)
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		received = append(received, envelope)
		mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}))
	defer core.Close()
	cfg := testConfig(core.URL)
	other := cfg.Grants[0]
	other.ID, other.ActorRef = "grant-second-v1", "actor-second"
	digest := sha256.Sum256([]byte("second-fixture-bearer"))
	other.BearerSHA256 = hex.EncodeToString(digest[:])
	cfg.Grants = append(cfg.Grants, other)
	_, server := newTestEdge(t, cfg)
	for _, token := range []string{testBearer, "second-fixture-bearer"} {
		response := edgePost(t, server.URL, textBody, http.Header{"Authorization": {"Bearer " + token}, "Idempotency-Key": {"same-key"}})
		if readBody(t, response) != "ok" {
			t.Fatal("请求未成功")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || received[0].AttemptID == received[1].AttemptID || received[1].ActorRef != "actor-second" {
		t.Fatal("不同授权未隔离")
	}
}

func TestEdgeResponseTrailers(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Trailer", "X-Usage-Fact, X-Bridge-Signature")
		_, _ = fmt.Fprint(w, "body")
		w.Header().Set("X-Usage-Fact", "done")
		w.Header().Set("X-Bridge-Signature", "must-not-leak")
	}))
	defer core.Close()
	_, server := newTestEdge(t, testConfig(core.URL))
	response := edgePost(t, server.URL, textBody, nil)
	if readBody(t, response) != "body" || response.Trailer.Get("X-Usage-Fact") != "done" || response.Trailer.Get("X-Bridge-Signature") != "" {
		t.Fatal("尾部响应头未正确透传或过滤")
	}
}

type captureTransport func(*http.Request) (*http.Response, error)

func (f captureTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestEdgeOutboundRequestCannotBeReplayedByTransport(t *testing.T) {
	handler, err := NewHandler(testConfig("https://core.test"))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	handler.client.Transport = captureTransport(func(r *http.Request) (*http.Response, error) {
		if r.GetBody != nil || r.Method != "POST" || r.ContentLength <= 0 {
			t.Error("出站请求可重放或正文长度丢失")
		}
		for _, name := range []string{"Idempotency-Key", "X-Idempotency-Key"} {
			if _, exists := r.Header[name]; exists {
				t.Error("出站包含重放头")
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte("ok")))}, nil
	})
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(textBody))
	request.Header.Set("Authorization", "Bearer "+testBearer)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "outer-key")
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(writer, request)
	if writer.Code != 200 || writer.Body.String() != "ok" {
		t.Fatal("出站请求失败")
	}
}
