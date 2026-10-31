//go:build unit || integration

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIResponsesWSUsageLogCase struct {
	firstPayload, secondPayload                      string
	group                                            *service.Group
	ingressMode                                      string
	firstFrameCloseExpected, secondTurnCloseExpected bool
	closeReason                                      string
}

func wsAllowlistGroup(enabled bool, models ...string) *service.Group {
	return &service.Group{ID: 4201, Platform: service.PlatformOpenAI,
		ModelAllowlist: service.GroupModelAllowlist{Enabled: enabled, Models: models}}
}

type publicWSHTTPUpstream struct{ service.HTTPUpstream }

type publicWSConcurrencyCache struct{ service.ConcurrencyCache }

func (publicWSConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}

func (publicWSConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error {
	return nil
}

func (publicWSHTTPUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return http.DefaultClient.Do(request)
}

func runOpenAIResponsesWebSocketUsageLogCase(t *testing.T, tc openAIResponsesWSUsageLogCase) {
	t.Helper()
	completed := []byte(`{"type":"response.completed","response":{"id":"fixture","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", completed)
			return
		}
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, _, err = conn.Read(r.Context())
			if err != nil {
				return
			}
			if err = conn.Write(r.Context(), coderws.MessageText, completed); err != nil {
				return
			}
		}
	}))
	t.Cleanup(upstream.Close)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	account := service.Account{ID: 9901, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{4201},
		Credentials: map[string]any{"api_key": "fixture", "base_url": upstream.URL},
		Extra: map[string]any{"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode": tc.ingressMode}}
	repo := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}
	logs := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 8)}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	gateway := service.NewOpenAIGatewayService(repo, logs, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billing, publicWSHTTPUpstream{}, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil)
	handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(publicWSConcurrencyCache{}), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(4201)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1801, GroupID: &groupID, Group: tc.group,
			User: &service.User{ID: 1701, Status: service.StatusActive}})
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1701, Concurrency: 0})
		c.Next()
	})
	router.GET("/v1/responses", handler.ResponsesWebSocket)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(tc.firstPayload)))
	if tc.secondTurnCloseExpected {
		_, payload, err := conn.Read(ctx)
		require.NoError(t, err)
		require.Contains(t, string(payload), "response.completed")
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(tc.secondPayload)))
	}
	_, _, err = conn.Read(ctx)
	require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
	require.ErrorContains(t, err, tc.closeReason)
}
