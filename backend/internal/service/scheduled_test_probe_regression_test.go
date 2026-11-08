//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestScheduledQuizAntigravityImageAndUpstreamSnapshot(t *testing.T) {
	for _, model := range []string{"gemini-2.5-flash", "gemini-3.1-flash-image", "claude-sonnet-4-6"} {
		t.Run(model, func(t *testing.T) {
			account := &Account{ID: 41, Platform: PlatformAntigravity, Type: AccountTypeUpstream,
				Credentials: map[string]any{"api_key": "test-key", "project_id": "test-project", "model_mapping": map[string]any{"public-model": model}},
				Extra:       map[string]any{AccountExtraScheduledTestRestrictions: map[string]any{"1": map[string]any{"action": ScheduledTestFailureDisableModel, "model_id": "public-model", "upstream_model": model}}},
			}
			repo := &systemOnePolicyAccountRepo{account: account}
			var requestBody []byte
			upstream := &systemOneHTTPUpstream{do: func(req *http.Request) (*http.Response, error) {
				var err error
				requestBody, err = io.ReadAll(req.Body)
				require.NoError(t, err)
				stream := `data: {"response":{"candidates":[{"content":{"parts":[{"text":"4"}]}}]}}` + "\n\n"
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
			}}
			svc := &AccountTestService{accountRepo: repo, antigravityGatewayService: &AntigravityGatewayService{
				accountRepo: repo, tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream,
			}}
			result, err := svc.forScheduledQuiz().RunTestBackground(context.Background(), account.ID, "public-model", "2+2=?", testIntelligencePromptImage)
			require.NoError(t, err)
			require.Equal(t, "success", result.Status)
			require.Equal(t, model, result.ModelID)
			require.Equal(t, "4", result.ResponseText)
			require.Equal(t, model, gjson.GetBytes(requestBody, "model").String())
			require.NotContains(t, string(requestBody), "2+2=?")
			require.False(t, gjson.GetBytes(requestBody, "request.contents.0.parts.0.text").Exists())
			require.Equal(t, "image/png", gjson.GetBytes(requestBody, "request.contents.0.parts.0.inlineData.mimeType").String())
			require.GreaterOrEqual(t, gjson.GetBytes(requestBody, "request.generationConfig.maxOutputTokens").Int(), int64(1024))
		})
	}
}

func TestScheduledQuizAntigravityDoesNotPermanentlyDisableAccount(t *testing.T) {
	repo := &internal500AccountRepoStub{}
	gateway := &AntigravityGatewayService{accountRepo: repo}
	svc := &AccountTestService{accountRepo: repo, antigravityGatewayService: gateway}
	quiz := svc.forScheduledQuiz()
	account := &Account{ID: 41, Platform: PlatformAntigravity}
	quiz.antigravityGatewayService.applyInternal500Penalty(context.Background(), "quiz", account, 3)
	require.Empty(t, repo.setErrorCalls)
	gateway.applyInternal500Penalty(context.Background(), "probe", account, 3)
	require.Len(t, repo.setErrorCalls, 1)
}

func TestScheduledQuizOpenAIOAuthSnapshotsActualModel(t *testing.T) {
	ctx, recorder := newTestContext()
	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}` + "\n\n"))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{httpUpstream: upstream}
	account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"},
	}
	require.NoError(t, svc.testOpenAIAccountConnection(ctx, account, "gpt-6", "", ""))
	require.Equal(t, "gpt-6-astra", parseTestSSEStartModel(recorder.Body.String()))
	require.Len(t, upstream.requests, 1)
	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
}
