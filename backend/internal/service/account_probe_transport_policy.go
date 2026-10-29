package service

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/probehttp"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

type accountProbePolicyContextKey struct{}

func accountProbeOptions(cfg *config.Config) probehttp.Options {
	if cfg == nil {
		return probehttp.Options{}
	}
	policy := cfg.Security.URLAllowlist
	options := probehttp.Options{
		AllowHTTP:        !policy.Enabled && policy.AllowInsecureHTTP,
		AllowPrivate:     cfg.Security.AccountProbe.AllowPrivateHosts,
		RequireAllowlist: policy.Enabled,
	}
	if policy.Enabled {
		options.AllowedHosts = append([]string(nil), policy.UpstreamHosts...)
	}
	return options
}

// WithHTTPUpstreamAccountProbe marks trusted server-side probe execution. No
// incoming request field can set this policy or supply resolver/dial overrides.
func WithHTTPUpstreamAccountProbe(ctx context.Context, cfg *config.Config) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, accountProbePolicyContextKey{}, accountProbeOptions(cfg))
}

func HTTPUpstreamAccountProbeOptions(ctx context.Context) (probehttp.Options, bool) {
	if ctx == nil {
		return probehttp.Options{}, false
	}
	options, ok := ctx.Value(accountProbePolicyContextKey{}).(probehttp.Options)
	options.AllowedHosts = append([]string(nil), options.AllowedHosts...)
	return options, ok
}

type accountProbeHTTPUpstream struct {
	next HTTPUpstream
	cfg  *config.Config
}

func newAccountProbeHTTPUpstream(next HTTPUpstream, cfg *config.Config) HTTPUpstream {
	if next == nil {
		return nil
	}
	return &accountProbeHTTPUpstream{next: next, cfg: cfg}
}

func (p *accountProbeHTTPUpstream) request(req *http.Request) (*http.Request, error) {
	if req == nil {
		return nil, errors.New("nil account probe request")
	}
	return req.Clone(WithHTTPUpstreamAccountProbe(req.Context(), p.cfg)), nil
}

func (p *accountProbeHTTPUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	marked, err := p.request(req)
	if err != nil {
		return nil, err
	}
	return p.next.Do(marked, proxyURL, accountID, concurrency)
}

func (p *accountProbeHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	marked, err := p.request(req)
	if err != nil {
		return nil, err
	}
	return p.next.DoWithTLS(marked, proxyURL, accountID, concurrency, profile)
}
