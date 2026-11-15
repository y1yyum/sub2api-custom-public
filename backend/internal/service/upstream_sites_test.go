package service

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestUpstreamSitesAuditOmitsAllCredentials(t *testing.T) {
	body := []byte(`{"config":{"name":"example"},"secrets":{"key":"wallet-value","wallet_token":"account-value","proxy_url":"http://name:proxy-value@example.com","probe_keys":{"health":"probe-value"}}}`)
	redacted := RedactAuditBody(body, "application/json")
	for _, secret := range []string{"wallet-value", "account-value", "proxy-value", "probe-value"} {
		require.False(t, strings.Contains(redacted, secret))
	}
	require.Contains(t, redacted, "example")
}

func TestUpstreamSiteRestrictionPreservesManualState(t *testing.T) {
	a := &Account{Status: StatusActive, Schedulable: true, Extra: map[string]any{AccountExtraUpstreamSiteRestriction: map[string]any{"site_id": float64(1)}}}
	require.False(t, a.IsSchedulable())
	require.True(t, a.Schedulable)
	a.Schedulable = false
	delete(a.Extra, AccountExtraUpstreamSiteRestriction)
	require.False(t, a.IsSchedulable())
	a.Schedulable = true
	require.True(t, a.IsSchedulable())
	a.Extra[AccountExtraScheduledTestRestrictions] = map[string]any{"1": map[string]any{"action": "disable_account"}}
	require.False(t, a.IsSchedulable())
}
