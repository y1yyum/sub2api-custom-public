//go:build unit

package handler

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type opsDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	err      error
}

func (w *opsDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return w.err
}

type blockingOpsDeadlineWriter struct {
	gin.ResponseWriter
	started chan struct{}
	unblock chan struct{}
}

func (w *blockingOpsDeadlineWriter) SetWriteDeadline(time.Time) error {
	close(w.started)
	<-w.unblock
	return nil
}

func TestBridgeForwardOpsWriterDeadlineWaitsForRelease(t *testing.T) {
	pool := &deterministicOpsCaptureWriterStatePool{}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	inner := &blockingOpsDeadlineWriter{ctx.Writer, make(chan struct{}), make(chan struct{})}
	writer := acquireOpsCaptureWriterFromPool(pool, inner)
	var unblock sync.Once
	defer unblock.Do(func() { close(inner.unblock) })
	updated := make(chan error, 1)
	go func() { updated <- http.NewResponseController(writer).SetWriteDeadline(time.Now()) }()
	select {
	case <-inner.started:
	case <-time.After(time.Second):
		t.Fatal("write deadline was not delegated")
	}
	if !writer.state.mu.TryLock() {
		t.Fatal("state mutex held across delegated deadline update")
	}
	writer.state.mu.Unlock()
	released := make(chan struct{})
	go func() {
		releaseOpsCaptureWriter(writer)
		close(released)
	}()
	select {
	case <-released:
		t.Fatal("state released during active deadline update")
	case <-time.After(20 * time.Millisecond):
	}
	unblock.Do(func() { close(inner.unblock) })
	require.NoError(t, <-updated)
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("state not released after deadline update")
	}
	require.Len(t, pool.states, 1)
}

func TestBridgeForwardOpsWriterDeadlineLease(t *testing.T) {
	pool := &deterministicOpsCaptureWriterStatePool{}
	first := &opsDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	firstContext, _ := gin.CreateTestContext(first)
	stale := acquireOpsCaptureWriterFromPool(pool, firstContext.Writer)
	deadline := time.Now().Add(time.Minute)
	require.NoError(t, http.NewResponseController(stale).SetWriteDeadline(deadline))
	require.Equal(t, deadline, first.deadline)
	first.err = errors.New("deadline transport failure")
	require.ErrorIs(t, http.NewResponseController(stale).SetWriteDeadline(time.Time{}), first.err)
	releaseOpsCaptureWriter(stale)

	second := &opsDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	secondContext, _ := gin.CreateTestContext(second)
	current := acquireOpsCaptureWriterFromPool(pool, secondContext.Writer)
	defer releaseOpsCaptureWriter(current)
	require.Same(t, stale.state, current.state)
	require.Error(t, http.NewResponseController(stale).SetWriteDeadline(deadline))
	require.True(t, second.deadline.IsZero(), "a stale lease must not change the next response")
	require.NoError(t, http.NewResponseController(current).SetWriteDeadline(deadline))
	require.Equal(t, deadline, second.deadline)

	unsupportedContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	unsupported := acquireOpsCaptureWriter(unsupportedContext.Writer)
	defer releaseOpsCaptureWriter(unsupported)
	require.ErrorIs(t, http.NewResponseController(unsupported).SetWriteDeadline(deadline), http.ErrNotSupported)
}

func TestBridgeForwardOpsWriterAcceptsSignedRequest(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hello"}`)
	key, envelope := bridgeHandlerEnvelope(body)
	verifier, err := custombridge.NewForwardVerifier("core", []custombridge.SigningKey{key})
	require.NoError(t, err)
	attempts := &bridgeHandlerAttempts{received: map[string]bool{}, sent: map[string]bool{}}
	router := gin.New()
	router.POST(custombridge.ForwardPath, middleware.ClientRequestID(), OpsErrorLoggerMiddleware(nil), func(c *gin.Context) {
		groupID := key.GroupID
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
			ID: key.InternalKeyID, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI},
		})
	}, middleware.BridgeForward(verifier, attempts, true, 1024), func(c *gin.Context) {
		c.String(http.StatusOK, "accepted")
	})
	server := httptest.NewServer(router)
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+custombridge.ForwardPath, bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer synthetic-core-service-key")
	request.Header.Set("Content-Type", "application/json")
	require.NoError(t, custombridge.SignForwardRequest(request, body, envelope, key))
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, string(responseBody))
	require.Equal(t, "accepted", string(responseBody))
}
