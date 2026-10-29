package repository

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/probehttp"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *httpUpstreamService) doAccountProbe(req *http.Request, proxyURL string, profile *tlsfingerprint.Profile, options probehttp.Options) (*http.Response, error) {
	_, parsedProxy, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	// Preserve stricter per-request markers, never turn a public-only image
	// fetch or a no-redirect billing probe into a more permissive request.
	if service.HTTPUpstreamPublicHostsOnly(req.Context()) {
		options.AllowPrivate = false
	}
	client := probehttp.NewClient(options, parsedProxy, profile)
	if service.HTTPUpstreamRedirectsDisabled(req.Context()) {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	applyGrokCLIProxyHeaders(req)
	client = httpClientWithGrokAccessDeniedFallback(client)
	return doUpstreamRequest(client, req)
}
