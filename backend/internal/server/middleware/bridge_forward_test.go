//go:build unit

package middleware

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpresponse"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bridgeForwardDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *bridgeForwardDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func newBridgeForwardDeadlineRecorder() *bridgeForwardDeadlineRecorder {
	return &bridgeForwardDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
}

type bridgeForwardTestStore struct {
	mu        sync.Mutex
	requests  map[string]string
	sent      map[string]bool
	available bool
}

func (s *bridgeForwardTestStore) Receive(_ context.Context, e custombridge.ForwardEnvelope, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.available {
		return context.DeadlineExceeded
	}
	if digest, exists := s.requests[e.AttemptID]; exists {
		if digest != custombridge.ForwardDigest(e) {
			return custombridge.ErrAttemptConflict
		}
		return custombridge.ErrAttemptReplay
	}
	s.requests[e.AttemptID] = custombridge.ForwardDigest(e)
	return nil
}

func (s *bridgeForwardTestStore) ClaimSend(_ context.Context, _ int64, attempt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent[attempt] {
		return custombridge.ErrAttemptReplay
	}
	s.sent[attempt] = true
	return nil
}

func (*bridgeForwardTestStore) Finish(context.Context, int64, string) error { return nil }

func bridgeForwardMiddlewareFixture(t *testing.T) (*custombridge.ForwardVerifier, custombridge.SigningKey, custombridge.ForwardEnvelope, []byte) {
	t.Helper()
	key := custombridge.SigningKey{ID: "kid-test", Secret: bytes.Repeat([]byte{37}, 32), InternalKeyID: 12, GroupID: 21}
	v, err := custombridge.NewForwardVerifier("core", []custombridge.SigningKey{key})
	require.NoError(t, err)
	body := []byte(`{"model":"m","input":"hello"}`)
	digest := sha256.Sum256(body)
	e := custombridge.ForwardEnvelope{Version: 1, KID: key.ID, Audience: "core", RequestID: "rid", ReservationID: "reservation", AttemptID: "attempt", InternalKeyID: 12, GroupID: 21, AccountID: 31, Endpoint: "/v1/responses", CanonicalModel: "m", PayloadSHA256: hex.EncodeToString(digest[:]), IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), Deadline: time.Now().Add(5 * time.Minute).Unix(), PolicyRevision: "p1", PoolRef: "pool", PayerRef: "payer", ActorRef: "actor"}
	return v, key, e, body
}

func TestBridgeForwardMiddlewareTrustAndReplay(t *testing.T) {
	verifier, key, envelope, body := bridgeForwardMiddlewareFixture(t)
	store := &bridgeForwardTestStore{requests: map[string]string{}, sent: map[string]bool{}, available: true}
	router := gin.New()
	router.POST(custombridge.ForwardPath, ClientRequestID(), func(c *gin.Context) {
		groupID := key.GroupID
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: key.InternalKeyID, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI}})
	}, BridgeForward(verifier, store, false, 1024), func(c *gin.Context) {
		bound, ok := custombridge.RequestFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, int64(31), bound.AccountID)
		require.Equal(t, "/v1/responses", c.Request.URL.Path)
		require.Empty(t, c.GetHeader(custombridge.ForwardSignatureHeader))
		require.Empty(t, c.GetHeader("X-Account-ID"))
		require.Empty(t, c.GetHeader("Authorization"))
		require.NoError(t, custombridge.ClaimSendIntent(c.Request.Context()))
		require.ErrorIs(t, custombridge.ClaimSendIntent(c.Request.Context()), custombridge.ErrAttemptReplay)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	for i := range 2 {
		r := httptest.NewRequest(http.MethodPost, custombridge.ForwardPath, bytes.NewReader(body))
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Authorization", "Bearer synthetic-native-key")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Account-ID", "999")
		deadline := time.Now().Add(30 * time.Second)
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()
		r = r.WithContext(ctx)
		e := envelope
		e.RequestID = "rid-next"
		e.IssuedAt++
		e.ExpiresAt++
		require.NoError(t, custombridge.SignForwardRequest(r, body, e, key))
		w := newBridgeForwardDeadlineRecorder()
		router.ServeHTTP(w, r)
		if i == 0 {
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, "attempt", w.Header().Get("X-Bridge-Attempt-ID"))
			require.Equal(t, []time.Time{deadline}, w.deadlines)
		} else {
			require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		}
	}
	require.Len(t, store.requests, 1)
	require.Len(t, store.sent, 1)
}

