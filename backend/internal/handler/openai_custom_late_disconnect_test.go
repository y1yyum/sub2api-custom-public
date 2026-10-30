//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// This is the real underlying HTTP writer, wrapped by Gin's responseWriter.
// Flush deliberately models net/http's legacy void method: callers that want
// its error must reach FlushError through the supported Unwrap chain.
type lateDisconnectWriter struct {
	header                 http.Header
	body                   bytes.Buffer
	mode                   string
	ctx                    context.Context
	initialFlushed         chan struct{}
	failed                 chan struct{}
	initialOnce            sync.Once
	failOnce               sync.Once
	pendingError           bool
	failureWithLiveContext bool
	writesAfterFailure     int
	flushErrors            int
}

func (w *lateDisconnectWriter) Header() http.Header { return w.header }
func (w *lateDisconnectWriter) WriteHeader(int)     {}
func (w *lateDisconnectWriter) markFailed() {
	w.failOnce.Do(func() {
		w.failureWithLiveContext = w.ctx.Err() == nil
		close(w.failed)
	})
}
func (w *lateDisconnectWriter) Write(p []byte) (int, error) {
	select {
	case <-w.failed:
		w.writesAfterFailure++
		return 0, io.ErrClosedPipe
	default:
	}
	if bytes.Contains(p, []byte(`"type":"error"`)) {
		w.pendingError = true
		if w.mode == "write" {
			w.markFailed()
			return 0, io.ErrClosedPipe
		}
	}
	return w.body.Write(p)
}
func (w *lateDisconnectWriter) Flush() { _ = w.FlushError() }
func (w *lateDisconnectWriter) FlushError() error {
	if w.mode == "flush" && w.pendingError {
		w.flushErrors++
		w.markFailed()
		return io.ErrClosedPipe
	}
	if strings.Contains(w.body.String(), "initial-visible-output") {
		w.initialOnce.Do(func() { close(w.initialFlushed) })
	}
	return nil
}

type lateDisconnectBody struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func (b *lateDisconnectBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return b.ReadCloser.Close()
}

type lateDisconnectUpstream struct {
	service.HTTPUpstream
	writer *lateDisconnectWriter
	calls  int
	closed chan struct{}
	done   chan struct{}
	frames []string
}

func (u *lateDisconnectUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	r, w := io.Pipe()
	go func() {
		defer close(u.done)
		defer w.Close()
		if _, err := io.WriteString(w, u.frames[0]); err != nil {
			return
		}
		select {
		case <-u.writer.initialFlushed:
		case <-u.closed:
			return
		case <-time.After(3 * time.Second):
			return
		}
		if _, err := io.WriteString(w, u.frames[1]); err != nil {
			return
		}
		// Final counters cannot be read before downstream failure is observed.
		select {
		case <-u.writer.failed:
		case <-u.closed:
			return
		case <-time.After(3 * time.Second):
			return
		}
		_, _ = io.WriteString(w, u.frames[2])
	}()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &lateDisconnectBody{ReadCloser: r, closed: u.closed}}, nil
}

func TestCustomLateBareErrorDisconnectSettlesFinalUsageExactlyOnce(t *testing.T) {
	for _, mode := range []string{"write", "flush"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background() // No cancellation can mask the write-time transition.
			writer := &lateDisconnectWriter{header: make(http.Header), mode: mode, ctx: ctx, initialFlushed: make(chan struct{}), failed: make(chan struct{})}
			upstream := &lateDisconnectUpstream{writer: writer, closed: make(chan struct{}), done: make(chan struct{}), frames: []string{
				"data: {\"type\":\"response.created\",\"response\":{\"id\":\"late\",\"usage\":{\"input_tokens\":13}}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"initial-visible-output\"}\n\n",
				"data: {\"type\":\"error\",\"error\":{\"code\":\"upstream_error\",\"message\":\"temporary failure\"}}\n\n",
				"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"late\",\"error\":{\"code\":\"upstream_error\",\"message\":\"failed\"},\"usage\":{\"input_tokens\":29,\"output_tokens\":7}}}\n\n",
			}}
			for _, frame := range upstream.frames {
				for _, line := range strings.Split(frame, "\n") {
					if strings.HasPrefix(line, "data: ") {
						require.True(t, json.Valid([]byte(strings.TrimPrefix(line, "data: "))))
					}
				}
			}
			groupID := int64(4206)
			account := service.Account{ID: 9910, Name: "fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.example.test"}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.StreamDataIntervalTimeout = 2
			accounts := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}
			logs := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 4)}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			svc := service.NewOpenAIGatewayService(accounts, logs, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			h := NewOpenAIGatewayHandler(svc, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			c, _ := gin.CreateTestContext(writer)
			// Verify this is Gin's genuine transport wrapper, not a synthetic
			// replacement exposing an interface absent from the real route.
			unwrapper, ok := c.Writer.(interface{ Unwrap() http.ResponseWriter })
			require.True(t, ok)
			require.Same(t, writer, unwrapper.Unwrap())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","input":"hello","stream":true}`)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1806, GroupID: &groupID, User: &service.User{ID: 1706, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true}})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1706, Concurrency: 0})
			// Use the same pooled capture wrapper as OpsErrorLoggerMiddleware;
			// its delegated-call lifetime must not hide transport flush errors.
			originalWriter := c.Writer
			capture := acquireOpsCaptureWriter(originalWriter)
			capture.setContext(c)
			c.Writer = capture
			h.Responses(c)
			c.Writer = originalWriter
			releaseOpsCaptureWriter(capture)
			select {
			case <-upstream.done:
			case <-time.After(time.Second):
				t.Fatal("upstream writer leaked after body close")
			}
			require.NoError(t, ctx.Err())
			require.True(t, writer.failureWithLiveContext, "failure must happen after connected classification, not early context cancellation")
			select {
			case <-writer.initialFlushed:
			default:
				t.Fatal("partial output was not successfully flushed before bare error")
			}
			select {
			case <-upstream.closed:
			default:
				t.Fatal("upstream body was not closed")
			}
			require.Equal(t, 1, upstream.calls, "late disconnect must never replay")
			require.Len(t, logs.created, 1, "actual repository must receive exactly one settlement")
			recorded := <-logs.created
			require.Equal(t, 29, recorded.InputTokens)
			require.Equal(t, 7, recorded.OutputTokens)
			require.Zero(t, writer.writesAfterFailure, "drain must not attempt more downstream output")
			require.NotContains(t, writer.body.String(), `"type":"response.failed"`)
			if mode == "flush" {
				require.Equal(t, 1, writer.flushErrors)
			}
			require.False(t, errors.Is(ctx.Err(), context.Canceled))
		})
	}
}
