package service

import (
	"context"
	"errors"
	"strings"

	coderws "github.com/coder/websocket"
)

// CheckScheduledTestWSRestrictions 用最新限制复核每轮，保留连接实际使用的账号映射。
// MapRequestModel 在两种 ingress 中均先于上游写入；BeforeTurn 的相对顺序不同。
func (s *OpenAIGatewayService) CheckScheduledTestWSRestrictions(ctx context.Context, selected *Account, requestedModel, mappedModel string, firstMessage []byte) error {
	var latest *Account
	var err error
	// 优先读持久化状态，避免 scheduler outbox 尚未刷新时继续放行。
	switch {
	case s.accountRepo != nil:
		latest, err = s.accountRepo.GetByID(ctx, selected.ID)
	case s.schedulerSnapshot != nil:
		latest, err = s.schedulerSnapshot.GetAccount(ctx, selected.ID)
	default:
		err = errors.New("account state reader is unavailable")
	}
	if err != nil || latest == nil {
		return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "account state could not be refreshed, please reconnect", err)
	}

	// 与 ProxyResponsesWebSocketFromClient 保持一致：透传只做渠道映射，
	// 原生 WS 和 HTTP bridge 的规范化阶段还会应用连接快照中的账号映射。
	upstreamModel := normalizeOpenAIModelForUpstream(selected, selected.GetMappedModel(mappedModel))
	forceHTTPBridge := selected.Platform == PlatformGrok ||
		(s.pluginManager != nil && s.pluginManager.ShouldRouteOpenAIOAuth(selected))
	if s.cfg != nil && s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled && !forceHTTPBridge &&
		selected.ResolveOpenAIResponsesWebSocketV2Mode(s.cfg.Gateway.OpenAIWS.IngressModeDefault) == OpenAIWSIngressModePassthrough &&
		!s.shouldBridgeOpenAIWSPassthroughFirstMessage(selected, firstMessage) {
		upstreamModel = strings.TrimSpace(mappedModel)
	}
	if latest.IsScheduledTestAccountBlocked() ||
		latest.isScheduledTestModelBlocked(strings.TrimSpace(requestedModel), upstreamModel) ||
		latest.isScheduledTestModelBlocked(strings.TrimSpace(mappedModel), upstreamModel) {
		return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "account or model is no longer eligible for this connection, please reconnect", nil)
	}
	return nil
}
