package upstreamsites

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// monitor.go queries explicit account endpoints with bounded, redirect-free requests.
// Public target resolution is pinned at dialing time. Transport, authentication,
// quota and malformed-response failures remain distinct and never become zero balances.

var (
	errWallet = errors.New("SITE_BALANCE_UNAVAILABLE")       // Missing authoritative account amount.
	errAuth   = errors.New("UPSTREAM_AUTHENTICATION_FAILED") // Rejected monitoring account credential.
)

// publicDial resolves and connects only globally routable destination addresses.
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, a := range ips {
		if !a.IP.IsGlobalUnicast() || a.IP.IsPrivate() || a.IP.IsLoopback() || a.IP.IsLinkLocalUnicast() {
			return nil, errors.New("private target rejected")
		}
	}
	var last error
	for _, a := range ips {
		c, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
		if e == nil {
			return c, nil
		}
		last = e
	}
	if last == nil {
		last = errors.New("no target addresses")
	}
	return nil, last
}

// monitorClient disables redirects and ambient proxies to contain credential delivery.
func monitorClient(proxy string) *http.Client {
	tr := &http.Transport{DialContext: publicDial, ForceAttemptHTTP2: true, MaxIdleConns: 100,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ExpectContinueTimeout: time.Second}
	if proxy != "" {
		if u, e := url.Parse(proxy); e == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// endpoint preserves deployment prefixes while removing one conventional v1 suffix.
func endpoint(base, path string) string {
	return strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + path
}

// request reads at most two MiB and never returns upstream bodies as diagnostics.
func request(ctx context.Context, c *http.Client, target, key, user string) ([]byte, int, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	r.Header.Set("Accept", "application/json")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if user != "" {
		r.Header.Set("New-Api-User", user)
	}
	response, err := c.Do(r)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if len(body) > 2*1024*1024 {
		return nil, response.StatusCode, errWallet
	}
	return body, response.StatusCode, err
}

// observe classifies a wallet or explicitly configured probe without provider writes.
func (p *Manager) observe(parent context.Context, c Config, s Secrets, source string) Observation {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	o := Observation{RunID: hex.EncodeToString(nonce), Source: source, At: time.Now().UTC(), Outcome: Unknown, Code: "SITE_BALANCE_UNAVAILABLE"}
	client := p.client
	if s.ProxyURL != "" {
		client = monitorClient(s.ProxyURL)
		defer client.CloseIdleConnections()
	}
	path := map[string]string{"sub2api": "/v1/usage", "zivv": "/v1/user/balance", "derouter": "/balance", "kimi_balance": "/v1/users/me/balance", "deepseek_balance": "/user/balance", "new_api": "/user/balance"}[c.Adapter]
	target, key, user := endpoint(c.BaseURL, path), s.Key, ""
	var probe *Probe
	timeout := 25 * time.Second
	if source != "balance" {
		for i := range c.Probes {
			if c.Probes[i].ID == source {
				probe = &c.Probes[i]
				break
			}
		}
		if probe == nil {
			o.Outcome = Configuration
			o.Code = "PROBE_NOT_CONFIGURED"
			return o
		}
		target = probe.URL
		key = s.ProbeKeys[source]
		if probe.UseWalletKey {
			key = s.Key
		}
		timeout = time.Duration(probe.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var body []byte
	var status int
	var err error
	newAPI := source == "balance" && c.Adapter == "new_api"
	if newAPI {
		o.Balances, status, err = queryNewAPI(ctx, client, c.BaseURL, s)
	} else {
		body, status, err = request(ctx, client, target, key, user)
	}
	o.At = time.Now().UTC()
	o.HTTPStatus = status
	if errors.Is(err, errWallet) {
		return o
	}
	if errors.Is(err, errAuth) {
		o.Outcome = Authentication
		o.Code = errAuth.Error()
		return o
	}
	if errors.Is(err, ErrInvalid) {
		o.Outcome = Configuration
		o.Code = "NEW_API_USER_ID_INVALID"
		return o
	}
	if err != nil {
		o.Code = "MONITOR_REQUEST_FAILED"
		o.Outcome = MonitorFailure
		if s.ProxyURL == "" && parent.Err() == nil {
			if isNetworkOutage(err) {
				o.Outcome = Unreachable
				o.Code = "UPSTREAM_UNREACHABLE"
			}
		}
		return o
	}
	if status == 401 || status == 403 {
		o.Outcome = Authentication
		o.Code = "UPSTREAM_AUTHENTICATION_FAILED"
		return o
	}
	if status == 429 {
		o.Outcome = RateLimited
		o.Code = "UPSTREAM_RATE_LIMITED"
		return o
	}
	if status >= 500 && s.ProxyURL == "" {
		o.Outcome = Unreachable
		o.Code = "UPSTREAM_UNAVAILABLE"
		return o
	}
	if status < 200 || status >= 300 {
		return o
	}
	if probe != nil {
		ok := len(probe.SuccessCodes) == 0
		for _, allowed := range probe.SuccessCodes {
			ok = ok || status == allowed
		}
		if ok && json.Valid(body) && gjson.GetBytes(body, probe.JSONPath).Exists() && gjson.GetBytes(body, probe.JSONPath).String() == probe.Expected {
			o.Outcome = Healthy
			o.Code = "PROBE_OK"
		} else {
			o.Code = "PROBE_RESPONSE_MISMATCH"
			if json.Valid(body) && gjson.GetBytes(body, probe.JSONPath).Exists() {
				o.Outcome = ProbeUnhealthy
			}
		}
		return o
	}
	if !newAPI {
		o.Balances, err = parseWallet(c.Adapter, body)
	}
	if errors.Is(err, errAuth) {
		o.Outcome = Authentication
		o.Code = errAuth.Error()
		return o
	}
	if err != nil {
		return o
	}
	if len(o.Balances) == 0 {
		return o
	}
	o.Outcome = Healthy
	o.Code = "WALLET_OK"
	return o
}

// isNetworkOutage accepts connection and DNS failures while excluding TLS/configuration errors.
// An outer url.Error implements net.Error even for invalid header values, so its
// mere presence cannot establish an upstream outage or justify suspending providers.
func isNetworkOutage(err error) bool {
	var cert *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &cert) || errors.As(err, &authority) || errors.As(err, &invalid) {
		return false
	}
	var op *net.OpError
	var dns *net.DNSError
	if errors.As(err, &op) || errors.As(err, &dns) {
		return true
	}
	var u *url.Error
	if errors.As(err, &u) {
		err = u.Err
	}
	return errors.Is(err, context.DeadlineExceeded)
}
