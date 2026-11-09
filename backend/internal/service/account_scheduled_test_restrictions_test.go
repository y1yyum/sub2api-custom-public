package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func scheduledTestRestrictionExtra(action, model, upstream string) map[string]any {
	return map[string]any{AccountExtraScheduledTestRestrictions: map[string]any{
		"plan-1": map[string]any{"action": action, "model_id": model, "upstream_model": upstream},
	}}
}

func TestScheduledTestRestrictions_ModelAliases(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"public": "upstream", "alias": "upstream", "alias-*": "upstream",
			"other": "healthy", "upstream": "healthy", "public-prefix": "healthy",
		}},
		Extra: scheduledTestRestrictionExtra("disable_model", "public", "upstream"),
	}
	for _, model := range []string{"public", "alias", "alias-v2"} {
		require.True(t, account.IsScheduledTestModelBlocked(model), model)
		require.False(t, account.IsModelSupported(model), model)
	}
	for _, model := range []string{"other", "upstream", "public-prefix"} {
		require.False(t, account.IsScheduledTestModelBlocked(model), model)
		require.True(t, account.IsModelSupported(model), model)
	}
	require.True(t, account.IsSchedulable(), "模型隔离不应暂停整个账号")
	require.False(t, account.IsScheduledTestAccountBlocked())
	require.Equal(t, "upstream", account.GetMappedModel("public"), "复测仍需原始映射")
	mapping, ok := account.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping["public"] = "healthy"
	require.True(t, account.IsScheduledTestModelBlocked("public"), "计划记录的公开 ID 继续隔离")
}

func TestScheduledTestRestrictions_MultiplePlansAndJSON(t *testing.T) {
	account := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Extra: scheduledTestRestrictionExtra("disable_model", "model-a", "model-a")}
	restrictions, ok := account.Extra[AccountExtraScheduledTestRestrictions].(map[string]any)
	require.True(t, ok)
	restrictions["plan-2"] = map[string]any{"action": "disable_model", "model_id": "model-b", "upstream_model": "model-b"}
	restrictions["plan-3"] = map[string]any{"action": "disable_account", "model_id": "model-c", "upstream_model": "model-c"}
	restrictions["plan-4"] = map[string]any{"action": "disable_account", "model_id": "model-d", "upstream_model": "model-d"}
	data, err := json.Marshal(account)
	require.NoError(t, err)
	var restored Account
	require.NoError(t, json.Unmarshal(data, &restored))
	require.True(t, restored.IsScheduledTestAccountBlocked())
	require.False(t, restored.IsSchedulable())
	require.False(t, restored.IsModelSupported("unrelated"))
	restrictions, ok = restored.Extra[AccountExtraScheduledTestRestrictions].(map[string]any)
	require.True(t, ok)
	delete(restrictions, "plan-3")
	require.False(t, restored.IsSchedulable(), "其他计划的账号隔离仍生效")
	delete(restrictions, "plan-4")
	require.True(t, restored.IsSchedulable())
	require.True(t, restored.IsScheduledTestModelBlocked("model-a"))
	require.True(t, restored.IsScheduledTestModelBlocked("model-b"))
	require.False(t, restored.IsScheduledTestModelBlocked("model-c"))
	delete(restrictions, "plan-1")
	require.False(t, restored.IsScheduledTestModelBlocked("model-a"))
	require.True(t, restored.IsScheduledTestModelBlocked("model-b"))
}

func TestScheduledTestRestrictions_InvalidExtra(t *testing.T) {
	for _, value := range []any{nil, "invalid", map[string]any{
		"missing": nil, "invalid": "invalid", "empty": map[string]any{},
		"unknown":     map[string]any{"action": "unknown", "model_id": "model-a"},
		"empty-model": map[string]any{"action": "disable_model"},
	}} {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			Extra: map[string]any{AccountExtraScheduledTestRestrictions: value}}
		require.False(t, account.IsScheduledTestAccountBlocked())
		require.False(t, account.IsScheduledTestModelBlocked("model-a"))
		require.False(t, account.IsScheduledTestModelBlocked(""))
		require.True(t, account.IsSchedulable())
	}
}

