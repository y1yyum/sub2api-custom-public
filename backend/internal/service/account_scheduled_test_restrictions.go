package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

const AccountExtraScheduledTestRestrictions = "scheduled_test_restrictions"

// IsScheduledTestAccountBlocked 判断是否有计划隔离整个账号。
func (a *Account) IsScheduledTestAccountBlocked() bool {
	if a == nil {
		return false
	}
	restrictions, _ := a.Extra[AccountExtraScheduledTestRestrictions].(map[string]any)
	for _, value := range restrictions {
		restriction, _ := value.(map[string]any)
		if restriction["action"] == "disable_account" {
			return true
		}
	}
	return false
}

// IsScheduledTestModelBlocked 匹配公开 ID 及指向同一上游的别名。
// 隔离不改变映射，定时测试仍可复测被隔离的模型。
func (a *Account) IsScheduledTestModelBlocked(requestedModel string) bool {
	return a.isScheduledTestRequestBlocked(requestedModel, false)
}

func (a *Account) isScheduledTestRequestBlocked(requestedModel string, requireCompact bool) bool {
	if a == nil {
		return false
	}
	restrictions, _ := a.Extra[AccountExtraScheduledTestRestrictions].(map[string]any)
	if len(restrictions) == 0 {
		return false
	}
	requestedModel = strings.TrimSpace(requestedModel)
	var upstreamModel string
	switch {
	case a.IsOpenAI():
		upstreamModel = resolveOpenAIAccountUpstreamModelForRequest(a, requestedModel, requireCompact)
	case a.IsBedrock():
		upstreamModel, _ = ResolveBedrockModelID(a, requestedModel)
	case a.Platform == PlatformAntigravity:
		upstreamModel = a.GetMappedModel(strings.TrimPrefix(requestedModel, "models/"))
	case a.Platform == PlatformAnthropic && a.Type == AccountTypeServiceAccount:
		var matched bool
		upstreamModel, matched = a.ResolveMappedModel(requestedModel)
		if !matched {
			upstreamModel = normalizeVertexAnthropicModelID(claude.NormalizeModelID(requestedModel))
		}
	case a.Platform == PlatformAnthropic && a.Type != AccountTypeAPIKey:
		upstreamModel = claude.NormalizeModelID(requestedModel)
	default:
		upstreamModel = a.GetMappedModel(requestedModel)
	}
	return a.isScheduledTestModelBlocked(requestedModel, upstreamModel)
}

func (a *Account) isScheduledTestModelBlocked(requestedModel, upstreamModel string) bool {
	restrictions, _ := a.Extra[AccountExtraScheduledTestRestrictions].(map[string]any)
	for _, value := range restrictions {
		restriction, _ := value.(map[string]any)
		switch restriction["action"] {
		case "disable_account":
			return true
		case "disable_model":
			modelID, _ := restriction["model_id"].(string)
			blockedUpstream, _ := restriction["upstream_model"].(string)
			if (modelID != "" && modelID == requestedModel) ||
				(blockedUpstream != "" && blockedUpstream == upstreamModel) {
				return true
			}
		}
	}
	return false
}
