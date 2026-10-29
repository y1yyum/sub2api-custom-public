//go:build unit

package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountProbeRepositoryPolicyDeniesBeforeSendingCredentials(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = io.WriteString(w, "ok") }))
	defer server.Close()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Security.URLAllowlist.AllowPrivateHosts = true // legacy gateway default is not a probe exception
	upstream := NewHTTPUpstream(cfg)
	for _, allow := range []bool{false, true} {
		cfg.Security.AccountProbe.AllowPrivateHosts = allow
		ctx := service.WithHTTPUpstreamAccountProbe(context.Background(), cfg)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer synthetic-fixture")
		resp, err := upstream.Do(req, "", 123, 1)
		if !allow {
			require.Error(t, err)
			require.Zero(t, calls.Load())
			continue
		}
		require.NoError(t, err)
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, int64(1), calls.Load())
	}
}

func TestAccountProbeRepositoryPreservesStricterMarkers(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Security.AccountProbe.AllowPrivateHosts = true
	ctx := service.WithHTTPUpstreamAccountProbe(service.WithHTTPUpstreamPublicHostsOnly(context.Background()), cfg)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/never-dial", nil)
	require.NoError(t, err)
	_, err = NewHTTPUpstream(cfg).Do(req, "", 1, 1)
	require.ErrorContains(t, err, "policy rejected")
}
