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
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const wrappedForbiddenFixture = `{"response":{"created_at":1791078420,"error":{"code":"upstream_error","message":"Upstream access forbidden, please contact administrator"},"id":"resp_fixture","model":"gpt-6-astra","object":"response","status":"failed"},"sequence_number":8,"type":"response.failed"}`

func TestOpenAIWrappedUpstreamErrorKeepsGatewayFailureSemantics(t *testing.T) {
	payload := []byte(wrappedForbiddenFixture)
	message := extractOpenAISSEErrorMessage(payload)
	require.Equal(t, http.StatusBadGateway, openAIStreamFailureStatus(payload, message), "structured upstream_error must not become a request-level 403 based only on message text")
	require.True(t, openAIStreamFailedEventShouldFailover(payload, message))
	require.True(t, openAIStreamErrorEventShouldFailover(payload, message))
}

func TestOpenAIWrappedUpstreamFailureBeforeOutputReturnsRetrySignal(t *testing.T) {
	preamble := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
		"data: {\"type\":\"response.content_part.added\",\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n\n"
	recorder := newOpenAIResponseFlushRecorder()
	_, err := runOpenAIResponseFlushTestWithAccount(recorder, io.NopCloser(strings.NewReader(preamble+"event: response.failed\ndata: "+wrappedForbiddenFixture+"\n\n")), config.GatewayConfig{}, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey})
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.IsCredentialFailure(), "a relay error must not permanently disable an otherwise valid credential")
	body, _ := recorder.snapshot()
	require.Empty(t, body, "the failed attempt must remain staged so the handler can select the next account")
}

func TestOpenAIWrappedUpstreamFailureAfterContentNeverReplays(t *testing.T) {
	for _, content := range []string{
		`{"type":"response.output_text.delta","delta":"already delivered"}`,
		`{"type":"response.function_call_arguments.delta","delta":"{\"path\":"}`,
	} {
		recorder := newOpenAIResponseFlushRecorder()
		_, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader("data: "+content+"\n\ndata: "+wrappedForbiddenFixture+"\n\n")), config.GatewayConfig{})
		require.Error(t, err)
		var failover *UpstreamFailoverError
		require.False(t, errors.As(err, &failover))
		body, _ := recorder.snapshot()
		require.Contains(t, body, content)
		require.Contains(t, body, "response.failed")
	}
}

func TestOpenAIWrappedUpstreamErrorDoesNotOverrideRequestRestrictions(t *testing.T) {
	for _, payload := range []string{
		`{"type":"response.failed","response":{"error":{"code":"upstream_error","status_code":403,"message":"forbidden"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"upstream_error","type":"permission_error","message":"forbidden"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"upstream_error","message":"content_policy: forbidden request"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"upstream_error","message":"context_length_exceeded"}}}`,
	} {
		message := extractOpenAISSEErrorMessage([]byte(payload))
		require.False(t, openAIStreamFailedEventShouldFailover([]byte(payload), message), payload)
		require.False(t, openAIStreamErrorEventShouldFailover([]byte(payload), message), payload)
	}
}

// A wrapper code is weaker evidence than an explicit request error or type.
func TestOpenAIWrappedUpstreamExplicitRequestStatusNeverReplays(t *testing.T) {
	for _, event := range []string{"response.failed", "error"} {
		for _, status := range []int{400, 404, 405, 409, 410, 413, 415, 422} {
			for _, errType := range []string{"", "upstream_error", "authentication_error", "permission_error", "invalid_request_error"} {
				inner := fmt.Sprintf(`{"code":"upstream_error","status_code":%d,"type":%q,"message":"temporary upstream error; please retry"}`, status, errType)
				data := []byte(`{"type":"error","error":` + inner + `}`)
				if event == "response.failed" {
					data = []byte(`{"type":"response.failed","response":{"error":` + inner + `}}`)
				}
				message := extractOpenAISSEErrorMessage(data)
				require.Equal(t, status, openAIStreamFailureStatus(data, message))
				require.False(t, openAIStreamFailedEventShouldFailover(data, message))
				require.False(t, openAIStreamErrorEventShouldFailover(data, message))
			}
		}
	}
}

func TestOpenAIWrappedUpstreamExplicitTypeKeepsItsPolicy(t *testing.T) {
	for _, tc := range []struct {
		errType string
		status  int
		retry   bool
	}{
		{"authentication_error", http.StatusUnauthorized, true},
		{"permission_error", http.StatusForbidden, false},
		{"invalid_request_error", http.StatusBadRequest, false},
		{"content_policy_error", http.StatusBadGateway, false},
		{"safety_error", http.StatusBadGateway, false},
	} {
		payload := []byte(fmt.Sprintf(`{"type":"error","error":{"code":"upstream_error","type":%q,"message":"temporary gateway failure"}}`, tc.errType))
		message := extractOpenAISSEErrorMessage(payload)
		require.Equal(t, tc.status, openAIStreamFailedEventSemanticStatus(payload, message))
		require.Equal(t, tc.retry, openAIStreamFailedEventShouldFailover(payload, message))
		require.Equal(t, tc.retry, openAIStreamErrorEventShouldFailover(payload, message))
	}
}

// An explicit generic bare server_error is not an untyped relay wrapper.
// Preserve the pre-existing visibility/no-replay contract used by Messages.
func TestOpenAIWrappedUpstreamBareServerTypeKeepsVisibleErrorPolicy(t *testing.T) {
	payload := []byte(`{"type":"error","error":{"type":"server_error","code":"upstream_error","message":"mixed tools failed"}}`)
	require.False(t, openAIStreamErrorEventShouldFailover(payload, "mixed tools failed"))
	require.True(t, openAIStreamFailedEventShouldFailover(payload, "mixed tools failed"), "response.failed retains its distinct pre-existing policy")
}

func TestOpenAIWrappedUpstreamBareServerErrorRemainsVisibleWithoutReplay(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	payload := "event: error\n" + `data: {"type":"error","error":{"type":"server_error","code":"upstream_error","message":"mixed tools failed"}}` + "\n\ndata: [DONE]\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	_, err := svc.ForwardAsAnthropic(context.Background(), c, rawChatCompletionsTestAccount(), body, "", "")
	require.Error(t, err)
	var retry *UpstreamFailoverError
	require.False(t, errors.As(err, &retry))
	require.Contains(t, rec.Body.String(), "mixed tools failed")
	require.Len(t, upstream.requests, 1)
}
