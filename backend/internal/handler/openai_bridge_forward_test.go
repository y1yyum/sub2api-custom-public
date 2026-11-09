//go:build unit

package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bridgeHandlerAttempts struct {
	mu       sync.Mutex
	received map[string]bool
	sent     map[string]bool
}

type bridgeHandlerDeadlineRecorder struct{ *httptest.ResponseRecorder }

func (*bridgeHandlerDeadlineRecorder) SetWriteDeadline(time.Time) error { return nil }

func (s *bridgeHandlerAttempts) Receive(_ context.Context, e custombridge.ForwardEnvelope, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.received[e.AttemptID] {
		return custombridge.ErrAttemptReplay
	}
	s.received[e.AttemptID] = true
	return nil
}
func (s *bridgeHandlerAttempts) ClaimSend(_ context.Context, _ int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent[id] {
		return custombridge.ErrAttemptReplay
	}
	s.sent[id] = true
	return nil
}
func (*bridgeHandlerAttempts) Finish(context.Context, int64, string) error { return nil }

func bridgeHandlerEnvelope(body []byte) (custombridge.SigningKey, custombridge.ForwardEnvelope) {
	key := custombridge.SigningKey{ID: "test-kid", Secret: bytes.Repeat([]byte{39}, 32), InternalKeyID: 1802, GroupID: 4202}
	digest := sha256.Sum256(body)
	e := custombridge.ForwardEnvelope{Version: 1, KID: key.ID, Audience: "core", RequestID: "edge-rid", ReservationID: "res-http", AttemptID: "attempt-http", InternalKeyID: key.InternalKeyID, GroupID: key.GroupID, AccountID: 20201, Endpoint: "/v1/responses", CanonicalModel: "gpt-5.4", PayloadSHA256: hex.EncodeToString(digest[:]), IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), Deadline: time.Now().Add(5 * time.Minute).Unix(), PolicyRevision: "p1", PoolRef: "pool", PayerRef: "payer", ActorRef: "actor"}
	return key, e
}

func TestBridgeForwardNativeResponsesProtocolAndReplay(t *testing.T) {
	for _, test := range []struct {
		stream  bool
		failure string
	}{{false, ""}, {true, ""}, {true, "sse_failed"}} {
		t.Run(fmt.Sprintf("stream_%t/%s", test.stream, test.failure), func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"gpt-5.4","input":"hello","stream":%t}`, test.stream))
			provider := newBoundRequestProvider(t, "A", test.failure)
			plain, svc, plainBilling := newBoundRequestHandler(t, provider, provider)
			plainContext, plainResponse := newBoundRequestHandlerContext(string(body), false)
			groupID := int64(4202)
			require.NoError(t, svc.BindStickySession(plainContext.Request.Context(), &groupID, svc.GenerateSessionHash(plainContext, body), 20201))
			plain.Responses(plainContext)
			if test.failure == "" {
				require.Nil(t, awaitBoundRequestBilling(t, plainBilling).BridgeRequest)
			}
			managedProvider, other := newBoundRequestProvider(t, "A", test.failure), newBoundRequestProvider(t, "B", "")
			managed, _, managedBilling := newBoundRequestHandler(t, managedProvider, other)
			key, envelope := bridgeHandlerEnvelope(body)
			verifier, err := custombridge.NewForwardVerifier("core", []custombridge.SigningKey{key})
			require.NoError(t, err)
			attempts := &bridgeHandlerAttempts{received: map[string]bool{}, sent: map[string]bool{}}
			router := gin.New()
			router.POST(custombridge.ForwardPath, middleware.ClientRequestID(), func(c *gin.Context) {
				if c.GetHeader("Authorization") != "Bearer synthetic-core-service-key" {
					c.AbortWithStatus(http.StatusUnauthorized)
					return
				}
				template, _ := newBoundRequestHandlerContext(string(body), false)
				for _, name := range []middleware.ContextKey{middleware.ContextKeyAPIKey, middleware.ContextKeyUser} {
					value, _ := template.Get(string(name))
					c.Set(string(name), value)
				}
			}, middleware.BridgeForward(verifier, attempts, true, 4096), managed.Responses)
			request := httptest.NewRequest(http.MethodPost, custombridge.ForwardPath, bytes.NewReader(body))
			request.RemoteAddr = "127.0.0.1:1234"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer synthetic-core-service-key")
			require.NoError(t, custombridge.SignForwardRequest(request, body, envelope, key))
			response := &bridgeHandlerDeadlineRecorder{httptest.NewRecorder()}
			router.ServeHTTP(response, request)
			require.Equal(t, plainResponse.Code, response.Code, response.Body.String())
			require.Equal(t, plainResponse.Header().Get("Content-Type"), response.Header().Get("Content-Type"))
			require.Equal(t, plainResponse.Body.String(), response.Body.String())
			require.Len(t, managedProvider.observed(), 1)
			require.Empty(t, other.observed())
			require.Len(t, attempts.sent, 1, "the actual native HTTP path must claim SEND_INTENT")
			if test.failure == "" {
				command := awaitBoundRequestBilling(t, managedBilling)
				require.Equal(t, envelope.AttemptID, command.BridgeRequest.AttemptID)
				require.Equal(t, "client:"+response.Header().Get("X-Client-Request-ID"), command.RequestID)
			}
			duplicate := httptest.NewRequest(http.MethodPost, custombridge.ForwardPath, bytes.NewReader(body))
			duplicate.RemoteAddr = "127.0.0.1:1234"
			duplicate.Header = request.Header.Clone()
			duplicateResponse := httptest.NewRecorder()
			router.ServeHTTP(duplicateResponse, duplicate)
			require.Equal(t, http.StatusConflict, duplicateResponse.Code)
			require.Len(t, managedProvider.observed(), 1)
		})
	}
}