func TestScheduledTestRestrictions_GatewayPlatformBranches(t *testing.T) {
	for _, tc := range []struct {
		name, platform, accountType, model, alias, upstream, healthy string
		credentials                                                  map[string]any
	}{
		{"bedrock", PlatformAnthropic, AccountTypeBedrock, "claude-sonnet-4-5", "claude-sonnet-4-5-20250929", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-opus-4-6", map[string]any{"aws_region": "us-east-1"}},
		{"antigravity", PlatformAntigravity, AccountTypeOAuth, "public", "alias", "claude-sonnet-4-5", "claude-opus-4-6-thinking", map[string]any{"model_mapping": map[string]any{"public": "claude-sonnet-4-5", "alias": "claude-sonnet-4-5", "claude-opus-4-6-thinking": "claude-opus-4-6-thinking"}}},
		{"anthropic-oauth", PlatformAnthropic, AccountTypeOAuth, "claude-sonnet-4-5", "claude-sonnet-4-5-20250929", "claude-sonnet-4-5-20250929", "claude-opus-4-6", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{Platform: tc.platform, Type: tc.accountType, Credentials: tc.credentials}
			gateway := &GatewayService{}
			require.True(t, gateway.isModelSupportedByAccount(account, tc.model))
			require.True(t, gateway.isModelSupportedByAccount(account, tc.alias))
			require.True(t, gateway.isModelSupportedByAccount(account, tc.healthy))
			account.Extra = scheduledTestRestrictionExtra("disable_model", tc.model, tc.upstream)
			for _, model := range []string{tc.model, tc.alias} {
				require.True(t, account.IsScheduledTestModelBlocked(model), model)
				require.False(t, gateway.isModelSupportedByAccount(account, model), model)
				require.False(t, gateway.isModelSupportedByAccountWithContext(context.Background(), account, model), model)
			}
			require.True(t, gateway.isModelSupportedByAccountWithContext(context.Background(), account, tc.healthy))
			account.Extra = scheduledTestRestrictionExtra("disable_account", tc.model, tc.upstream)
			require.False(t, gateway.isModelSupportedByAccount(account, tc.healthy))
			require.False(t, gateway.isModelSupportedByAccountWithContext(context.Background(), account, tc.healthy))
		})
	}
}

func TestScheduledTestRestrictions_PassthroughAndCompact(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping":         map[string]any{"alias": "isolated"},
			"compact_model_mapping": map[string]any{"alias": "isolated", "healthy": "compact-healthy"},
		},
		Extra: scheduledTestRestrictionExtra("disable_model", "public", "isolated"),
	}
	account.Extra["openai_passthrough"] = true
	gateway := &GatewayService{}
	for _, model := range []string{"public", "isolated"} {
		require.False(t, supportsOpenAIRequestModel(account, model, false))
		require.False(t, gateway.isModelSupportedByAccount(account, model))
	}
	require.True(t, supportsOpenAIRequestModel(account, "alias", false), "透传忽略残留普通映射")
	require.True(t, gateway.isModelSupportedByAccount(account, "alias"))
	require.False(t, supportsOpenAIRequestModel(account, "alias", true), "compact 目标仍需拦截")
	require.True(t, supportsOpenAIRequestModel(account, "healthy", true))
	delete(account.Extra, "openai_passthrough")
	compactMapping, ok := account.Credentials["compact_model_mapping"].(map[string]any)
	require.True(t, ok)
	compactMapping["alias"] = "compact-healthy"
	require.False(t, supportsOpenAIRequestModel(account, "alias", false))
	require.True(t, supportsOpenAIRequestModel(account, "alias", true), "普通上游被隔离不应误伤不同 compact 目标")
	require.Equal(t, "isolated", account.GetMappedModel("alias"))
	require.Equal(t, "compact-healthy", resolveOpenAIAccountUpstreamModelForRequest(account, "alias", true))
}

