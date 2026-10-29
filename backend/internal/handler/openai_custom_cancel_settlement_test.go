//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type customCanceledUsageHTTP struct {
	service.HTTPUpstream
	cancel   context.CancelFunc
	mu       sync.Mutex
	calls    int
	terminal string
}

func (u *customCanceledUsageHTTP) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()
		if _, err := io.WriteString(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_cancel\",\"usage\":{\"input_tokens\":13}}}\n\n"); err != nil {
			return
		}
		u.cancel()
		_, _ = io.WriteString(writer, u.terminal)
	}()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"cancel-once"}}, Body: reader}, nil
}

func TestCustomCanceledResponsesRecordsObservedUsageExactlyOnce(t *testing.T) {
	for _, event := range []string{"response.completed", "response.failed"} {
		t.Run(event, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			groupID := int64(4206)
			account := service.Account{ID: 9910, Name: "fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.example.test"}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.StreamDataIntervalTimeout = 2
			accounts := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}
			logs := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 4)}
			upstream := &customCanceledUsageHTTP{cancel: cancel, terminal: "data: {\"type\":\"" + event + "\",\"response\":{\"id\":\"resp_cancel\",\"error\":{\"code\":\"upstream_error\",\"message\":\"temporary error\"},\"usage\":{\"input_tokens\":13,\"output_tokens\":4}}}\n\n"}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			svc := service.NewOpenAIGatewayService(accounts, logs, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			h := NewOpenAIGatewayHandler(svc, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","input":"hello","stream":true}`)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1806, GroupID: &groupID, User: &service.User{ID: 1706, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true}})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1706, Concurrency: 0})
			h.Responses(c)
			upstream.mu.Lock()
			calls := upstream.calls
			upstream.mu.Unlock()
			require.Equal(t, 1, calls, "cancellation must not call another upstream")
			require.Len(t, logs.created, 1, "actual usage repository must receive exactly one settlement record")
			recorded := <-logs.created
			require.Equal(t, 13, recorded.InputTokens)
			require.Equal(t, 4, recorded.OutputTokens)
			require.Empty(t, recorder.Body.String(), "canceled client must not receive replay/error frames")
		})
	}
}
