//go:build unit

package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bridgeRequestIDTestAttempts struct {
	bridgeHandlerAttempts
	coreRequestID string
}

func (s *bridgeRequestIDTestAttempts) Receive(ctx context.Context, envelope custombridge.ForwardEnvelope, coreRequestID string) error {
	if err := s.bridgeHandlerAttempts.Receive(ctx, envelope, coreRequestID); err != nil {
		return err
	}
	s.coreRequestID = coreRequestID
	return nil
}

func TestBridgeForwardCoreRequestIDIgnoresProviderPrefix(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			const upstreamRequestID = "web_search:fixture-provider"
			const providerResponse = `{"id":"resp_fixture","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`
			var providerCalls atomic.Int32
			provider := &boundRequestProvider{label: "A"}
			provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				providerCalls.Add(1)
				w.Header().Set("X-Request-Id", upstreamRequestID)
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", providerResponse)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, providerResponse)
			}))
			t.Cleanup(provider.server.Close)
			other := newBoundRequestProvider(t, "B", "")
			h, _, billing := newBoundRequestHandler(t, provider, other)
			body := []byte(fmt.Sprintf(`{"model":"gpt-5.4","input":"hello","stream":%t}`, stream))
			key, envelope := bridgeHandlerEnvelope(body)
			verifier, err := custombridge.NewForwardVerifier("core", []custombridge.SigningKey{key})
			require.NoError(t, err)
			attempts := &bridgeRequestIDTestAttempts{bridgeHandlerAttempts: bridgeHandlerAttempts{received: map[string]bool{}, sent: map[string]bool{}}}
			router := gin.New()
			router.POST(custombridge.ForwardPath, middleware.ClientRequestID(), func(c *gin.Context) {
				template, _ := newBoundRequestHandlerContext(string(body), false)
				for _, name := range []middleware.ContextKey{middleware.ContextKeyAPIKey, middleware.ContextKeyUser} {
					value, _ := template.Get(string(name))
					c.Set(string(name), value)
				}
			}, middleware.BridgeForward(verifier, attempts, true, 4096), h.Responses)
			request := httptest.NewRequest(http.MethodPost, custombridge.ForwardPath, bytes.NewReader(body))
			request.RemoteAddr = "127.0.0.1:1234"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer synthetic-core-service-key")
			require.NoError(t, custombridge.SignForwardRequest(request, body, envelope, key))
			response := &bridgeHandlerDeadlineRecorder{httptest.NewRecorder()}
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, int32(1), providerCalls.Load())
			require.Empty(t, other.observed())
			command := awaitBoundRequestBilling(t, billing)
			require.Equal(t, "client:"+response.Header().Get("X-Client-Request-ID"), attempts.coreRequestID)
			require.Equal(t, attempts.coreRequestID, command.RequestID, "provider request IDs must not replace the attempt's registered Core ID")
			require.NotEqual(t, upstreamRequestID, command.RequestID)
			require.Equal(t, envelope.AttemptID, command.BridgeRequest.AttemptID)
		})
	}
}
