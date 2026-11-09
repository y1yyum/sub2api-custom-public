//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBridgeReceiptCyberUsageSurvivesOverflowAndCancellation(t *testing.T) {
	for _, tokens := range []int{13, 0} {
		t.Run(fmt.Sprintf("input_%d", tokens), func(t *testing.T) {
			a, b := newBoundRequestProvider(t, "A", ""), newBoundRequestProvider(t, "B", "")
			h, _, receipts := newBoundRequestHandler(t, a, b)
			h.usageRecordWorkerPool = newFullBridgeReceiptDropPool(t)
			c, _ := newBoundRequestHandlerContext(`{"model":"gpt-5.4","input":"hello","stream":true}`, true)
			ctx := context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "cyber-overflow")
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			c.Request = c.Request.WithContext(ctx)
			key, ok := c.MustGet(string(middleware.ContextKeyAPIKey)).(*service.APIKey)
			require.True(t, ok)
			account := &service.Account{ID: 20201, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}
			service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{
				Message: "fixture rejection", UpstreamStatus: 400, UpstreamInTok: tokens,
			})
			h.recordCyberPolicyIfMarked(c, key, account, nil, "gpt-5.4", true, nil, service.ChannelUsageFields{}, "fixture-hash")
			require.Len(t, receipts.commands, 1, "managed cyber billing must use the mandatory overflow path")
			command := <-receipts.commands
			request, _ := custombridge.RequestFromContext(ctx)
			require.Equal(t, &request, command.BridgeRequest)
			require.Equal(t, "client:cyber-overflow", command.RequestID)
			require.Equal(t, tokens, command.InputTokens)
			var usage service.OpenAIUsage
			require.NoError(t, json.Unmarshal(command.BridgeUsage, &usage))
			require.Equal(t, tokens, usage.InputTokens)
			h.recordCyberPolicyIfMarked(c, key, account, nil, "gpt-5.4", true, nil, service.ChannelUsageFields{}, "fixture-hash")
			require.Empty(t, receipts.commands, "the same cyber mark must not bill twice")
		})
	}
}

func TestBridgeReceiptCyberResponsesRealHTTP(t *testing.T) {
	for _, failure := range []string{"cyber_failed", "cyber_zero"} {
		t.Run(failure, func(t *testing.T) {
			a, b := newBoundRequestProvider(t, "A", failure), newBoundRequestProvider(t, "B", "")
			h, _, receipts := newBoundRequestHandler(t, a, b)
			c, _ := newBoundRequestHandlerContext(`{"model":"gpt-5.4","input":"hello","stream":true}`, true)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "cyber-http"))
			h.Responses(c)
			h.usageRecordWorkerPool.Stop()
			require.Len(t, a.observed(), 1)
			require.Empty(t, b.observed())
			require.Len(t, receipts.commands, 1, "observed result and cyber fallback must have one billing owner")
			command := <-receipts.commands
			require.NotNil(t, command.BridgeRequest)
			require.Equal(t, "attempt-A", command.BridgeRequest.AttemptID)
			require.Equal(t, "client:cyber-http", command.RequestID)
			if failure == "cyber_zero" {
				require.Zero(t, command.BalanceCost)
			} else {
				require.Positive(t, command.BalanceCost)
			}
		})
	}
}
