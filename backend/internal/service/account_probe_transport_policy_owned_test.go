//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/probehttp"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestAccountProbePrivateOptInIsSeparateFromGatewayDefaults(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	policy := accountProbeOptions(cfg)
	require.False(t, policy.AllowPrivate)
	require.True(t, policy.AllowHTTP)
	_, err := probehttp.ValidateURL("http://127.0.0.1/model", policy)
	require.Error(t, err)
	cfg.Security.AccountProbe.AllowPrivateHosts = true
	policy = accountProbeOptions(cfg)
	_, err = probehttp.ValidateURL("http://127.0.0.1/model", policy)
	require.NoError(t, err)
	_, err = probehttp.ValidateURL("http://169.254.169.254/metadata", policy)
	require.Error(t, err)
	require.True(t, cfg.Security.URLAllowlist.AllowPrivateHosts, "gateway configuration must remain unchanged")
}

func TestAccountProbeKeepsExistingHTTPAndHostAllowlistPolicy(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = true
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"approved.example.test"}
	cfg.Security.AccountProbe.AllowPrivateHosts = true
	policy := accountProbeOptions(cfg)
	require.False(t, policy.AllowHTTP, "enabled allowlist has always required HTTPS")
	_, err := probehttp.ValidateURL("https://approved.example.test/v1", policy)
	require.NoError(t, err)
	for _, target := range []string{"http://approved.example.test/v1", "https://other.example.test/v1", "https://127.0.0.1/v1"} {
		_, err := probehttp.ValidateURL(target, policy)
		require.Error(t, err)
	}
}

type probePolicyCapture struct {
	options []probehttp.Options
	proxies []string
}

func (f *probePolicyCapture) Do(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	options, ok := HTTPUpstreamAccountProbeOptions(req.Context())
	if !ok {
		return nil, context.Canceled
	}
	f.options = append(f.options, options)
	f.proxies = append(f.proxies, proxy)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}
func (f *probePolicyCapture) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f.Do(req, proxy, accountID, concurrency)
}

func TestAccountProbeWrapperMarksHTTPAndTLSWithoutMutatingCaller(t *testing.T) {
	cfg := &config.Config{}
	capture := &probePolicyCapture{}
	wrapper := newAccountProbeHTTPUpstream(capture, cfg)
	req, err := http.NewRequest(http.MethodGet, "https://public.example.test", nil)
	require.NoError(t, err)
	for _, tls := range []bool{false, true} {
		var resp *http.Response
		if tls {
			resp, err = wrapper.DoWithTLS(req, "http://persisted-proxy.example.test:8080", 1, 1, &tlsfingerprint.Profile{})
		} else {
			resp, err = wrapper.Do(req, "http://persisted-proxy.example.test:8080", 1, 1)
		}
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	require.Len(t, capture.options, 2)
	require.False(t, capture.options[0].AllowPrivate)
	require.Equal(t, capture.proxies[0], capture.proxies[1])
	_, marked := HTTPUpstreamAccountProbeOptions(req.Context())
	require.False(t, marked)
	require.Nil(t, newAccountProbeHTTPUpstream(nil, cfg))
}

// Read-only diagnostics may show a redacted URL; they must neither perform a
// request nor turn that redacted view into permission to probe the original.
func TestAccountProbeDiagnosticsRedactWithoutAuthorizingCredentialURL(t *testing.T) {
	cfg := &config.Config{}
	capture := &probePolicyCapture{}
	svc := &AccountTestService{cfg: cfg, httpUpstream: newAccountProbeHTTPUpstream(capture, cfg)}
	original := "https://user:synthetic-secret@relay.example.com/v1?access_token=synthetic-query#private-fragment"
	account := &Account{ID: 25, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": original}}
	diagnostic := svc.GetGrokModelDiagnostics(context.Background(), account)
	require.Equal(t, "https://relay.example.com/v1/models", diagnostic.Endpoint)
	require.Empty(t, capture.options, "diagnostics must never call the injected upstream")
	_, err := svc.validateUpstreamBaseURL(original)
	require.Error(t, err, "the original URL remains forbidden for actual probes")
	require.Equal(t, original, account.GetCredential("base_url"), "redaction must not rewrite stored credentials")
	cfg.Security.URLAllowlist.Enabled = true
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"approved.example.test"}
	require.Empty(t, svc.GetGrokModelDiagnostics(context.Background(), account).Endpoint, "redaction must not bypass host policy")
}
