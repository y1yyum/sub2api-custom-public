// Package probehttp binds account-probe URL policy to the actual network dial.
// It never shares the application's connection pool and never uses environment proxies.
package probehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// Resolver and DialContext are instance-local seams. Tests cannot change the
// production resolver or bypass policy globally.
type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Options struct {
	AllowHTTP        bool
	AllowPrivate     bool
	RequireAllowlist bool
	AllowedHosts     []string
	Resolver         Resolver
	DialContext      func(context.Context, string, string) (net.Conn, error)
}

type Transport struct {
	options Options
	proxy   *url.URL
	profile *tlsfingerprint.Profile
}

func NewClient(options Options, proxyURL *url.URL, profile *tlsfingerprint.Profile) *http.Client {
	options.AllowedHosts = append([]string(nil), options.AllowedHosts...)
	if proxyURL != nil {
		copyURL := *proxyURL
		proxyURL = &copyURL
	}
	return &http.Client{
		Transport: &Transport{options: options, proxy: proxyURL, profile: profile},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("probe redirect limit exceeded")
			}
			// API-key headers are not all recognized by net/http's sensitive-header
			// stripping. Never forward probe credentials to a different origin.
			if len(via) > 0 && !sameOrigin(via[0].URL, req.URL) {
				return errors.New("cross-origin probe redirect rejected")
			}
			return nil // RoundTrip validates and resolves every permitted hop again.
		},
	}
}

func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) && effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Shared-address, benchmarking and reserved IPv4 ranges are not public
	// upstream services. This also blocks cloud metadata in 100.64.0.0/10.
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
		netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("2001::/32"),
	} {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func (t *Transport) addresses(ctx context.Context, host string) ([]netip.Addr, error) {
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		resolver := t.options.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		var err error
		addresses, err = resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("probe DNS resolution failed: %w", err)
		}
	}
	if len(addresses) == 0 || len(addresses) > 64 {
		return nil, errors.New("probe DNS returned an invalid address count")
	}
	// Reject a mixed public/private answer as a whole, never select just the
	// convenient public result and leave another path able to dial the rest.
	for i, ip := range addresses {
		ip = ip.Unmap()
		// An explicit private-service exception means RFC1918/ULA or loopback,
		// not metadata/link-local, multicast, unspecified or reserved ranges.
		allowed := publicIP(ip) || (t.options.AllowPrivate && (ip.IsPrivate() || ip.IsLoopback()))
		if !ip.IsValid() || ip.Zone() != "" || !allowed {
			return nil, errors.New("probe destination address is not allowed")
		}
		addresses[i] = ip
	}
	return addresses, nil
}

// ValidateURL performs syntax, configured-host and literal-address checks.
// Hostnames are resolved only by RoundTrip, immediately before the pinned dial.
func ValidateURL(raw string, options Options) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" {
		return "", errors.New("invalid probe URL")
	}
	if _, err := urlvalidator.ValidateHTTPURL(raw, options.AllowHTTP, urlvalidator.ValidationOptions{
		RequireAllowlist: options.RequireAllowlist, AllowedHosts: options.AllowedHosts, AllowPrivate: options.AllowPrivate,
	}); err != nil {
		return "", errors.New("probe URL policy rejected destination")
	}
	if ip, err := netip.ParseAddr(parsed.Hostname()); err == nil {
		if _, err := (&Transport{options: options}).addresses(context.Background(), ip.String()); err != nil {
			return "", err
		}
	}
	// Do not rewrite the escaped path of a signed provider URL.
	return strings.TrimRight(strings.TrimSpace(raw), "/"), nil
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || req.URL.User != nil || req.URL.Opaque != "" || req.URL.Fragment != "" {
		return nil, errors.New("invalid probe request URL")
	}
	if req.Host != "" && !strings.EqualFold(req.Host, req.URL.Host) {
		return nil, errors.New("probe Host differs from validated URL")
	}
	if _, err := ValidateURL(req.URL.String(), t.options); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	addresses, err := t.addresses(ctx, req.URL.Hostname())
	cancel()
	if err != nil {
		return nil, err
	}
	host, port := req.URL.Hostname(), effectivePort(req.URL)
	baseDial := t.options.DialContext
	if baseDial == nil {
		baseDial = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		requestedHost, requestedPort, splitErr := net.SplitHostPort(addr)
		if splitErr != nil || !strings.EqualFold(requestedHost, host) || requestedPort != port {
			return nil, errors.New("probe dial destination differs from validated URL")
		}
		var last error
		for _, ip := range addresses {
			// The network layer gets only an already-validated literal. No second
			// DNS lookup, including at a configured proxy, can rebind the target.
			conn, dialErr := dialViaProxy(ctx, network, net.JoinHostPort(ip.String(), port), t.proxy, baseDial)
			if dialErr == nil {
				return conn, nil
			}
			last = dialErr
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, last
	}
	transport := &http.Transport{
		DialContext: dial, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 5 * time.Minute,
		DisableKeepAlives: true, MaxConnsPerHost: 1,
	}
	if t.profile != nil && strings.EqualFold(req.URL.Scheme, "https") {
		fingerprint := tlsfingerprint.NewDialer(t.profile, dial)
		transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return fingerprint.DialTLSContext(handshakeCtx, network, address)
		}
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	resp.Body = &responseBody{ReadCloser: resp.Body, closeIdle: transport.CloseIdleConnections}
	return resp, nil
}

type responseBody struct {
	io.ReadCloser
	closeIdle func()
}

func (b *responseBody) Close() error {
	err := b.ReadCloser.Close()
	b.closeIdle()
	return err
}

// A 101 WebSocket response body is bidirectional; preserve that capability.
func (b *responseBody) Write(p []byte) (int, error) {
	if writer, ok := b.ReadCloser.(io.Writer); ok {
		return writer.Write(p)
	}
	return 0, errors.New("probe response body is not writable")
}
