//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestWSRestrictions_RefreshEveryTurn(t *testing.T) {
	selected := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	repo := &gatewayProfitAccountRepo{account: selected}
	svc := &OpenAIGatewayService{accountRepo: repo}
	ctx := context.Background()
	require.NoError(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "public", "upstream", nil))

	latest := *selected
	latest.Extra = scheduledTestRestrictionExtra("disable_model", "other-alias", "upstream")
	repo.account = &latest
	err := svc.CheckScheduledTestWSRestrictions(ctx, selected, "public", "upstream", nil)
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusTryAgainLater, closeErr.StatusCode())
	require.Contains(t, closeErr.Reason(), "please reconnect")
	require.NoError(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "healthy", "healthy", nil))
	require.Nil(t, selected.Extra, "连接快照不能被最新状态修改")

	latest.Extra = scheduledTestRestrictionExtra("disable_account", "other", "other")
	require.Error(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "healthy", "healthy", nil))
	latest.Extra = nil
	require.NoError(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "public", "upstream", nil))
}

func TestScheduledTestWSRestrictions_ActualForwardedModel(t *testing.T) {
	for _, tc := range []struct {
		name, mode, requested, mapped, blockedID, blockedUpstream string
		wantBlocked                                               bool
	}{
		{"same model", OpenAIWSIngressModeCtxPool, "upstream", "upstream", "other", "upstream", true},
		{"public alias cannot allow blocked channel target", OpenAIWSIngressModeCtxPool, "public", "blocked-channel", "other", "upstream", true},
		{"public id remains blocked", OpenAIWSIngressModeCtxPool, "public", "healthy", "public", "old-upstream", true},
		{"channel id remains blocked", OpenAIWSIngressModeCtxPool, "public", "blocked-channel", "blocked-channel", "old-upstream", true},
		{"healthy model", OpenAIWSIngressModeCtxPool, "healthy", "healthy", "other", "upstream", false},
		{"passthrough uses channel target", OpenAIWSIngressModePassthrough, "public", "blocked-channel", "other", "blocked-channel", true},
		{"passthrough ignores unused account mapping", OpenAIWSIngressModePassthrough, "public", "blocked-channel", "other", "upstream", false},
		{"bridge applies account mapping", OpenAIWSIngressModeHTTPBridge, "public", "blocked-channel", "other", "upstream", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"model_mapping": map[string]any{"public": "healthy", "blocked-channel": "upstream"}},
				Extra:       map[string]any{"openai_apikey_responses_websockets_v2_mode": tc.mode},
			}
			latest := *selected
			latest.Extra = scheduledTestRestrictionExtra("disable_model", tc.blockedID, tc.blockedUpstream)
			// 更新后的映射不能掩盖连接仍在使用的旧 upstream。
			latest.Credentials = map[string]any{"model_mapping": map[string]any{"blocked-channel": "healthy"}}
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			svc := &OpenAIGatewayService{cfg: cfg, accountRepo: &gatewayProfitAccountRepo{account: &latest}}
			err := svc.CheckScheduledTestWSRestrictions(context.Background(), selected, tc.requested, tc.mapped, nil)
			if tc.wantBlocked {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestScheduledTestWSRestrictions_PassthroughBridge(t *testing.T) {
	selected := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"public": "upstream"}},
		Extra:       map[string]any{"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough},
	}
	latest := *selected
	latest.Extra = scheduledTestRestrictionExtra("disable_model", "another-alias", "upstream")
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	svc := &OpenAIGatewayService{cfg: cfg, accountRepo: &gatewayProfitAccountRepo{account: &latest}}
	ctx := context.Background()
	// 大首包进入 bridge 后会使用账号映射；带 previous_response_id 时保持透传。
	require.Error(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "public", "public", []byte(`{"type":"response.create","model":"public"}`)))
	require.NoError(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "public", "public", []byte(`{"type":"response.create","model":"public","previous_response_id":"resp_1"}`)))
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = false
	require.Error(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "public", "public", nil))
}

func TestScheduledTestWSRestrictions_CodexNormalization(t *testing.T) {
	selected := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"model_mapping": map[string]any{"public": "gpt-5.4-high"}},
	}
	latest := *selected
	latest.Extra = scheduledTestRestrictionExtra("disable_model", "another-alias", "gpt-5.4")
	svc := &OpenAIGatewayService{accountRepo: &gatewayProfitAccountRepo{account: &latest}}
	require.Error(t, svc.CheckScheduledTestWSRestrictions(context.Background(), selected, "public", "public", nil))
}

func TestScheduledTestWSRestrictions_StateSources(t *testing.T) {
	selected := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	blocked := *selected
	blocked.Extra = scheduledTestRestrictionExtra("disable_account", "", "")
	ctx := context.Background()
	t.Run("repository precedes stale scheduler snapshot", func(t *testing.T) {
		svc := &OpenAIGatewayService{accountRepo: &gatewayProfitAccountRepo{account: &blocked},
			schedulerSnapshot: &SchedulerSnapshotService{cache: &gatewayProfitSnapshotCache{account: selected}}}
		require.Error(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "model", "model", nil))
	})
	t.Run("snapshot reader observes replacement", func(t *testing.T) {
		cache := &gatewayProfitSnapshotCache{account: selected}
		svc := &OpenAIGatewayService{schedulerSnapshot: &SchedulerSnapshotService{cache: cache}}
		require.NoError(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "model", "model", nil))
		cache.account = &blocked
		require.Error(t, svc.CheckScheduledTestWSRestrictions(ctx, selected, "model", "model", nil))
	})
	readErr := errors.New("account read failed")
	for name, repo := range map[string]*gatewayProfitAccountRepo{
		"read failure":    {err: readErr},
		"missing account": {},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &OpenAIGatewayService{accountRepo: repo,
				schedulerSnapshot: &SchedulerSnapshotService{cache: &gatewayProfitSnapshotCache{account: selected}}}
			err := svc.CheckScheduledTestWSRestrictions(ctx, selected, "model", "model", nil)
			var closeErr *OpenAIWSClientCloseError
			require.ErrorAs(t, err, &closeErr)
			require.Equal(t, coderws.StatusTryAgainLater, closeErr.StatusCode())
			require.Contains(t, closeErr.Reason(), "please reconnect")
			if repo.err != nil {
				require.ErrorIs(t, err, readErr)
			}
		})
	}
}
