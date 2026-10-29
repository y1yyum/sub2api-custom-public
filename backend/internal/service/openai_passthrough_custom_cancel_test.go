//go:build unit

package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPassthroughCanceledReadyEventsNeverReplay(t *testing.T) {
	for _, ending := range []string{"failed", "completed", "eof", "read_error"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
			payload := "data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"r\",\"usage\":{\"input_tokens\":13}}}\n\n"
			switch ending {
			case "failed":
				payload += "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"r\",\"error\":{\"message\":\"An error occurred while processing your request.\"},\"usage\":{\"input_tokens\":13,\"output_tokens\":4}}}\n\n"
			case "completed":
				payload += "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"usage\":{\"input_tokens\":13,\"output_tokens\":4}}}\n\n"
			}
			var reader io.Reader = strings.NewReader(payload)
			if ending == "read_error" {
				reader = io.MultiReader(reader, passthroughCancelErrorReader{})
			}
			svc := &OpenAIGatewayService{}
			result, err := svc.handleStreamingResponsePassthrough(context.Background(), &http.Response{Header: http.Header{}, Body: io.NopCloser(reader)}, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt", "gpt")
			require.Error(t, err)
			var replay *UpstreamFailoverError
			require.False(t, errors.As(err, &replay))
			_, compact := asOpenAICompactFallbackSignal(err)
			require.False(t, compact)
			require.True(t, result.clientDisconnect)
			require.Equal(t, 13, result.usage.InputTokens)
			if ending == "failed" || ending == "completed" {
				require.Equal(t, 4, result.usage.OutputTokens)
			}
			require.False(t, c.Writer.Written())
		})
	}
}

type passthroughCancelErrorReader struct{}

func (passthroughCancelErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestPassthroughCancelClosesBlockedScannerWithinTotalBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}
	done := make(chan error, 1)
	go func() {
		r, e := svc.handleStreamingResponsePassthrough(context.Background(), &http.Response{Header: http.Header{}, Body: reader}, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt", "gpt")
		if r == nil || !r.clientDisconnect {
			done <- errors.New("disconnect result absent")
			return
		}
		done <- e
	}()
	cancel()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "disconnect timeout")
	case <-time.After(3 * time.Second):
		t.Fatal("unbounded canceled passthrough drain")
	}
	_, err := writer.Write([]byte("late"))
	require.Error(t, err, "reader must be closed on return")
}

func TestOpenAIPassthroughForwardCanceledUsageSettlesWithoutReplay(t *testing.T) {
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
				result, err := svc.Forward(ctx, c, newOpenAIPartialUsageAccount(true), body)
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

type passthroughCancelFailWriter struct{ http.ResponseWriter }

func (passthroughCancelFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (passthroughCancelFailWriter) Flush()                    {}
func TestPassthroughWriteFailureWithBackgroundContextHasTotalDrainBound(t *testing.T) {
	c, _ := gin.CreateTestContext(passthroughCancelFailWriter{httptest.NewRecorder()})
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}
	done := make(chan error, 1)
	go func() {
		r, e := svc.handleStreamingResponsePassthrough(context.Background(), &http.Response{Header: http.Header{}, Body: reader}, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt", "gpt")
		if r == nil || !r.clientDisconnect {
			done <- errors.New("disconnect result absent")
			return
		}
		done <- e
	}()
	_, err := io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
	require.NoError(t, err)
	select {
	case err := <-done:
		require.ErrorContains(t, err, "disconnect timeout")
	case <-time.After(3 * time.Second):
		t.Fatal("unbounded write-failed passthrough drain")
	}
	_, err = writer.Write([]byte("late"))
	require.Error(t, err)
}

// Close occurs after the streaming classifier has built its error but before
// Forward can consider retry. This deterministically exercises that race.
type passthroughCancelOnClose struct {
	io.Reader
	cancel context.CancelFunc
}

func (b passthroughCancelOnClose) Close() error { b.cancel(); return nil }
func TestPassthroughCancellationAtClassifierReturnRemovesReplayIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := []byte(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":true}`)
	c, _ := newOpenAIPartialUsageContext(t, body)
	c.Request = c.Request.WithContext(ctx)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: passthroughCancelOnClose{Reader: strings.NewReader("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"An error occurred while processing your request.\"},\"usage\":{\"input_tokens\":13,\"output_tokens\":4}}}\n\n"), cancel: cancel}}}
	svc := newOpenAIPartialUsageService(upstream)
	result, err := svc.Forward(ctx, c, newOpenAIPartialUsageAccount(true), body)
	require.Error(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Len(t, upstream.requests, 1)
	var replay *UpstreamFailoverError
	require.False(t, errors.As(err, &replay))
	_, compact := asOpenAICompactFallbackSignal(err)
	require.False(t, compact)
}

func TestPassthroughCanceledCyberFailurePreservesUsageAudit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	payload := `{"type":"response.failed","response":{"error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":13,"output_tokens":4}}}`
	svc := &OpenAIGatewayService{}
	result, err := svc.handleStreamingResponsePassthrough(context.Background(), &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + payload + "\n\n"))}, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt", "gpt")
	require.Error(t, err)
	require.True(t, result.clientDisconnect)
	require.Equal(t, 13, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, "cyber_policy", mark.Code)
	require.Equal(t, 13, mark.UpstreamInTok)
	require.Equal(t, 4, mark.UpstreamOutTok)
	require.Equal(t, payload, mark.Body)
	var replay *UpstreamFailoverError
	require.False(t, errors.As(err, &replay))
	require.False(t, c.Writer.Written())
}
