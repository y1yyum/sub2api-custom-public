//go:build unit

package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Reuse #27's always-successful buffered Writes, but fail on the final event's
// flush rather than its error message (which the gateway may sanitize).
type passthroughEOFFlushWriter struct {
	*passthroughFlushOnlyWriter
	finalEvent string
}

func (w *passthroughEOFFlushWriter) Flush() { _ = w.FlushError() }
func (w *passthroughEOFFlushWriter) FlushError() error {
	if strings.Contains(w.body.String(), `"type":"`+w.finalEvent+`"`) {
		w.secondOnce.Do(func() { close(w.secondFlushed) })
		if w.fail {
			w.failed = true
			w.flushErrors++
			return io.ErrClosedPipe
		}
		return nil
	}
	return w.passthroughFlushOnlyWriter.FlushError()
}

// These are complete JSON data lines with either no EOF newline, one newline,
// or a normal SSE blank event boundary. The first two exercise the repository's
// existing EOF-residual forwarding contract, not concatenated-JSON repair.
func TestPassthroughEOFFlushForwardResult(t *testing.T) {
	for _, ending := range []string{"failed", "completed", "in_progress"} {
		for _, suffix := range []struct{ name, text string }{
			{"no_newline", ""}, {"one_newline", "\n"}, {"event_boundary", "\n\n"},
		} {
			for _, disconnect := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/disconnect_%t", ending, suffix.name, disconnect)
				t.Run(name, func(t *testing.T) {
					runPassthroughEOFFlushForward(t, ending, suffix.text, disconnect)
				})
			}
		}
	}
}

func runPassthroughEOFFlushForward(t *testing.T, ending, suffix string, disconnect bool) {
	t.Helper()
	ctx := context.Background()
	writer := &passthroughEOFFlushWriter{
		passthroughFlushOnlyWriter: &passthroughFlushOnlyWriter{
			header: make(http.Header), fail: disconnect,
			initialFlushed: make(chan struct{}), secondFlushed: make(chan struct{}),
		},
		finalEvent: "response." + ending,
	}
	c, _ := gin.CreateTestContext(writer)
	unwrapper, ok := c.Writer.(interface{ Unwrap() http.ResponseWriter })
	require.True(t, ok)
	require.Same(t, writer, unwrapper.Unwrap(), "use Gin's real transport wrapper")
	requestBody := []byte(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	initialJSON := `{"type":"response.output_text.delta","delta":"initial-visible-output"}`
	finalJSON := fmt.Sprintf(`{"type":"response.%s","response":{"id":"eof-flush","usage":{"input_tokens":29,"output_tokens":7}}}`, ending)
	if ending == "failed" {
		finalJSON = `{"type":"response.failed","response":{"id":"eof-flush","error":{"code":"upstream_error","message":"temporary failure"},"usage":{"input_tokens":29,"output_tokens":7}}}`
	}
	require.True(t, json.Valid([]byte(initialJSON)))
	require.True(t, json.Valid([]byte(finalJSON)))
	initialFrame := "data: " + initialJSON + "\n\n"
	finalFrame := "data: " + finalJSON + suffix
	// Prove the scanner does not silently manufacture a final blank boundary
	// for these valid single-document payloads; otherwise this would retest #27.
	scanner := newOpenAISSEJSONDocumentScanner(bufio.NewScanner(strings.NewReader(finalFrame)))
	var scanned []string
	for scanner.Scan() {
		scanned = append(scanned, scanner.Text())
	}
	require.NoError(t, scanner.Err())
	wantLines := []string{"data: " + finalJSON}
	if suffix == "\n\n" {
		wantLines = append(wantLines, "")
	}
	require.Equal(t, wantLines, scanned)

	pr, pw := io.Pipe()
	body := &passthroughOwnedBody{PipeReader: pr, closed: make(chan struct{})}
	t.Cleanup(func() { _ = body.Close(); _ = pw.Close() })
	producerDone := make(chan struct{})
	producerResult := make(chan error, 1)
	go func() {
		defer close(producerDone)
		defer pw.Close() // Natural EOF, never a watchdog-induced passing result.
		if _, err := io.WriteString(pw, initialFrame); err != nil {
			producerResult <- err
			return
		}
		select {
		case <-writer.initialFlushed:
		case <-body.closed:
			producerResult <- errors.New("body closed before initial successful flush")
			return
		case <-time.After(2 * time.Second):
			producerResult <- errors.New("initial successful flush not observed")
			return
		}
		_, err := io.WriteString(pw, finalFrame)
		producerResult <- err
	}()
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body,
	}}
	svc := newOpenAIPartialUsageService(upstream)
	svc.cfg.Gateway.StreamDataIntervalTimeout = 2
	started := time.Now()
	result, err := svc.Forward(ctx, c, newOpenAIPartialUsageAccount(true), requestBody)
	require.Less(t, time.Since(started), 1500*time.Millisecond, "natural EOF must not wait for disconnect budget")
	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("upstream producer leaked")
	}
	require.NoError(t, <-producerResult)
	select {
	case <-body.closed:
	default:
		t.Fatal("upstream body not closed")
	}
	select {
	case <-writer.initialFlushed:
	default:
		t.Fatal("initial output never flushed successfully")
	}
	select {
	case <-writer.secondFlushed:
	default:
		t.Fatal("EOF residual output never flushed")
	}
	require.NoError(t, ctx.Err(), "context cancellation must not supply disconnect classification")
	require.Len(t, upstream.requests, 1, "output/EOF must not replay the upstream request")
	var replay *UpstreamFailoverError
	require.False(t, errors.As(err, &replay))
	_, compact := asOpenAICompactFallbackSignal(err)
	require.False(t, compact)
	if ending == "completed" {
		require.NoError(t, err)
	} else {
		require.Error(t, err)
	}
	if !disconnect {
		require.False(t, writer.failed)
		require.Zero(t, writer.flushErrors)
		if ending != "completed" {
			// Preserve connected failure/nonterminal EOF policy. This does not
			// broaden partial settlement to a still-connected upstream error.
			require.Nil(t, result)
			return
		}
	} else {
		require.True(t, writer.failed, "fixture must actually fail FlushError")
		require.Equal(t, 1, writer.flushErrors)
		require.Zero(t, writer.writesAfterFailure)
	}
	require.NotNil(t, result, "FlushError classification must precede the result handoff")
	require.Equal(t, disconnect, result.ClientDisconnect)
	require.Equal(t, 29, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
}
