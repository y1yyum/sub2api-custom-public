//go:build unit

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bridgeDeadlineAttemptStore struct{}

func (bridgeDeadlineAttemptStore) Receive(context.Context, custombridge.ForwardEnvelope, string) error {
	return nil
}
func (bridgeDeadlineAttemptStore) ClaimSend(context.Context, int64, string) error { return nil }
func (bridgeDeadlineAttemptStore) Finish(context.Context, int64, string) error    { return nil }

type bridgeDeadlineSmallBufferListener struct{ net.Listener }

func (l bridgeDeadlineSmallBufferListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if err := connection.(*net.TCPConn).SetWriteBuffer(4096); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

func TestProvideHTTPServerBridgeExecutionDeadlineBoundsWrites(t *testing.T) {
	for _, operation := range []string{"write", "flush"} {
		t.Run(operation, func(t *testing.T) {
			key := custombridge.SigningKey{ID: "deadline-test", Secret: bytes.Repeat([]byte{37}, 32), InternalKeyID: 12, GroupID: 21}
			verifier, err := custombridge.NewForwardVerifier("core", []custombridge.SigningKey{key})
			require.NoError(t, err)
			body := []byte(`{"model":"m","input":"hello"}`)
			digest := sha256.Sum256(body)
			now := time.Now()
			envelope := custombridge.ForwardEnvelope{
				Version: 1, KID: key.ID, Audience: "core", RequestID: "deadline-request",
				ReservationID: "reservation", AttemptID: "attempt", InternalKeyID: key.InternalKeyID,
				GroupID: key.GroupID, AccountID: 31, Endpoint: "/v1/responses", CanonicalModel: "m",
				PayloadSHA256: hex.EncodeToString(digest[:]), IssuedAt: now.Unix(),
				ExpiresAt: now.Add(time.Minute).Unix(), Deadline: now.Add(2 * time.Minute).Unix(),
				PolicyRevision: "p1", PoolRef: "pool", PayerRef: "payer", ActorRef: "actor",
			}
			accepted := make(chan context.Context, 1)
			completed := make(chan error, 1)
			router := gin.New()
			router.POST(custombridge.ForwardPath, middleware.ClientRequestID(), func(c *gin.Context) {
				groupID := key.GroupID
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: key.InternalKeyID, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI}})
				ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
				defer cancel()
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			}, middleware.BridgeForward(verifier, bridgeDeadlineAttemptStore{}, true, 1024), func(c *gin.Context) {
				accepted <- c.Request.Context()
				if operation == "write" {
					_, err := c.Writer.Write(make([]byte, 64<<20))
					completed <- err
					return
				}
				chunk := make([]byte, 512)
				for range 128 << 10 {
					if _, err := c.Writer.Write(chunk); err != nil {
						completed <- err
						return
					}
					if err := http.NewResponseController(c.Writer).Flush(); err != nil {
						completed <- err
						return
					}
				}
				completed <- nil
			})
			cfg := ingressTestConfig()
			cfg.Gateway.DownstreamWriteTimeoutSeconds = 2
			cfg.Gateway.RequestBodyReadTimeoutSeconds = 1
			server := httptest.NewUnstartedServer(nil)
			server.Config = ProvideHTTPServer(cfg, router)
			server.Listener = bridgeDeadlineSmallBufferListener{server.Listener}
			server.Start()
			defer server.Close()
			connection, err := net.Dial("tcp", server.Listener.Addr().String())
			require.NoError(t, err)
			defer func() { _ = connection.Close() }()
			require.NoError(t, connection.(*net.TCPConn).SetReadBuffer(1024))
			request, err := http.NewRequest(http.MethodPost, server.URL+custombridge.ForwardPath, bytes.NewReader(body))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer synthetic-native-key")
			request.Header.Set("Content-Type", "application/json")
			require.NoError(t, custombridge.SignForwardRequest(request, body, envelope, key))
			require.NoError(t, request.Write(connection))
			var execution context.Context
			select {
			case execution = <-accepted:
			case <-time.After(2 * time.Second):
				t.Fatal("signed bridge request was not accepted by ProvideHTTPServer.Handler")
			}
			select {
			case <-execution.Done():
				deadline, ok := execution.Deadline()
				require.True(t, ok)
				require.False(t, time.Now().Before(deadline), "execution must not cancel before its deadline")
				// The transport timeout can cancel the parent before the context timer wins.
				require.True(t, errors.Is(execution.Err(), context.DeadlineExceeded) || errors.Is(execution.Err(), context.Canceled), "unexpected execution error: %v", execution.Err())
			case <-time.After(2 * time.Second):
				t.Fatal("bridge execution context did not expire")
			}
			select {
			case err := <-completed:
				var timeout net.Error
				require.True(t, errors.As(err, &timeout) && timeout.Timeout(), "expected transport timeout, got %v", err)
			case <-time.After(500 * time.Millisecond):
				t.Fatal("per-operation timeout extended the bridge execution deadline")
			}
		})
	}
}
