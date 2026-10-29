//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func TestOpenAIGatewayService_Forward_CanceledStreamPreservesUsage(t *testing.T) {
	for _, ending := range []string{"eof", "read_error", "response_failed"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := []byte(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":true}`)
			c, recorder := newOpenAIPartialUsageContext(t, body)
			c.Request = c.Request.WithContext(ctx)
			pr, pw := io.Pipe()
			defer func() { _ = pr.Close() }()
			defer func() { _ = pw.Close() }()
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       pr,
			}}
			svc := newOpenAIPartialUsageService(upstream)
			svc.cfg.Gateway.StreamDataIntervalTimeout = 2
			written := make(chan error, 1)
			go func() {
				defer func() { _ = pw.Close() }()
				_, err := io.WriteString(pw, partialOpenAIResponsesSSE(false)+"\n")
				written <- err
				if err != nil {
					return
				}
				<-ctx.Done()
				switch ending {
				case "read_error":
					_ = pw.CloseWithError(io.ErrUnexpectedEOF)
				case "response_failed":
					_, _ = io.WriteString(pw, "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_partial\",\"error\":{\"message\":\"An error occurred while processing your request.\"},\"usage\":{\"input_tokens\":13,\"output_tokens\":4}}}\n\n")
				}
			}()
			type outcome struct {
				result *OpenAIForwardResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := svc.Forward(ctx, c, newOpenAIPartialUsageAccount(false), body)
				done <- outcome{result, err}
			}()
			select {
			case err := <-written:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("preamble was not read")
			}
			cancel()
			select {
			case got := <-done:
				require.Error(t, got.err)
				var failover *UpstreamFailoverError
				require.Len(t, upstream.requests, 1, "canceled requests must not retry")
				if errors.As(got.err, &failover) {
					t.Error("canceled client was classified as replayable failover")
				}
				require.NotNil(t, got.result, "already observed usage must reach billing when retry is canceled")
				require.True(t, got.result.ClientDisconnect)
				require.Equal(t, 13, got.result.Usage.InputTokens)
				require.Empty(t, recorder.Body.String(), "cancellation must not inject an error event")
				if ending == "response_failed" {
					require.Equal(t, 4, got.result.Usage.OutputTokens)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Forward did not finish")
			}
		})
	}
}

func TestOpenAIStreamingResponse_GrokWrappedBodyClosesOnTerminalOrCancel(t *testing.T) {
	for _, cancelClient := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_%t", cancelClient), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstreamClosed := make(chan struct{})
			allowTerminal := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(upstreamClosed)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-allowTerminal:
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_review\",\"usage\":{\"input_tokens\":13,\"output_tokens\":4,\"input_tokens_details\":{\"cached_tokens\":2}}}}\n\n")
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			resp, err := server.Client().Get(server.URL)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			account := &Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth}
			_, mapping, err := patchGrokResponsesBodyWithClientTools([]byte(`{"input":"hello","tools":[{"type":"custom","name":"apply_patch"}]}`), "grok-4.6")
			require.NoError(t, err)
			resp.Body = newGrokResponsesBillingPingFilterBody(resp.Body, account, defaultMaxLineSize)
			resp.Body = newGrokResponsesClientToolStreamBody(resp.Body, mapping, defaultMaxLineSize)
			recorder := newOpenAIResponseFlushRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request = c.Request.WithContext(ctx)
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}, toolCorrector: NewCodexToolCorrector()}
			resultCh := make(chan *openaiStreamingResult, 1)
			errCh := make(chan error, 1)
			go func() {
				result, err := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "grok", "grok")
				resultCh <- result
				errCh <- err
			}()
			waitOpenAIResponseFlushCount(t, recorder, 1)
			if cancelClient {
				cancel()
			} else {
				close(allowTerminal)
			}
			var result *openaiStreamingResult
			select {
			case result = <-resultCh:
			case <-time.After(4 * time.Second):
				t.Fatal("stream did not finish within bounded drain")
			}
			err = <-errCh
			if cancelClient {
				require.ErrorContains(t, err, "disconnect timeout")
			} else {
				require.NoError(t, err)
				require.Equal(t, 13, result.usage.InputTokens)
				require.Equal(t, 4, result.usage.OutputTokens)
				require.Equal(t, 2, result.usage.CacheReadInputTokens)
			}
			select {
			case <-upstreamClosed:
			case <-time.After(time.Second):
				t.Fatal("wrapper Close did not close the HTTP source")
			}
		})
	}
}

func newOpenAIPartialUsageContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c, recorder
}

func newOpenAIPartialUsageAccount(passthrough bool) *Account {
	extra := map[string]any{"openai_responses_supported": true}
	if passthrough {
		extra["openai_passthrough"] = true
	}
	return &Account{
		ID: 811, Name: "openai-partial-usage", Platform: PlatformOpenAI,
		Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       extra, Status: StatusActive, Schedulable: true,
	}
}

func newOpenAIPartialUsageService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	}
}

func partialOpenAIResponsesSSE(withOutput bool) string {
	lines := []string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_partial","model":"gpt-5.4","status":"in_progress","output":[],"usage":{"input_tokens":13,"output_tokens":0}}}`,
		"",
	}
	if withOutput {
		lines = append(lines,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","response_id":"resp_partial","delta":"partial","usage":{"input_tokens":13,"output_tokens":4}}`,
			"",
		)
	}
	return strings.Join(lines, "\n")
}

func TestCustomCanceledReadyErrorNeverReturnsReplaySignal(t *testing.T) {
	for _, event := range []string{"response.failed", "error"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
		c, recorder := newOpenAIPartialUsageContext(t, body)
		c.Request = c.Request.WithContext(ctx)
		frame := `{"type":"error","error":{"code":"upstream_error","message":"temporary upstream failure"}}`
		if event == "response.failed" {
			frame = `{"type":"response.failed","response":{"error":{"code":"upstream_error","message":"temporary upstream failure"},"usage":{"input_tokens":13,"output_tokens":4}}}`
		}
		response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(partialOpenAIResponsesSSE(false) + "\n" + "data: " + frame + "\n\n"))}
		svc := newOpenAIPartialUsageService(nil)
		result, err := svc.handleStreamingResponse(context.Background(), response, c, newOpenAIPartialUsageAccount(false), time.Now(), "gpt-5.4", "gpt-5.4")
		require.Error(t, err)
		var replay *UpstreamFailoverError
		require.False(t, errors.As(err, &replay))
		require.True(t, result.clientDisconnect)
		require.Equal(t, 13, result.usage.InputTokens)
		require.Empty(t, recorder.Body.String())
	}
}

func TestCustomWriteFailureDrainIsBoundedWithoutCancelableContext(t *testing.T) {
	recorder := newOpenAIResponseFlushRecorder()
	recorder.failAfterWrites = 0
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	require.Nil(t, c.Request.Context().Done())
	reader, writer := io.Pipe()
	defer writer.Close()
	closed := make(chan struct{})
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &customCancelCloseBody{ReadCloser: reader, closed: closed}}
	go func() {
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"usage\":{\"input_tokens\":13,\"output_tokens\":4}}\n\n")
		<-closed
	}()
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}
	type outcome struct {
		result *openaiStreamingResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := svc.handleStreamingResponse(context.Background(), response, c, &Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeAPIKey}, time.Now(), "grok", "grok")
		done <- outcome{result, err}
	}()
	select {
	case got := <-done:
		require.Error(t, got.err)
		require.True(t, got.result.clientDisconnect)
		var replay *UpstreamFailoverError
		require.False(t, errors.As(got.err, &replay))
	case <-time.After(4 * time.Second):
		t.Fatal("write failure left scanner blocked beyond drain budget")
	}
	select {
	case <-closed:
	default:
		t.Fatal("upstream body was not closed")
	}
}

type customCancelCloseBody struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func (b *customCancelCloseBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return b.ReadCloser.Close()
}
