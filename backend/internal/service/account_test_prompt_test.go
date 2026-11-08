//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newAccountTestPromptContext(prompt string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if prompt != "" {
		c.Set(accountTestPromptKey, prompt)
	}
	return c
}

func TestAccountTestTextPromptFallsBackToHi(t *testing.T) {
	require.Equal(t, "hi", accountTestTextPrompt(newAccountTestPromptContext("")))
	require.Equal(t, "hi", accountTestTextPrompt(newAccountTestPromptContext("   ")))
	require.Equal(t, "draw it", accountTestTextPrompt(newAccountTestPromptContext("  draw it ")))
}

func TestBedrockTestMaxTokensDependsOnPrompt(t *testing.T) {
	require.Equal(t, 256, bedrockTestMaxTokens(newAccountTestPromptContext("")))
	require.Equal(t, 1024, bedrockTestMaxTokens(newAccountTestPromptContext("draw it")))
}

func TestTextTestPayloadsCarryPrompt(t *testing.T) {
	claudePayload, err := createTestPayload("claude-x", "draw it")
	require.NoError(t, err)
	claudeContent := claudePayload["messages"].([]map[string]any)[0]["content"].([]map[string]any)
	require.Equal(t, "draw it", claudeContent[0]["text"])

	openAIPayload := createOpenAITestPayload("gpt-x", false, "draw it")
	openAIContent := openAIPayload["input"].([]map[string]any)[0]["content"].([]map[string]any)
	require.Equal(t, "draw it", openAIContent[0]["text"])
}

func TestBuildGrokTextTestBody(t *testing.T) {
	defaultBody, err := buildGrokTextTestBody(newAccountTestPromptContext(""), "grok-x")
	require.NoError(t, err)
	probeBody, err := buildGrokQuotaProbeBody("grok-x")
	require.NoError(t, err)
	require.JSONEq(t, string(probeBody), string(defaultBody))

	customBody, err := buildGrokTextTestBody(newAccountTestPromptContext("draw it"), "grok-x")
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(customBody, &payload))
	require.Equal(t, "draw it", payload["input"])
	require.Equal(t, "grok-x", payload["model"])
	require.Equal(t, true, payload["stream"])
}

func TestParseTestSSEStartModel(t *testing.T) {
	body := "data: {\"type\":\"content\",\"text\":\"x\"}\n\n" +
		"data: not-json\n\n" +
		"data: {\"type\":\"test_start\",\"model\":\"upstream-a\"}\n\n" +
		"data: {\"type\":\"test_start\",\"model\":\"upstream-b\"}\n\n"
	require.Equal(t, "upstream-a", parseTestSSEStartModel(body))
	require.Empty(t, parseTestSSEStartModel("data: {\"type\":\"content\"}\n"))
}

func newClaudePromptFixture(t *testing.T, reply string) (*AccountTestService, *[]map[string]any) {
	t.Helper()
	account := &Account{
		ID:       41,
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"model_mapping": map[string]any{"public-model": "upstream-model"},
		},
	}
	var bodies []map[string]any
	stream := "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":" + jsonStringLiteral(reply) + "}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	upstream := &systemOneHTTPUpstream{do: func(req *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		bodies = append(bodies, body)
		header := make(http.Header)
		header.Set("Content-Type", "text/event-stream")
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(stream))}, nil
	}}
	svc := &AccountTestService{
		accountRepo:  &systemOnePolicyAccountRepo{account: account},
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	return svc, &bodies
}

func jsonStringLiteral(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func claudeRequestText(t *testing.T, body map[string]any) string {
	t.Helper()
	messages := body["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

func TestRunTestBackgroundSendsPromptAndSnapshotsUpstreamModel(t *testing.T) {
	svc, bodies := newClaudePromptFixture(t, "#  #\n####")

	result, err := svc.RunTestBackground(context.Background(), 41, "public-model", "  draw it  ")
	require.NoError(t, err)

	require.Len(t, *bodies, 1)
	require.Equal(t, "upstream-model", (*bodies)[0]["model"])
	require.Equal(t, "draw it", claudeRequestText(t, (*bodies)[0]))
	require.Equal(t, "success", result.Status)
	require.Equal(t, "upstream-model", result.ModelID)
	require.Equal(t, "draw it", result.Prompt)
	require.Equal(t, "#  #\n####", result.ResponseText)
}

func TestRunTestBackgroundWithoutPromptKeepsHi(t *testing.T) {
	svc, bodies := newClaudePromptFixture(t, "hello")

	result, err := svc.RunTestBackground(context.Background(), 41, "public-model", "")
	require.NoError(t, err)

	require.Equal(t, "hi", claudeRequestText(t, (*bodies)[0]))
	require.Empty(t, result.Prompt)
}

func TestRunTestBackgroundTruncatesLongReplies(t *testing.T) {
	svc, _ := newClaudePromptFixture(t, strings.Repeat("帅", scheduledTestResponseMaxBytes))

	result, err := svc.RunTestBackground(context.Background(), 41, "public-model", "draw it")
	require.NoError(t, err)

	require.LessOrEqual(t, len(result.ResponseText), scheduledTestResponseMaxBytes)
	require.True(t, strings.HasPrefix(strings.Repeat("帅", scheduledTestResponseMaxBytes), result.ResponseText))
	require.NotEmpty(t, result.ResponseText)
}
