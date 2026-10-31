//go:build unit || integration

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
)

type mockAccountRepoForGemini struct {
	AccountRepository
	accounts     []Account
	accountsByID map[int64]*Account
}

func (m *mockAccountRepoForGemini) GetByID(_ context.Context, id int64) (*Account, error) {
	if a := m.accountsByID[id]; a != nil {
		return a, nil
	}
	return nil, errors.New("account not found")
}
func (m *mockAccountRepoForGemini) ListOAuthRefreshCandidates(context.Context) ([]Account, error) {
	return m.accounts, nil
}

type refreshAPIAccountRepo struct {
	mockAccountRepoForGemini
	account                 *Account // returned by GetByID
	getByIDErr              error
	getByIDCalls            int
	getByIDErrAfterCall     int
	getByIDErrAfterCallErr  error
	updateErr               error
	updateCalls             int
	updateCredentialsCalls  int
	successCASCalls         int
	beforeSuccessCAS        func(*refreshAPIAccountRepo)
	lastExpectedCredentials map[string]any
	lastExpectedProxyID     *int64
}

func (r *refreshAPIAccountRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	r.getByIDCalls++
	if r.getByIDErrAfterCall > 0 && r.getByIDCalls >= r.getByIDErrAfterCall {
		return nil, r.getByIDErrAfterCallErr
	}
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	return activeRefreshAPITestAccount(r.account), nil
}

func activeRefreshAPITestAccount(account *Account) *Account {
	if account == nil || account.Status != "" {
		return account
	}
	copy := *account
	copy.Status = StatusActive
	return &copy
}

func (r *refreshAPIAccountRepo) Update(_ context.Context, _ *Account) error {
	r.updateCalls++
	return r.updateErr
}

func (r *refreshAPIAccountRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.updateCalls++
	r.updateCredentialsCalls++
	if r.updateErr != nil {
		return r.updateErr
	}
	if r.account == nil || r.account.ID != id {
		r.account = &Account{ID: id}
	}
	r.account.Credentials = shallowCopyMap(credentials)
	return nil
}

func (r *refreshAPIAccountRepo) UpdateGrokOAuthCredentialsIfUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	credentials map[string]any,
) (bool, error) {
	r.successCASCalls++
	r.lastExpectedCredentials = shallowCopyMap(expectedCredentials)
	if expectedProxyID != nil {
		proxyID := *expectedProxyID
		r.lastExpectedProxyID = &proxyID
	} else {
		r.lastExpectedProxyID = nil
	}
	if r.beforeSuccessCAS != nil {
		r.beforeSuccessCAS(r)
	}
	if r.updateErr != nil {
		return false, r.updateErr
	}
	if r.account == nil || r.account.ID != id || r.account.Platform != PlatformGrok ||
		r.account.Type != AccountTypeOAuth ||
		!reflect.DeepEqual(r.account.Credentials, expectedCredentials) ||
		!reflect.DeepEqual(r.account.ProxyID, expectedProxyID) {
		return false, nil
	}
	r.updateCalls++
	r.updateCredentialsCalls++
	r.account.Credentials = shallowCopyMap(credentials)
	return true, nil
}

type refreshAPIExecutorStub struct {
	needsRefresh  bool
	cannotRefresh bool
	credentials   map[string]any
	err           error
	refreshCalls  int
	canRefresh    func(*Account) bool
	onRefresh     func()
	delay         time.Duration
}

func (e *refreshAPIExecutorStub) CanRefresh(account *Account) bool {
	if e.cannotRefresh {
		return false
	}
	if e.canRefresh != nil {
		return e.canRefresh(account)
	}
	return true
}

func (e *refreshAPIExecutorStub) NeedsRefresh(_ *Account, _ time.Duration) bool {
	return e.needsRefresh
}

