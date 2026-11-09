package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestBridgeReceiptCaptureCoreRequestIDUsesExistingPriority(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "local-id")
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "client-id")
	require.Equal(t, "client:client-id", CaptureBridgeCoreRequestID(ctx, "upstream-id"))
	require.Equal(t, "grok-video:task", CaptureBridgeCoreRequestID(ctx, "grok-video:task"))
	require.Equal(t, "upstream-id", CaptureBridgeCoreRequestID(context.Background(), "upstream-id"))
	require.NotEmpty(t, CaptureBridgeCoreRequestID(context.Background(), ""))
}

func TestBridgeReceiptCapturePreservesPinnedCoreRequestID(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-id")
	ctx = custombridge.WithCoreRequestID(ctx, "client:registered-id")
	for _, upstreamID := range []string{"provider-id", "web_search:provider-id", "grok-video:provider-id", "grok_audio:provider-id", "grok_realtime:provider-id", ""} {
		t.Run(upstreamID, func(t *testing.T) {
			require.Equal(t, "client:registered-id", CaptureBridgeCoreRequestID(ctx, upstreamID))
		})
	}
	ctx = custombridge.WithCoreRequestID(context.Background(), "generated:registered-id")
	require.Equal(t, "generated:registered-id", CaptureBridgeCoreRequestID(ctx, "web_search:provider-id"))
	require.Equal(t, "web_search:provider-id", resolveUsageBillingRequestID(ctx, "web_search:provider-id"), "ordinary forced money events retain their existing priority")
}

func TestOpenAIRecordUsageBridgeReceiptExplicitSnapshot(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			logs := &openAIRecordUsageLogRepoStub{}
			repo := &openAIRecordUsageBillingRepoStub{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, repo,
				&openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			request := custombridge.Request{ReservationID: "reservation", AttemptID: "attempt", AccountID: 3}
			raw := OpenAIUsage{InputTokens: 1200, OutputTokens: 30, CacheReadInputTokens: 200, CacheCreationInputTokens: 100}
			input := &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "upstream-response", Usage: raw, Model: "gpt-5.1", Stream: stream},
				APIKey: &APIKey{ID: 2}, User: &User{ID: 1}, Account: &Account{ID: 3, Type: AccountTypeAPIKey},
				BridgeRequest: &request, BridgeCoreRequestID: "core-request", RequestPayloadHash: "payload-hash",
			}
			// worker 使用全新的 context；metadata 只从显式输入读取。
			require.NoError(t, svc.RecordUsage(context.Background(), input))
			require.Equal(t, 1, repo.calls)
			require.Equal(t, request, *repo.lastCmd.BridgeRequest)
			require.NotSame(t, &request, repo.lastCmd.BridgeRequest)
			require.Equal(t, "core-request", repo.lastCmd.RequestID)
			require.Equal(t, "core-request", logs.lastLog.RequestID)
			require.Equal(t, 900, repo.lastCmd.InputTokens)
			var persisted OpenAIUsage
			require.NoError(t, json.Unmarshal(repo.lastCmd.BridgeUsage, &persisted))
			require.Equal(t, raw, persisted)
			request.AttemptID = "changed-after-billing"
			require.Equal(t, "attempt", repo.lastCmd.BridgeRequest.AttemptID)
		})
	}
}

func TestOpenAIRecordUsageBridgeReceiptDefaultsOffEvenWithContext(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{}
	repo := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, repo,
		&openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	ctx := custombridge.WithRequest(context.Background(), custombridge.Request{ReservationID: "r", AttemptID: "a", AccountID: 3})
	require.NoError(t, svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "ordinary", Model: "gpt-5.1"},
		APIKey: &APIKey{ID: 2}, User: &User{ID: 1}, Account: &Account{ID: 3},
	}))
	require.Nil(t, repo.lastCmd.BridgeRequest)
	require.Empty(t, repo.lastCmd.BridgeUsage)
	require.Equal(t, "ordinary", repo.lastCmd.RequestID)
}

func TestOpenAIRecordUsageBridgeReceiptRejectsMissingIdentityAndWrongAccount(t *testing.T) {
	for _, tc := range []struct {
		name      string
		accountID int64
		requestID string
		want      error
	}{
		{name: "wrong_account", accountID: 4, requestID: "core", want: custombridge.ErrReceiptAccountMismatch},
		{name: "missing_core_id", accountID: 3, want: ErrUsageBillingRequestIDRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &openAIRecordUsageBillingRepoStub{}
			svc := &OpenAIGatewayService{usageBillingRepo: repo}
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{}, Account: &Account{ID: tc.accountID},
				BridgeRequest: &custombridge.Request{AccountID: 3}, BridgeCoreRequestID: tc.requestID,
			})
			require.ErrorIs(t, err, tc.want)
			require.Zero(t, repo.calls)
		})
	}
}

func TestApplyUsageBillingBridgeReceiptRejectsLegacyFallback(t *testing.T) {
	for _, missing := range []string{"repository", "request_id", "api_key", "dependencies"} {
		t.Run(missing, func(t *testing.T) {
			p := &postUsageBillingParams{
				Cost: &CostBreakdown{ActualCost: 1}, User: &User{ID: 1},
				APIKey: &APIKey{ID: 2}, Account: &Account{ID: 3},
				BridgeRequest: &custombridge.Request{ReservationID: "r", AttemptID: "a", AccountID: 3},
			}
			var repo UsageBillingRepository = &openAIRecordUsageBillingRepoStub{}
			deps := &billingDeps{}
			requestID := "core"
			switch missing {
			case "repository":
				repo = nil
			case "request_id":
				requestID = ""
			case "api_key":
				p.APIKey = nil
			case "dependencies":
				deps = nil
			}
			applied, err := applyUsageBilling(context.Background(), requestID, nil, p, deps, repo)
			require.ErrorIs(t, err, custombridge.ErrReceiptBillingUnavailable)
			require.False(t, applied)
		})
	}
}

func TestOpenAIRecordUsageBridgeReceiptSimpleModeDoesNotSilentlySkip(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{RunMode: config.RunModeSimple}}
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{}, Account: &Account{ID: 3},
		BridgeRequest: &custombridge.Request{AccountID: 3}, BridgeCoreRequestID: "core",
	})
	require.ErrorIs(t, err, custombridge.ErrReceiptBillingUnavailable)
}

func TestUsageBillingBridgeReceiptPreservesFingerprint(t *testing.T) {
	ordinary := &UsageBillingCommand{RequestID: "core", APIKeyID: 2, UserID: 1, AccountID: 3, BalanceCost: 0.000078125}
	managed := *ordinary
	managed.BridgeRequest = &custombridge.Request{ReservationID: "r", AttemptID: "a", AccountID: 3}
	managed.BridgeUsage = json.RawMessage(`{"input_tokens":1200,"output_tokens":30}`)
	ordinary.Normalize()
	managed.Normalize()
	require.Equal(t, ordinary.RequestFingerprint, managed.RequestFingerprint)
	require.Equal(t, ordinary.BalanceCost, managed.BalanceCost)
}