func TestBridgeForwardMiddlewareRejectsBeforeDispatch(t *testing.T) {
	for _, scenario := range []string{"insecure", "forwarded_tls", "wrong_key", "wrong_group", "wrong_platform", "unsigned", "storage_error", "too_large", "wrong_type", "public_headers"} {
		t.Run(scenario, func(t *testing.T) {
			verifier, key, e, body := bridgeForwardMiddlewareFixture(t)
			store := &bridgeForwardTestStore{requests: map[string]string{}, sent: map[string]bool{}, available: scenario != "storage_error"}
			router := gin.New()
			called := false
			router.POST(custombridge.ForwardPath, ClientRequestID(), func(c *gin.Context) {
				groupID, id, platform := key.GroupID, key.InternalKeyID, service.PlatformOpenAI
				if scenario == "wrong_key" {
					id++
				}
				if scenario == "wrong_group" {
					groupID++
				}
				if scenario == "wrong_platform" {
					platform = service.PlatformComposite
				}
				c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: id, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: platform}})
			}, BridgeForward(verifier, store, true, 64), func(c *gin.Context) { called = true; c.Status(http.StatusOK) })
			if scenario == "too_large" {
				body = bytes.Repeat([]byte("x"), 65)
			}
			r := httptest.NewRequest(http.MethodPost, custombridge.ForwardPath, bytes.NewReader(body))
			r.RemoteAddr = "127.0.0.1:1234"
			if scenario == "insecure" || scenario == "forwarded_tls" {
				r.RemoteAddr = "192.0.2.1:1234"
			}
			if scenario == "forwarded_tls" {
				r.Header.Set("X-Forwarded-Proto", "https")
			}
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer synthetic-native-key")
			if scenario == "wrong_type" {
				r.Header.Set("Content-Type", "text/plain")
			}
			if scenario != "unsigned" && scenario != "public_headers" {
				require.NoError(t, custombridge.SignForwardRequest(r, body, e, key))
			} else {
				r.Header.Set("X-Bridge-Account-ID", "31")
			}
			w := newBridgeForwardDeadlineRecorder()
			router.ServeHTTP(w, r)
			require.False(t, called)
			require.GreaterOrEqual(t, w.Code, 400)
			require.Empty(t, store.sent)
		})
	}
}

func TestBridgeForwardSignatureExpiryDoesNotCutAcceptedStream(t *testing.T) {
	verifier, key, envelope, body := bridgeForwardMiddlewareFixture(t)
	envelope.ExpiresAt = time.Now().Unix() + 2
	store := &bridgeForwardTestStore{requests: map[string]string{}, sent: map[string]bool{}, available: true}
	router := gin.New()
	router.POST(custombridge.ForwardPath, ClientRequestID(), func(c *gin.Context) {
		groupID := key.GroupID
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: key.InternalKeyID, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI}})
	}, BridgeForward(verifier, store, false, 1024), func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString("event: started\ndata: {}\n\n")
		c.Writer.Flush()
		select {
		case <-c.Request.Context().Done():
			_, _ = c.Writer.WriteString("event: expired\ndata: {}\n\n")
		case <-time.After(2100 * time.Millisecond):
			_, _ = c.Writer.WriteString("event: completed\ndata: {}\n\n")
		}
	})
	r := httptest.NewRequest(http.MethodPost, custombridge.ForwardPath, bytes.NewReader(body))
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Authorization", "Bearer synthetic-native-key")
	r.Header.Set("Content-Type", "application/json")
	require.NoError(t, custombridge.SignForwardRequest(r, body, envelope, key))
	w := newBridgeForwardDeadlineRecorder()
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, "the authentication acceptance window must not terminate an accepted stream")
	require.True(t, w.Flushed)
	require.Contains(t, w.Body.String(), "event: completed")
	require.NotContains(t, w.Body.String(), "event: expired")
	require.Equal(t, []time.Time{time.Unix(envelope.Deadline, 0)}, w.deadlines)
}

func TestBridgeForwardRejectsUnsupportedWriteDeadline(t *testing.T) {
	verifier, key, envelope, body := bridgeForwardMiddlewareFixture(t)
	store := &bridgeForwardTestStore{requests: map[string]string{}, sent: map[string]bool{}, available: true}
	router := gin.New()
	called := false
	router.POST(custombridge.ForwardPath, ClientRequestID(), func(c *gin.Context) {
		groupID := key.GroupID
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: key.InternalKeyID, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI}})
	}, BridgeForward(verifier, store, false, 1024), func(c *gin.Context) { called = true })
	request := httptest.NewRequest(http.MethodPost, custombridge.ForwardPath, bytes.NewReader(body))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Authorization", "Bearer synthetic-native-key")
	request.Header.Set("Content-Type", "application/json")
	require.NoError(t, custombridge.SignForwardRequest(request, body, envelope, key))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.False(t, called)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Contains(t, response.Body.String(), "bridge_write_deadline_unavailable")
	require.Empty(t, store.sent)
}