func (e *refreshAPIExecutorStub) Refresh(_ context.Context, _ *Account) (map[string]any, error) {
	e.refreshCalls++
	if e.delay > 0 {
		time.Sleep(e.delay)
	}
	if e.onRefresh != nil {
		e.onRefresh()
	}
	if e.err != nil {
		return nil, e.err
	}
	return e.credentials, nil
}

func (e *refreshAPIExecutorStub) CacheKey(account *Account) string {
	return "test:api:" + account.Platform
}

type httpUpstreamRecorder struct {
	lastReq      *http.Request
	lastBody     []byte
	lastProxyURL string
	requests     []*http.Request
	bodies       [][]byte

	resp      *http.Response
	responses []*http.Response
	err       error
}

func (u *httpUpstreamRecorder) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.lastReq = req
	u.lastProxyURL = proxyURL
	if req != nil && req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		u.lastBody = b
		u.bodies = append(u.bodies, append([]byte(nil), b...))
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	u.requests = append(u.requests, req)
	if u.err != nil {
		return nil, u.err
	}
	if len(u.responses) > 0 {
		resp := u.responses[0]
		u.responses = u.responses[1:]
		return resp, nil
	}
	return u.resp, nil
}

func (u *httpUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

type openAIResponseFlushRecorder struct {
	header          http.Header
	mu              sync.Mutex
	body            bytes.Buffer
	status          int
	writes          int
	failAfterWrites int
	flushSnapshots  []string
	flushEvents     chan int
	blockFlush      int
	flushBlocked    chan struct{}
	releaseFlush    <-chan struct{}
}

func newOpenAIResponseFlushRecorder() *openAIResponseFlushRecorder {
	return &openAIResponseFlushRecorder{
		header:          make(http.Header),
		failAfterWrites: -1,
		flushEvents:     make(chan int, 16),
	}
}

func (w *openAIResponseFlushRecorder) Header() http.Header {
	return w.header
}

func (w *openAIResponseFlushRecorder) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *openAIResponseFlushRecorder) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failAfterWrites >= 0 && w.writes >= w.failAfterWrites {
		return 0, errors.New("client disconnected")
	}
	w.writes++
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func (w *openAIResponseFlushRecorder) Flush() {
	w.mu.Lock()
	w.flushSnapshots = append(w.flushSnapshots, w.body.String())
	count := len(w.flushSnapshots)
	w.mu.Unlock()
	w.flushEvents <- count
	if count == w.blockFlush {
		close(w.flushBlocked)
		<-w.releaseFlush
	}
}

func (w *openAIResponseFlushRecorder) snapshot() (string, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String(), append([]string(nil), w.flushSnapshots...)
}

func runOpenAIResponseFlushTest(recorder *openAIResponseFlushRecorder, body io.ReadCloser, gatewayCfg config.GatewayConfig) (*openaiStreamingResult, error) {
	return runOpenAIResponseFlushTestWithAccount(recorder, body, gatewayCfg, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
}

func runOpenAIResponseFlushTestWithAccount(recorder *openAIResponseFlushRecorder, body io.ReadCloser, gatewayCfg config.GatewayConfig, account *Account) (*openaiStreamingResult, error) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{Gateway: gatewayCfg},
		toolCorrector: NewCodexToolCorrector(),
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	return svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5", "gpt-5")
}

func waitOpenAIResponseFlushCount(t *testing.T, recorder *openAIResponseFlushRecorder, want int) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case count := <-recorder.flushEvents:
			if count >= want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for flush %d", want)
		}
	}
}
func rawChatCompletionsTestConfig() *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: true,
			},
		},
	}
}

func rawChatCompletionsTestAccount() *Account {
	return &Account{
		ID:          101,
		Name:        "raw-openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "http://upstream.example",
		},
	}
}

func (s *TokenRefreshService) refreshWithRetry(ctx context.Context, account *Account, refresher TokenRefresher, executor OAuthRefreshExecutor, refreshWindow time.Duration) error {
	return s.refreshWithRetryWithRateGate(ctx, account, refresher, executor, refreshWindow, nil)
}
