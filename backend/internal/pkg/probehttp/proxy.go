package probehttp

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

type contextForward struct {
	ctx  context.Context
	dial func(context.Context, string, string) (net.Conn, error)
}

func (d contextForward) Dial(network, addr string) (net.Conn, error) {
	return d.dial(d.ctx, network, addr)
}

func (d contextForward) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.dial(ctx, network, addr)
}

// proxyURL is exclusively the persisted administrator-selected proxy, not the
// untrusted target. Its existing network trust is independent of target policy.
func dialViaProxy(ctx context.Context, network, target string, proxyURL *url.URL, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if proxyURL == nil {
		return dial(ctx, network, target)
	}
	scheme := strings.ToLower(proxyURL.Scheme)
	if scheme == "socks5" || scheme == "socks5h" {
		var auth *proxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &proxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		d, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, contextForward{ctx: ctx, dial: dial})
		if err != nil {
			return nil, errors.New("invalid probe SOCKS proxy")
		}
		contextDialer, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("probe proxy lacks context cancellation")
		}
		return contextDialer.DialContext(ctx, network, target)
	}
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("unsupported probe proxy scheme")
	}
	proxyPort := proxyURL.Port()
	if proxyPort == "" {
		proxyPort = "80"
		if scheme == "https" {
			proxyPort = "443"
		}
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(proxyURL.Hostname(), proxyPort))
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	deadline := time.Now().Add(10 * time.Second)
	if limit, exists := ctx.Deadline(); exists && limit.Before(deadline) {
		deadline = limit
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = tlsConn
	}
	// Always tunnel to the pinned literal, even for an HTTP target. Sending an
	// absolute-form hostname would delegate a second DNS lookup to the proxy.
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		credentials := proxyURL.User.Username() + ":" + password
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	}
	if err := request.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(conn, 4096)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, errors.New("invalid probe proxy CONNECT response")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("probe proxy CONNECT rejected with status %d", response.StatusCode)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	ok = true
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
