//go:build integration

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/upstreamsites"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSitesPreserveRestrictionsThroughAccountEdits(t *testing.T) {
	ctx := context.Background()
	cipher, err := NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("a", 64)}})
	require.NoError(t, err)
	manager := upstreamsites.New(integrationDB, cipher, func(context.Context, string, string, string) error { return nil })
	defer manager.Stop()
	client := testEntClient(t)
	accounts := newAccountRepositoryWithSQL(client, integrationDB, nil)
	account := mustCreateAccount(t, client, &service.Account{Name: "upstream site", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id=$1", account.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM upstream_site_records")
	})
	cfg := upstreamsites.DefaultConfig()
	cfg.BaseURL = "https://example.com"
	raw, err := json.Marshal(upstreamsites.SaveRequest{Config: cfg})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	manager.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/sites", bytes.NewReader(raw)))
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		Data upstreamsites.Site `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	id := response.Data.ID
	raw, err = json.Marshal(map[string]any{"bindings": map[string]int64{strconv.FormatInt(account.ID, 10): id}, "version": 0})
	require.NoError(t, err)
	w = httptest.NewRecorder()
	manager.Handler().ServeHTTP(w, httptest.NewRequest("PUT", "/bindings", bytes.NewReader(raw)))
	require.Equal(t, 200, w.Code, w.Body.String())
	_, err = integrationDB.ExecContext(ctx, `UPDATE upstream_site_records SET value=jsonb_set(value,'{site,state,manual}','true') WHERE collection='sites' AND key=$1`, strconv.FormatInt(id, 10))
	require.NoError(t, err)
	require.NoError(t, manager.RunTask(ctx, "monitor", time.Now()))
	current, err := accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.False(t, current.IsSchedulable())
	require.True(t, current.Schedulable)
	// 模拟旧表单覆盖 extra，站点限制必须从行锁内的最新值恢复。
	account.Extra = map[string]any{"note": "edited"}
	account.Schedulable = false
	require.NoError(t, accounts.Update(ctx, account))
	current, err = accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, current.IsUpstreamSiteBlocked())
	require.False(t, current.Schedulable)
	require.NoError(t, accounts.UpdateExtra(ctx, account.ID, map[string]any{service.AccountExtraUpstreamSiteRestriction: nil}))
	current, err = accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, current.IsUpstreamSiteBlocked())
	// 解绑清除站点原因，而人工关闭不变。
	raw = []byte(`{"bindings":{},"version":1}`)
	w = httptest.NewRecorder()
	manager.Handler().ServeHTTP(w, httptest.NewRequest("PUT", "/bindings", bytes.NewReader(raw)))
	require.Equal(t, 200, w.Code, w.Body.String())
	current, err = accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.False(t, current.IsUpstreamSiteBlocked())
	require.False(t, current.Schedulable)
	var n int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=$1 AND event_type='account_changed'`, account.ID).Scan(&n))
	require.GreaterOrEqual(t, n, 2)
}