func TestScheduledTestRestrictions_AntigravityThinkingAndProbe(t *testing.T) {
	account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Credentials: map[string]any{"model_mapping": map[string]any{"alias": "claude-sonnet-4-5", "claude-sonnet-4-5": "claude-sonnet-4-5", "claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking"}},
		Extra:       scheduledTestRestrictionExtra("disable_model", "public-thinking", "claude-sonnet-4-5-thinking"),
	}
	gateway := &GatewayService{}
	require.True(t, gateway.isModelSupportedByAccountWithContext(context.Background(), account, "alias"))
	require.False(t, gateway.isModelSupportedByAccountWithContext(WithThinkingEnabled(context.Background(), true, false), account, "alias"))
	require.Equal(t, "claude-sonnet-4-5-thinking", mapAntigravityModel(account, "claude-sonnet-4-5-thinking"), "隔离不应阻止复测解析 identity 映射")
	account.Extra = scheduledTestRestrictionExtra("disable_model", "public-normal", "claude-sonnet-4-5")
	require.False(t, gateway.isModelSupportedByAccountWithContext(context.Background(), account, "alias"))
	require.True(t, gateway.isModelSupportedByAccountWithContext(WithThinkingEnabled(context.Background(), true, false), account, "alias"))
	mapping, ok := account.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping["claude-sonnet-4-5-thinking"] = "different-upstream"
	account.Extra = scheduledTestRestrictionExtra("disable_model", "claude-sonnet-4-5-thinking", "different-upstream")
	require.True(t, gateway.isModelSupportedByAccountWithContext(WithThinkingEnabled(context.Background(), true, false), account, "alias"), "已解析的 thinking 目标不能再按公开别名二次匹配")
}

func TestScheduledTestRestrictions_TokenCountAndSessionSticky(t *testing.T) {
	for _, action := range []string{"disable_model", "disable_account"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			groupID := int64(23)
			blocked := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
				Extra: scheduledTestRestrictionExtra(action, "gpt-5.1", "gpt-5.1")}
			healthy := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 10, GroupIDs: []int64{groupID}}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"sticky": blocked.ID}}
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{blocked, healthy}}, cache: cache, cfg: &config.Config{}}
			account, err := svc.SelectAccountForTokenCount(ctx, &groupID, "sticky", "gpt-5.1", OpenAIEndpointCapabilityChatCompletions, PlatformOpenAI)
			require.NoError(t, err)
			require.Equal(t, healthy.ID, account.ID, "token 计数与粘性绑定都不得绕过隔离")
		})
	}
}

func TestScheduledTestRestrictions_WebSocketPreviousResponse(t *testing.T) {
	for _, action := range []string{"disable_model", "disable_account"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			groupID := int64(23)
			account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 1,
				Extra: scheduledTestRestrictionExtra(action, "gpt-5.1", "gpt-5.1")}
			account.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
			cache := &stubGatewayCache{}
			store := NewOpenAIWSStateStore(cache)
			svc := &OpenAIGatewayService{accountRepo: stubOpenAIAccountRepo{accounts: []Account{account}}, cache: cache,
				cfg: newOpenAIWSV2TestConfig(), concurrencyService: NewConcurrencyService(stubConcurrencyCache{}), openaiWSStateStore: store}
			require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_isolated", account.ID, time.Hour))
			restrictions := account.Extra[AccountExtraScheduledTestRestrictions]
			delete(account.Extra, AccountExtraScheduledTestRestrictions)
			baseline, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_isolated", "gpt-5.1", nil, false)
			require.NoError(t, err)
			require.NotNil(t, baseline, "未隔离时已有 response 绑定可复用")
			if baseline.ReleaseFunc != nil {
				baseline.ReleaseFunc()
			}
			account.Extra[AccountExtraScheduledTestRestrictions] = restrictions
			selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_isolated", "gpt-5.1", nil, false)
			require.NoError(t, err)
			require.Nil(t, selection, "已有 response 绑定不得复用被隔离账号或模型")
		})
	}
}
