package relaymonitor

import (
	"context"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (m *Module) validateURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Fragment != "" {
		return errors.New("站点 URL 无效，不允许 URL 凭据或片段")
	}
	var p config.URLAllowlistConfig
	if m.cfg != nil {
		p = m.cfg.Security.URLAllowlist
	}
	hosts := p.UpstreamHosts
	if !p.Enabled {
		hosts = nil
	}
	_, e = urlvalidator.ValidateHTTPURL(raw, p.AllowInsecureHTTP, urlvalidator.ValidationOptions{AllowedHosts: hosts, RequireAllowlist: p.Enabled, AllowPrivate: p.AllowPrivateHosts})
	return e
}
func relayBlockedIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return true
		}
	}
	return false
}
func (m *Module) dialDestination(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	var p config.URLAllowlistConfig
	if m.cfg != nil {
		p = m.cfg.Security.URLAllowlist
	}
	if p.Enabled {
		_, e = urlvalidator.ValidateHTTPURL("https://"+net.JoinHostPort(host, port), false, urlvalidator.ValidationOptions{AllowedHosts: p.UpstreamHosts, RequireAllowlist: true, AllowPrivate: p.AllowPrivateHosts})
		if e != nil {
			return nil, e
		}
	}
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil {
		return nil, errors.New("目标 DNS 解析失败")
	}
	if len(ips) == 0 {
		return nil, errors.New("目标 DNS 无地址")
	}
	for _, entry := range ips {
		if !p.AllowPrivateHosts && relayBlockedIP(entry.IP) {
			return nil, errors.New("目标解析到禁止的网络地址")
		}
	}
	var last error
	for _, entry := range ips {
		dialer := net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(entry.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, fmt.Errorf("目标连接失败: %w", last)
}

type relayPolicyTransport struct {
	module    *Module
	transport *http.Transport
}

func (t relayPolicyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if e := t.module.validateURL(r.URL.String()); e != nil {
		return nil, e
	}
	return t.transport.RoundTrip(r)
}
func relayOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}
func newOutboundClient(cfg *config.Config) *http.Client {
	m := &Module{cfg: cfg}
	transport := &http.Transport{Proxy: nil, DialContext: m.dialDestination, MaxIdleConns: 20, MaxIdleConnsPerHost: 4, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 9 * time.Second, ForceAttemptHTTP2: true}
	return &http.Client{Timeout: 10 * time.Second, Transport: relayPolicyTransport{m, transport}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("跳转次数超过 3 次")
		}
		if len(via) > 0 && relayOrigin(req.URL) != relayOrigin(via[0].URL) {
			return errors.New("禁止跨源跳转，防止凭据泄露")
		}
		return m.validateURL(req.URL.String())
	}}
}