type bridgeForwardSmallBufferListener struct{ net.Listener }

func (l bridgeForwardSmallBufferListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := connection.(*net.TCPConn); ok {
		if err := tcp.SetWriteBuffer(4096); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	return connection, nil
}

func TestBridgeForwardExecutionDeadlineUnblocksSlowReader(t *testing.T) {
	for _, operation := range []string{"write", "flush"} {
		t.Run(operation, func(t *testing.T) {
			verifier, key, envelope, body := bridgeForwardMiddlewareFixture(t)
			envelope.ExpiresAt = time.Now().Unix() + 2
			envelope.Deadline = envelope.ExpiresAt + 1
			store := &bridgeForwardTestStore{requests: map[string]string{}, sent: map[string]bool{}, available: true}
			accepted := make(chan context.Context, 1)
			completed := make(chan error, 1)
			router := gin.New()
			router.POST(custombridge.ForwardPath, ClientRequestID(), func(c *gin.Context) {
				groupID := key.GroupID
				c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: key.InternalKeyID, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI}})
			}, BridgeForward(verifier, store, true, 1024), func(c *gin.Context) {
				accepted <- c.Request.Context()
				c.Header("Content-Type", "text/event-stream")
				if operation == "write" {
					_, err := c.Writer.Write(bytes.Repeat([]byte("x"), 64<<20))
					completed <- err
					return
				}
				chunk := bytes.Repeat([]byte("x"), 512)
				for range 128 << 10 {
					if _, err := c.Writer.Write(chunk); err != nil {
						completed <- fmt.Errorf("write before flush: %w", err)
						return
					}
					if err := httpresponse.Flush(c.Writer); err != nil {
						completed <- err
						return
					}
				}
				completed <- nil
			})
			server := httptest.NewUnstartedServer(router)
			server.Listener = bridgeForwardSmallBufferListener{server.Listener}
			server.Start()
			defer server.Close()
			connection, err := net.Dial("tcp", server.Listener.Addr().String())
			require.NoError(t, err)
			defer func() { _ = connection.Close() }()
			tcp, ok := connection.(*net.TCPConn)
			require.True(t, ok)
			require.NoError(t, tcp.SetReadBuffer(1024))
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
				t.Fatal("signed request was not accepted")
			}
			select {
			case <-execution.Done():
				require.Error(t, execution.Err())
			case <-time.After(4 * time.Second):
				t.Fatal("execution context did not expire")
			}
			select {
			case err := <-completed:
				var timeout net.Error
				require.True(t, errors.As(err, &timeout) && timeout.Timeout(), "expected a transport write timeout, got %v", err)
			case <-time.After(time.Second):
				t.Fatal("execution context expired but downstream " + operation + " remained blocked")
			}
		})
	}
}

func TestBridgeForwardWriteDeadlineDoesNotLeakToNextRequest(t *testing.T) {
	verifier, key, envelope, body := bridgeForwardMiddlewareFixture(t)
	envelope.ExpiresAt = time.Now().Unix() + 2
	envelope.Deadline = envelope.ExpiresAt + 1
	store := &bridgeForwardTestStore{requests: map[string]string{}, sent: map[string]bool{}, available: true}
	router := gin.New()
	router.POST(custombridge.ForwardPath, ClientRequestID(), func(c *gin.Context) {
		groupID := key.GroupID
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: key.InternalKeyID, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI}})
	}, BridgeForward(verifier, store, true, 1024), func(c *gin.Context) { c.String(http.StatusOK, "bridge complete") })
	router.GET("/ordinary", func(c *gin.Context) { c.String(http.StatusOK, "ordinary complete") })
	server := httptest.NewServer(router)
	defer server.Close()
	require.Zero(t, server.Config.WriteTimeout)
	connection, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	require.NoError(t, connection.SetDeadline(time.Now().Add(6*time.Second)))
	request, err := http.NewRequest(http.MethodPost, server.URL+custombridge.ForwardPath, bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer synthetic-native-key")
	request.Header.Set("Content-Type", "application/json")
	require.NoError(t, custombridge.SignForwardRequest(request, body, envelope, key))
	require.NoError(t, request.Write(connection))
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, request)
	require.NoError(t, err)
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "bridge complete", string(responseBody))
	<-time.After(time.Until(time.Unix(envelope.Deadline, 0)) + 100*time.Millisecond)
	next, err := http.NewRequest(http.MethodGet, server.URL+"/ordinary", nil)
	require.NoError(t, err)
	require.NoError(t, next.Write(connection))
	response, err = http.ReadResponse(reader, next)
	require.NoError(t, err)
	responseBody, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "ordinary complete", string(responseBody))
}
