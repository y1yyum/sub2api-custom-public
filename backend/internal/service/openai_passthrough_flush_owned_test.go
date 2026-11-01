//go:build unit

package service

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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Writes deliberately keep succeeding after FlushError, as a buffered transport
// can do. A subsequent Write error must not mask a missing flush-error transition.
type passthroughFlushOnlyWriter struct {
	header             http.Header
	body               bytes.Buffer
	fail               bool
	failed             bool
	initialFlushed     chan struct{}
	secondFlushed      chan struct{}
	initialOnce        sync.Once
	secondOnce         sync.Once
	flushErrors        int
	writesAfterFailure int
}

func (w *passthroughFlushOnlyWriter) Header() http.Header { return w.header }
func (w *passthroughFlushOnlyWriter) WriteHeader(int)     {}
func (w *passthroughFlushOnlyWriter) Write(p []byte) (int, error) {
	if w.failed {
		w.writesAfterFailure++
	}
	return w.body.Write(p)
}
func (w *passthroughFlushOnlyWriter) Flush() { _ = w.FlushError() }
func (w *passthroughFlushOnlyWriter) FlushError() error {
	if strings.Contains(w.body.String(), "second-visible-output") {
		if w.fail {
			w.failed = true
			w.flushErrors++
		}
		w.secondOnce.Do(func() { close(w.secondFlushed) })
		if w.fail {
			return io.ErrClosedPipe
		}
	}
	if strings.Contains(w.body.String(), "initial-visible-output") {
		w.initialOnce.Do(func() { close(w.initialFlushed) })
	}
	return nil
}

type passthroughOwnedBody struct {
	*io.PipeReader
	closed chan struct{}
	once   sync.Once
}

func (b *passthroughOwnedBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return b.PipeReader.Close()
}

func TestPassthroughFlushOnlyDisconnectRetainsLateUsage(t *testing.T) {
	for _, ending := range []string{"failed", "completed", "budget"} {
		t.Run(ending, func(t *testing.T) { runPassthroughFlushOnly(t, true, ending, false) })
	}
}

func TestPassthroughFlushOnlyConnectedControl(t *testing.T) {
	runPassthroughFlushOnly(t, false, "failed", false)
}

func TestPassthroughFlushOnlyForwardPreservesResult(t *testing.T) {
	for _, ending := range []string{"failed", "completed", "budget"} {
		t.Run(ending, func(t *testing.T) { runPassthroughFlushOnly(t, true, ending, true) })
	}
}

func TestPassthroughFlushOnlyForwardConnectedControl(t *testing.T) {
	runPassthroughFlushOnly(t, false, "failed", true)
}

