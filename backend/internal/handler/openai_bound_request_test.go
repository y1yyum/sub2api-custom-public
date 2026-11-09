//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type boundRequestProviderCall struct {
	path, authorization, body string
}

type boundRequestProvider struct {
	server  *httptest.Server
	mu      sync.Mutex
	calls   []boundRequestProviderCall
	label   string
	failure string
}

func newBoundRequestProvider(t *testing.T, label, failure string) *boundRequestProvider {
	t.Helper()
	p := &boundRequestProvider{label: label, failure: failure}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read provider body: %v", err)
			return
		}
		p.mu.Lock()
		p.calls = append(p.calls, boundRequestProviderCall{r.URL.Path, r.Header.Get("Authorization"), string(body)})
		attempt := len(p.calls)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", fmt.Sprintf("provider-%s-%d", label, attempt))
		if failure == "http_502" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":{"message":"fixture upstream failure","code":"server_error"}}`)
			return
		}
		if failure == "field_retry" && attempt == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Unknown parameter: max_output_tokens","code":"unknown_parameter","param":"max_output_tokens"}}`)
			return
		}
		if gjson.GetBytes(body, "stream").Bool() {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_provider_%s\",\"created_at\":1700000000,\"model\":\"gpt-5.4\",\"status\":\"in_progress\"}}\n\n", label)
			if failure == "cyber_failed" || failure == "cyber_zero" {
				input, output := 13, 4
				if failure == "cyber_zero" {
					input, output = 0, 0
				}
				_, _ = fmt.Fprintf(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_cyber_%s\",\"created_at\":1700000000,\"status\":\"failed\",\"error\":{\"code\":\"cyber_policy\",\"message\":\"fixture rejection\"},\"usage\":{\"input_tokens\":%d,\"output_tokens\":%d}}}\n\n", label, input, output)
				return
			}
			if failure == "sse_failed" {
				_, _ = io.WriteString(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"created_at\":1700000000,\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"fixture stream failure\"}}}\n\n")
				return
			}
			_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"response_id\":\"resp_provider_%s\",\"delta\":\"provider-%s\"}\n\n", label, label)
			_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_provider_%s\",\"created_at\":1700000000,\"model\":\"gpt-5.4\",\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n", label)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":"resp_provider_%s","object":"response","created_at":1700000000,"model":"gpt-5.4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"provider-%s"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`, label, label)
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *boundRequestProvider) observed() []boundRequestProviderCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]boundRequestProviderCall(nil), p.calls...)
}

type boundRequestRealHTTP struct{ service.HTTPUpstream }

func (boundRequestRealHTTP) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return http.DefaultClient.Do(r)
}

type boundRequestBillingRepo struct {
	service.UsageBillingRepository
	commands chan *service.UsageBillingCommand
}

func (r *boundRequestBillingRepo) Apply(_ context.Context, command *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	copy := *command
	r.commands <- &copy
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

type boundRequestBalanceCache struct{ *handlerInflightCache }

func (c *boundRequestBalanceCache) DeductUserBalance(_ context.Context, _ int64, amount float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.balance -= amount
	return nil
}

type boundRequestRPMCache struct {
	service.UserRPMCache
	mu                    sync.Mutex
	userCount, groupCount int
}

func (c *boundRequestRPMCache) IncrementUserRPM(context.Context, int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userCount++
	return c.userCount, nil
}

func (c *boundRequestRPMCache) IncrementUserGroupRPM(context.Context, int64, int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.groupCount++
	return c.groupCount, nil
}

type boundRequestSessionCache struct {
	service.GatewayCache
	mu       sync.Mutex
	bindings map[string]int64
}

func (c *boundRequestSessionCache) GetSessionAccountID(_ context.Context, group int64, key string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.bindings[fmt.Sprintf("%d:%s", group, key)]
	if !ok {
		return 0, service.ErrStickySessionNotFound
	}
	return id, nil
}

func (c *boundRequestSessionCache) SetSessionAccountID(_ context.Context, group int64, key string, id int64, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bindings[fmt.Sprintf("%d:%s", group, key)] = id
	return nil
}

func (*boundRequestSessionCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *boundRequestSessionCache) DeleteSessionAccountID(_ context.Context, group int64, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.bindings, fmt.Sprintf("%d:%s", group, key))
	return nil
}

func (*boundRequestSessionCache) GetReasoningContent(context.Context, string) (string, error) {
	return "", service.ErrReasoningContentNotFound
}
func (*boundRequestSessionCache) SetReasoningContent(context.Context, string, string, time.Duration) error {
	return nil
}

func newBoundRequestHandler(t *testing.T, a, b *boundRequestProvider) (*OpenAIGatewayHandler, *service.OpenAIGatewayService, *boundRequestBillingRepo) {
	t.Helper()
	groupID := int64(4202)
	accounts := make([]service.Account, 2)
	for i, provider := range []*boundRequestProvider{a, b} {
		accounts[i] = service.Account{
			ID: int64(20201 + i), Name: provider.label, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 2, Priority: i * 10,
			GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "fixture-" + provider.label, "base_url": provider.server.URL},
			Extra: map[string]any{"openai_responses_supported": true},
		}
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxAccountSwitches = 2
	cfg.Gateway.StreamDataIntervalTimeout = 2
	billing := service.NewBillingCacheService(&boundRequestBalanceCache{newHandlerInflightCache(100)}, nil, nil, nil, &boundRequestRPMCache{}, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	logs := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 8)}
	receipts := &boundRequestBillingRepo{commands: make(chan *service.UsageBillingCommand, 8)}
	svc := service.NewOpenAIGatewayService(&openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}, logs,
		receipts, nil, nil, nil, &boundRequestSessionCache{bindings: make(map[string]int64)}, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing,
		boundRequestRealHTTP{}, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(svc, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{
		WorkerCount: 1, QueueSize: 4, TaskTimeout: 5 * time.Second, OverflowPolicy: config.UsageRecordOverflowPolicyDrop,
	})
	t.Cleanup(pool.Stop)
	h.usageRecordWorkerPool = pool
	return h, svc, receipts
}

func newBoundRequestHandlerContext(body string, bound bool) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx := context.Background()
	if bound {
		ctx = custombridge.WithRequest(ctx, custombridge.Request{ReservationID: "reservation-A", AttemptID: "attempt-A", AccountID: 20201})
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("session_id", "bound-session")
	groupID := int64(4202)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 1802, UserID: 1702, GroupID: &groupID, User: &service.User{ID: 1702, Status: service.StatusActive, Balance: 100, RPMLimit: 100},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1, RPMLimit: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1702, Concurrency: 0})
	return c, recorder
}

func TestBoundRequestResponsesRealHTTPJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, affinity := range []string{"sticky_B", "previous_B", "ordinary_sticky_B", "public_header_only"} {
			t.Run(fmt.Sprintf("stream_%t/%s", stream, affinity), func(t *testing.T) {
				a, b := newBoundRequestProvider(t, "A", ""), newBoundRequestProvider(t, "B", "")
				h, svc, receipts := newBoundRequestHandler(t, a, b)
				body := fmt.Sprintf(`{"model":"gpt-5.4","input":"hello","stream":%t}`, stream)
				if affinity == "previous_B" {
					body = fmt.Sprintf(`{"model":"gpt-5.4","input":"hello","stream":%t,"previous_response_id":"resp_provider_B"}`, stream)
				}
				bound := affinity == "sticky_B" || affinity == "previous_B"
				c, recorder := newBoundRequestHandlerContext(body, bound)
				groupID := int64(4202)
				require.NoError(t, svc.BindStickySession(c.Request.Context(), &groupID, svc.GenerateSessionHash(c, []byte(body)), 20202))
				seedB := 0
				if affinity == "previous_B" {
					seed, seedRecorder := newBoundRequestHandlerContext(`{"model":"gpt-5.4","input":"seed response B"}`, false)
					h.Responses(seed)
					require.Equal(t, http.StatusOK, seedRecorder.Code, seedRecorder.Body.String())
					require.Contains(t, seedRecorder.Body.String(), "provider-B")
					seedB = 1
					awaitBoundRequestBilling(t, receipts)
				}
				if affinity == "public_header_only" {
					c.Request.Header.Set("X-Account-ID", "20201")
					c.Request.Header.Set("X-Bridge-Account-ID", "20201")
				}
				h.Responses(c)
				command := awaitBoundRequestBilling(t, receipts)
				require.NotEmpty(t, command.RequestID, "Core billing ID must survive the asynchronous worker")
				if bound {
					request, _ := custombridge.RequestFromContext(c.Request.Context())
					require.Equal(t, &request, command.BridgeRequest)
					require.Equal(t, request.AccountID, command.AccountID)
				} else {
					require.Nil(t, command.BridgeRequest)
				}
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				want, other := a, b
				if !bound {
					want, other = b, a
				}
				require.Contains(t, recorder.Body.String(), "provider-"+want.label)
				calls := want.observed()
				require.Len(t, calls, 1, "record actual HTTP attempts, including same-account replay")
				require.Len(t, other.observed(), seedB, "B receives only the explicit continuation seed request")
				require.Equal(t, "/v1/responses", calls[0].path)
				require.Equal(t, "Bearer fixture-"+want.label, calls[0].authorization)
				if affinity == "previous_B" {
					require.Equal(t, "resp_provider_B", gjson.Get(calls[0].body, "previous_response_id").String())
				}
			})
		}
	}
}

func TestBoundRequestResponsesRealHTTPFailureAndReplay(t *testing.T) {
	for _, test := range []struct {
		failure       string
		stream, bound bool
		wantA, wantB  int
		success       bool
	}{
		{"http_502", false, true, 1, 0, false},
		{"http_502", true, true, 1, 0, false},
		{"sse_failed", true, true, 1, 0, false},
		{"field_retry", false, true, 1, 0, false},
		{"field_retry", true, true, 1, 0, false},
		{"field_retry", false, false, 2, 0, true},
		{"field_retry", true, false, 2, 0, true},
		{"http_502", false, false, 1, 1, true},
	} {
		t.Run(fmt.Sprintf("%s/stream_%t/bound_%t", test.failure, test.stream, test.bound), func(t *testing.T) {
			a, b := newBoundRequestProvider(t, "A", test.failure), newBoundRequestProvider(t, "B", "")
			h, _, receipts := newBoundRequestHandler(t, a, b)
			body := fmt.Sprintf(`{"model":"gpt-5.4","input":"hello","stream":%t,"max_output_tokens":32}`, test.stream)
			c, recorder := newBoundRequestHandlerContext(body, test.bound)
			h.Responses(c)
			calls := a.observed()
			require.Len(t, calls, test.wantA, "actual A HTTP attempts")
			require.Len(t, b.observed(), test.wantB, "actual B HTTP attempts")
			if test.success {
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				awaitBoundRequestBilling(t, receipts)
			} else {
				require.NotContains(t, recorder.Body.String(), "provider-B")
			}
			if test.failure == "field_retry" {
				require.True(t, gjson.Get(calls[0].body, "max_output_tokens").Exists())
				if test.bound {
					require.NotEqual(t, http.StatusOK, recorder.Code, "bound requests must not remove the output bound and replay")
				} else {
					require.False(t, gjson.Get(calls[1].body, "max_output_tokens").Exists(), "ordinary compatibility recovery still replays on A")
				}
			}
		})
	}
}

func awaitBoundRequestBilling(t *testing.T, receipts *boundRequestBillingRepo) *service.UsageBillingCommand {
	t.Helper()
	select {
	case command := <-receipts.commands:
		return command
	case <-time.After(5 * time.Second):
		t.Fatal("Responses handler did not deliver billing command to asynchronous worker")
		return nil
	}
}

func TestBoundRequestResponsesPreservesStandardAdmission(t *testing.T) {
	for _, failure := range []string{"balance", "rpm"} {
		t.Run(failure, func(t *testing.T) {
			a, b := newBoundRequestProvider(t, "A", ""), newBoundRequestProvider(t, "B", "")
			h, _, _ := newBoundRequestHandler(t, a, b)
			balance := 100.0
			rpm := &boundRequestRPMCache{}
			if failure == "balance" {
				balance = 0
			} else {
				rpm.userCount = 100
			}
			billing := service.NewBillingCacheService(&boundRequestBalanceCache{newHandlerInflightCache(balance)}, nil, nil, nil, rpm, nil, h.cfg, nil)
			t.Cleanup(billing.Stop)
			h.billingCacheService = billing
			c, recorder := newBoundRequestHandlerContext(`{"model":"gpt-5.4","input":"hello"}`, true)
			h.Responses(c)
			if failure == "balance" {
				require.Equal(t, http.StatusForbidden, recorder.Code)
			} else {
				require.Equal(t, http.StatusTooManyRequests, recorder.Code)
			}
			require.Empty(t, a.observed())
			require.Empty(t, b.observed())
		})
	}
}
