//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const testIntelligencePromptImage = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4//8/AAX+Av4N70a4AAAAAElFTkSuQmCC"

func TestValidateIntelligencePromptImage(t *testing.T) {
	require.NoError(t, validateIntelligencePromptImage(testIntelligencePromptImage))
	var oversized bytes.Buffer
	require.NoError(t, png.Encode(&oversized, image.NewGray(image.Rect(0, 0, 2049, 1))))
	for name, dataURL := range map[string]string{
		"missing":              "",
		"remote":               "https://example.com/question.png",
		"jpeg":                 "data:image/jpeg;base64,eA==",
		"bad base64":           intelligenceImagePrefix + "!",
		"not PNG":              intelligenceImagePrefix + "eA==",
		"incomplete":           testIntelligencePromptImage[:len(testIntelligencePromptImage)-16],
		"oversized body":       intelligenceImagePrefix + strings.Repeat("A", 2<<20),
		"oversized dimensions": intelligenceImagePrefix + base64.StdEncoding.EncodeToString(oversized.Bytes()),
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, validateIntelligencePromptImage(dataURL), "prompt_image")
		})
	}
}

type imageQuestionRepo struct {
	IntelligenceQuestionRepository
	saved *IntelligenceQuestion
}

func (r *imageQuestionRepo) Create(_ context.Context, q *IntelligenceQuestion) (*IntelligenceQuestion, error) {
	r.saved = q
	return q, nil
}

func (r *imageQuestionRepo) Update(ctx context.Context, q *IntelligenceQuestion) (*IntelligenceQuestion, error) {
	return r.Create(ctx, q)
}

func TestQuestionSavesValidateAndKeepImage(t *testing.T) {
	repo := &imageQuestionRepo{}
	svc := NewScheduledTestService(nil, nil)
	svc.questionRepo = repo
	for _, save := range []func(context.Context, *IntelligenceQuestion) (*IntelligenceQuestion, error){svc.CreateQuestion, svc.UpdateQuestion} {
		q := &IntelligenceQuestion{Prompt: " question ", PromptImage: testIntelligencePromptImage, AnswerRule: IntelligenceRuleExact, ExpectedAnswers: []string{"answer"}}
		saved, err := save(context.Background(), q)
		require.NoError(t, err)
		require.Equal(t, "question", saved.Prompt)
		require.Equal(t, testIntelligencePromptImage, saved.PromptImage)
		repo.saved = nil
		q.PromptImage = ""
		_, err = save(context.Background(), q)
		require.Error(t, err)
		require.Nil(t, repo.saved, "invalid images must not change the bank")
	}
}

func TestQuestionImagePayloadsContainOnlyImageContent(t *testing.T) {
	const prompt = "private question text"
	options := AccountTestOptions{ImageDataURL: testIntelligencePromptImage}
	claude, err := createTestPayload("claude-test", prompt, options)
	require.NoError(t, err)
	for name, payload := range map[string]any{
		"anthropic": claude,
		"responses": createOpenAITestPayload("gpt-test", false, prompt, options),
		"chat":      createOpenAIChatCompletionsTestPayload("gpt-test", prompt, options),
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(payload)
			require.NoError(t, err)
			require.NotContains(t, string(raw), prompt)
			require.Contains(t, string(raw), strings.TrimPrefix(testIntelligencePromptImage, intelligenceImagePrefix))
		})
	}
	gemini := createGeminiTestPayload("gemini-test", prompt, options)
	require.NotContains(t, string(gemini), prompt)
	require.False(t, gjson.GetBytes(gemini, "contents.0.parts.0.text").Exists())
	require.Equal(t, "image/png", gjson.GetBytes(gemini, "contents.0.parts.0.inlineData.mimeType").String())
	c := newAccountTestPromptContext(prompt)
	c.Set(accountTestQuestionImageKey, testIntelligencePromptImage)
	grok, err := buildGrokTextTestBody(c, "grok-test")
	require.NoError(t, err)
	require.NotContains(t, string(grok), prompt)
	require.Equal(t, "input_image", gjson.GetBytes(grok, "input.0.content.0.type").String())
}

func TestRunnerMissingQuestionImageDoesNotSendText(t *testing.T) {
	svc, plans, results := newKindsTestService(&fakeQuestionRepo{enabled: []*IntelligenceQuestion{{ID: 1, Prompt: "private question"}}})
	probe, bodies := newClaudePromptFixture(t, "unused")
	runner := NewScheduledTestRunnerService(plans, svc, probe, nil, nil)
	runner.runOnePlan(context.Background(), &ScheduledTestPlan{ID: 5, AccountID: 41, Kind: ScheduledTestKindQuiz, CronExpression: "0 * * * *"})
	require.Empty(t, *bodies)
	require.Len(t, results.saved, 1)
	require.Equal(t, "failed", results.saved[0].Status)
	require.Empty(t, results.saved[0].Verdict)
	require.Contains(t, results.saved[0].ErrorMessage, "image is missing")
	require.Equal(t, []int64{5}, plans.afterRunPlan)
}

func TestScheduledQuizImageRequests(t *testing.T) {
	for _, tc := range []struct {
		name, platform, model, path, contentPath, stream string
		chat                                             bool
	}{
		{"anthropic", PlatformAnthropic, "claude-test", "/v1/messages", "messages.0.content.0.type", "data: {\"type\":\"message_stop\"}\n\n", false},
		{"responses", PlatformOpenAI, "gpt-5.4", "/v1/responses", "input.0.content.0.type", "data: {\"type\":\"response.completed\"}\n\n", false},
		{"chat fallback", PlatformOpenAI, "gpt-5.4", "/v1/chat/completions", "messages.0.content.0.type", "data: [DONE]\n\n", true},
		{"gemini", PlatformGemini, "gemini-2.5-flash", "/v1beta/models/gemini-2.5-flash:streamGenerateContent", "contents.0.parts.0.inlineData.mimeType", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"42\"}]}}]}\n\n", false},
		{"grok", PlatformGrok, "grok-4.3", "/v1/responses", "input.0.content.0.type", "data: {\"type\":\"response.completed\"}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 41, Platform: tc.platform, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"api_key": "sk-test", "base_url": "https://quiz.example", "model_mapping": map[string]any{"public-model": tc.model},
			}}
			if tc.chat {
				account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
			}
			var body []byte
			upstream := &systemOneHTTPUpstream{do: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, tc.path, req.URL.Path)
				var err error
				body, err = io.ReadAll(req.Body)
				require.NoError(t, err)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.stream))}, nil
			}}
			svc := &AccountTestService{accountRepo: &systemOnePolicyAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			result, err := svc.forScheduledQuiz().RunTestBackground(context.Background(), account.ID, "public-model", "private question text", testIntelligencePromptImage)
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Equal(t, "private question text", result.Prompt)
			require.Equal(t, tc.model, result.ModelID)
			require.NotContains(t, string(body), "private question text")
			require.Contains(t, string(body), strings.TrimPrefix(testIntelligencePromptImage, intelligenceImagePrefix))
			require.NotEmpty(t, gjson.GetBytes(body, tc.contentPath).String())
		})
	}
}