func runPassthroughFlushOnly(t *testing.T, disconnect bool, ending string, forward bool) {
	t.Helper()
	ctx := context.Background() // A context cancellation cannot hide FlushError loss.
	writer := &passthroughFlushOnlyWriter{
		header: make(http.Header), fail: disconnect,
		initialFlushed: make(chan struct{}), secondFlushed: make(chan struct{}),
	}
	c, _ := gin.CreateTestContext(writer)
	unwrapper, ok := c.Writer.(interface{ Unwrap() http.ResponseWriter })
	require.True(t, ok, "fixture must use Gin's actual ResponseWriter wrapper")
	require.Same(t, writer, unwrapper.Unwrap())
	requestBody := []byte(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	frames := []string{
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"flush-only\",\"usage\":{\"input_tokens\":13}}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"initial-visible-output\"}\n\n",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"second-visible-output\"}\n\n",
		"data: {\"type\":\"error\",\"error\":{\"code\":\"upstream_error\",\"message\":\"temporary failure\"}}\n\n",
		"data: {\"type\":\"response." + ending + "\",\"response\":{\"id\":\"flush-only\",\"usage\":{\"input_tokens\":29,\"output_tokens\":7}}}\n\n",
	}
	for _, frame := range frames {
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "data: ") {
				require.True(t, json.Valid([]byte(strings.TrimPrefix(line, "data: "))))
			}
		}
	}
	pr, pw := io.Pipe()
	body := &passthroughOwnedBody{PipeReader: pr, closed: make(chan struct{})}
	t.Cleanup(func() { _ = body.Close(); _ = pw.Close() })
	producerDone := make(chan struct{})
	guardExpired := make(chan struct{})
	// This watchdog bounds a buggy implementation too. It is a fixture failure,
	// not the production drain budget and not the source of a passing result.
	guard := time.AfterFunc(5*time.Second, func() { close(guardExpired); _ = pw.CloseWithError(io.ErrUnexpectedEOF) })
	defer guard.Stop()
	go func() {
		defer close(producerDone)
		defer pw.Close()
		if _, err := io.WriteString(pw, frames[0]); err != nil {
			return
		}
		select {
		case <-writer.initialFlushed:
		case <-body.closed:
			return
		case <-guardExpired:
			return
		}
		if _, err := io.WriteString(pw, frames[1]); err != nil {
			return
		}
		// Bare errors are suppressed in connected OpenAI passthrough. Fail on a
		// visible event first; only release final usage AFTER its actual flush.
		select {
		case <-writer.secondFlushed:
		case <-body.closed:
			return
		case <-guardExpired:
			return
		}
		if _, err := io.WriteString(pw, frames[2]); err != nil {
			return
		}
		if ending != "budget" {
			if _, err := io.WriteString(pw, frames[3]); err != nil {
				return
			}
		}
		if disconnect {
			// A terminal must finish without upstream EOF; budget case must close
			// the held-open body itself, rather than wait for this watchdog.
			select {
			case <-body.closed:
			case <-guardExpired:
			}
		}
	}()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body}
	var result *openaiStreamingResultPassthrough
	var err error
	var upstream *httpUpstreamRecorder
	started := time.Now()
	if forward {
		upstream = &httpUpstreamRecorder{resp: resp}
		svc := newOpenAIPartialUsageService(upstream)
		svc.cfg.Gateway.StreamDataIntervalTimeout = 2
		var forwarded *OpenAIForwardResult
		forwarded, err = svc.Forward(ctx, c, newOpenAIPartialUsageAccount(true), requestBody)
		if forwarded != nil {
			// Only normalize fields actually returned by Forward. A nil result
			// remains nil, exposing loss at the outer result-handoff boundary.
			result = &openaiStreamingResultPassthrough{clientDisconnect: forwarded.ClientDisconnect, usage: &forwarded.Usage}
		}
	} else {
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 2}}}
		result, err = svc.handleStreamingResponsePassthrough(ctx, resp,
			c, &Account{ID: 1, Platform: PlatformOpenAI}, started, "gpt-5.4", "gpt-5.4")
	}
	elapsed := time.Since(started)
	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("upstream fixture producer leaked")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("upstream body not closed")
	}
	select {
	case <-writer.initialFlushed:
	default:
		t.Fatal("initial output was never successfully flushed")
	}
	select {
	case <-writer.secondFlushed:
	default:
		t.Fatal("second visible event was never flushed")
	}
	require.NoError(t, ctx.Err())
	if forward {
		require.Len(t, upstream.requests, 1, "late disconnect must not replay upstream request")
	}
	require.Error(t, err, "bare upstream error remains an error, including after later completed usage")
	var replay *UpstreamFailoverError
	require.False(t, errors.As(err, &replay), "partial output/disconnect cannot request replay")
	_, compact := asOpenAICompactFallbackSignal(err)
	require.False(t, compact)
	if !disconnect {
		if forward {
			// Preserve the existing connected non-cyber error contract; this
			// patch does not introduce partial settlement for connected errors.
			require.Nil(t, result)
		} else {
			require.NotNil(t, result)
			require.False(t, result.clientDisconnect)
			require.Equal(t, 29, result.usage.InputTokens)
			require.Equal(t, 7, result.usage.OutputTokens)
		}
		require.Contains(t, writer.body.String(), `"type":"response.failed"`)
		require.Zero(t, writer.flushErrors)
		return
	}
	require.True(t, writer.failed, "fixture did not exercise FlushError")
	require.NotNil(t, result, "disconnect usage result must survive the forwarding boundary")
	require.True(t, result.clientDisconnect, "FlushError must start disconnect drain while context stays live")
	select {
	case <-guardExpired:
		t.Fatal("fixture watchdog, not production drain, terminated stream")
	default:
	}
	require.Zero(t, writer.writesAfterFailure, "draining must not emit more downstream bytes")
	require.Equal(t, 1, writer.flushErrors, "do not flush again after disconnect")
	if ending == "budget" {
		require.GreaterOrEqual(t, elapsed, 1800*time.Millisecond)
		require.Less(t, elapsed, 4500*time.Millisecond)
		require.ErrorContains(t, err, "disconnect timeout")
		require.Equal(t, 13, result.usage.InputTokens)
		require.Zero(t, result.usage.OutputTokens)
	} else {
		require.Less(t, elapsed, 1500*time.Millisecond, "authoritative terminal must not wait for drain timeout or EOF")
		require.Equal(t, 29, result.usage.InputTokens)
		require.Equal(t, 7, result.usage.OutputTokens)
	}
}
