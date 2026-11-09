package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var ErrOpenAIBoundRequestRejected = errors.New("bound OpenAI request rejected")

func (s *defaultOpenAIAccountScheduler) selectBoundRequest(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	decision := OpenAIAccountScheduleDecision{Layer: "bound_request"}
	if s.service.cfg != nil && s.service.cfg.RunMode == config.RunModeSimple {
		return nil, decision, fmt.Errorf("%w: bound requests require standard mode", ErrNoAvailableAccounts)
	}
	if NormalizeOpenAICompatiblePlatform(req.Platform) != PlatformOpenAI || req.RequireCompact || req.RequiredImageCapability != "" ||
		(req.RequiredTransport != OpenAIUpstreamTransportAny && req.RequiredTransport != OpenAIUpstreamTransportHTTPSSE) {
		return nil, decision, fmt.Errorf("%w: bound requests require standard OpenAI Responses HTTP", ErrNoAvailableAccounts)
	}
	if s.service.checkChannelPricingRestriction(ctx, req.GroupID, req.RequestedModel) {
		return nil, decision, fmt.Errorf("%w: channel pricing restriction", ErrNoAvailableAccounts)
	}
	// 受信账号约束优先于所有亲和性来源；仍复用原候选过滤、抢槽和 DB 终检。
	req.PreviousResponseID = ""
	req.SessionHash = ""
	req.StickyAccountID = 0
	req.StickyPreviousAccountID = 0
	req.GuardianParentAccountID = 0
	req.StickyWeighted = false
	req.PreserveStickyBinding = true
	req.RequiredCapability = OpenAIEndpointCapabilityResponses
	selection, count, topK, skew, err := s.selectByLoadBalance(ctx, req)
	decision.CandidateCount, decision.TopK, decision.LoadSkew = count, topK, skew
	if selection != nil && selection.Account != nil {
		decision.SelectedAccountID = selection.Account.ID
		decision.SelectedAccountType = selection.Account.Type
	}
	return selection, decision, err
}

func (s *OpenAIGatewayService) validateBoundOpenAIForward(ctx context.Context, c *gin.Context, account *Account, model string) error {
	bound, ok := custombridge.RequestFromContext(ctx)
	if !ok {
		return nil
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return fmt.Errorf("%w: bound requests require standard mode", ErrOpenAIBoundRequestRejected)
	}
	if account == nil || account.ID != bound.AccountID {
		return fmt.Errorf("%w: selected account differs from trusted account", ErrOpenAIBoundRequestRejected)
	}
	v, _ := c.Get("api_key")
	apiKey, ok := v.(*APIKey)
	if !ok || apiKey == nil || s.accountRepo == nil {
		return fmt.Errorf("%w: missing authenticated scope or account repository", ErrOpenAIBoundRequestRejected)
	}
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, apiKey.GroupID)
	ctx = s.withOpenAIProfitControlGate(ctx, apiKey.GroupID)
	latest := s.recheckSelectedOpenAIAccountFromDB(ctx, account, apiKey.GroupID, PlatformOpenAI, model, false, OpenAIEndpointCapabilityResponses)
	if latest == nil || latest.ID != bound.AccountID || latest.Concurrency != account.Concurrency {
		return fmt.Errorf("%w: account no longer eligible for its acquired slot", ErrOpenAIBoundRequestRejected)
	}
	if isOpenAIResponsesCompactPath(c) || GetOpenAIClientTransport(c) != OpenAIClientTransportHTTP {
		return fmt.Errorf("%w: only standard OpenAI Responses HTTP is supported", ErrOpenAIBoundRequestRejected)
	}
	for _, candidate := range []*Account{account, latest} {
		if isOpenAIImageGenerationModel(model) || isOpenAIImageGenerationModel(candidate.GetMappedModel(model)) {
			return fmt.Errorf("%w: image generation is not a managed text capability", ErrOpenAIBoundRequestRejected)
		}
		if !candidate.IsOpenAIApiKey() || candidate.IsCredentialShadow() || candidate.IsOpenAIPassthroughEnabled() ||
			s.resolveUpstreamProtocolFor(ctx, candidate, APIProtocolResponses, model) != APIProtocolResponses {
			return fmt.Errorf("%w: only standard OpenAI Responses HTTP is supported", ErrOpenAIBoundRequestRejected)
		}
	}
	scheduler := &defaultOpenAIAccountScheduler{service: s}
	compatible, reason := scheduler.isAccountRequestCompatibleReason(ctx, latest, OpenAIAccountScheduleRequest{
		GroupID: apiKey.GroupID, Platform: PlatformOpenAI, RequestedModel: model,
		RequiredCapability: OpenAIEndpointCapabilityResponses,
		RequirePrivacySet:  s.openAIGroupRequiresPrivacySet(ctx, apiKey.GroupID),
	})
	if !compatible {
		return fmt.Errorf("%w: %s", ErrOpenAIBoundRequestRejected, reason)
	}
	if s.checkChannelPricingRestriction(ctx, apiKey.GroupID, model) {
		return fmt.Errorf("%w: channel pricing restriction", ErrOpenAIBoundRequestRejected)
	}
	return nil
}

func validateBoundOpenAIResolvedBody(ctx context.Context, body []byte) error {
	if _, bound := custombridge.RequestFromContext(ctx); !bound {
		return nil
	}
	model := gjson.GetBytes(body, "model").String()
	if IsImageGenerationIntent(openAIResponsesEndpoint, model, body) || gjson.GetBytes(body, "tools").Exists() || gjson.GetBytes(body, "tool_choice").Exists() {
		return fmt.Errorf("%w: resolved tools and image generation are not supported", ErrOpenAIBoundRequestRejected)
	}
	return nil
}

func boundOpenAIExecutionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	detached, release := detachUpstreamContext(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		limited, cancel := context.WithDeadline(detached, deadline)
		return limited, func() { cancel(); release() }
	}
	return detached, release
}
