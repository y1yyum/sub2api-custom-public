//go:build unit

package probehttp

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type fakeResolver struct {
	answers []netip.Addr
	err     error
	calls   int
}

func (r *fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls++
	return append([]netip.Addr(nil), r.answers...), r.err
}

func ipAnswers(values ...string) []netip.Addr {
	result := make([]netip.Addr, len(values))
	for i, value := range values {
		result[i] = netip.MustParseAddr(value)
	}
	return result
}

func TestProbeDeniedDNSNeverDialsOrSendsCredentials(t *testing.T) {
	for _, tc := range []struct {
		name      string
		addresses []netip.Addr
		err       error
	}{
		{"dns failure", nil, errors.New("synthetic resolution failure")},
		{"empty answer", nil, nil},
		{"loopback", ipAnswers("127.0.0.1"), nil},
		{"private", ipAnswers("10.1.2.3"), nil},
		{"mixed", ipAnswers("203.0.113.9", "169.254.169.254"), nil},
		{"mapped", ipAnswers("::ffff:127.0.0.1"), nil},
		{"nat64", ipAnswers("64:ff9b::a9fe:a9fe"), nil},
		{"shared metadata", ipAnswers("100.100.100.200"), nil},
		{"multicast", ipAnswers("224.0.0.1"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &fakeResolver{answers: tc.addresses, err: tc.err}
			dials := 0
			client := NewClient(Options{Resolver: resolver, DialContext: func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("must not dial")
			}}, nil, nil)
			req, err := http.NewRequest(http.MethodGet, "https://probe.example.test/usage", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer synthetic-fixture")
			_, err = client.Do(req)
			require.Error(t, err)
			require.Zero(t, dials)
			require.Equal(t, 1, resolver.calls)
		})
	}
}

type wireRecord struct {
	destination   string
	host          string
	authorization string
	path          string
}

func responseDialer(t *testing.T, response string) (func(context.Context, string, string) (net.Conn, error), <-chan wireRecord) {
	t.Helper()
	records := make(chan wireRecord, 8)
	return func(_ context.Context, _, destination string) (net.Conn, error) {
		client, server := net.Pipe()
		_ = server.SetDeadline(time.Now().Add(3 * time.Second))
		t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
		go func() {
			defer server.Close()
			req, err := http.ReadRequest(bufio.NewReader(server))
			if err != nil {
				return
			}
			records <- wireRecord{destination, req.Host, req.Header.Get("Authorization"), req.URL.Path}
			_, _ = io.WriteString(server, response)
		}()
		return client, nil
	}, records
}

func TestProbeDialsOnlyValidatedLiteralPreservingHost(t *testing.T) {
	resolver := &fakeResolver{answers: ipAnswers("203.0.113.9")}
	dial, records := responseDialer(t, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	client := NewClient(Options{AllowHTTP: true, Resolver: resolver, DialContext: dial}, nil, nil)
	req, err := http.NewRequest(http.MethodGet, "http://probe.example.test:8123/usage", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer synthetic-fixture")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))
	require.Equal(t, wireRecord{"203.0.113.9:8123", "probe.example.test:8123", "Bearer synthetic-fixture", "/usage"}, <-records)
	require.Equal(t, 1, resolver.calls, "the actual dial must not resolve the hostname again")
}

func TestProbePrivateExceptionIsInstanceLocal(t *testing.T) {
	for _, allow := range []bool{false, true} {
		resolver := &fakeResolver{answers: ipAnswers("10.1.2.3")}
		dial, records := responseDialer(t, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
		client := NewClient(Options{AllowHTTP: true, AllowPrivate: allow, Resolver: resolver, DialContext: dial}, nil, nil)
		resp, err := client.Get("http://private.example.test/usage")
		if !allow {
			require.Error(t, err)
			require.Empty(t, records)
			continue
		}
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, "10.1.2.3:80", (<-records).destination)
	}
}

func TestProbeCrossOriginRedirectDoesNotForwardSecret(t *testing.T) {
	for _, destination := range []string{"http://127.0.0.1/metadata", "http://other.example.test/usage", "https://probe.example.test/usage"} {
		resolver := &fakeResolver{answers: ipAnswers("203.0.113.9")}
		dial, records := responseDialer(t, "HTTP/1.1 302 Found\r\nLocation: "+destination+"\r\nContent-Length: 0\r\n\r\n")
		client := NewClient(Options{AllowHTTP: true, Resolver: resolver, DialContext: dial}, nil, nil)
		req, err := http.NewRequest(http.MethodGet, "http://probe.example.test/usage", nil)
		require.NoError(t, err)
		req.Header.Set("X-Api-Key", "synthetic-fixture")
		_, err = client.Do(req)
		require.ErrorContains(t, err, "cross-origin")
		require.Equal(t, 1, resolver.calls)
		require.Len(t, records, 1)
	}
}

type rebindingResolver struct {
	mu    sync.Mutex
	calls int
}

func (r *rebindingResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls == 1 {
		return ipAnswers("203.0.113.9"), nil
	}
	return ipAnswers("127.0.0.1"), nil
}

func TestProbeSameOriginRedirectRevalidatesDNS(t *testing.T) {
	resolver := &rebindingResolver{}
	dial, records := responseDialer(t, "HTTP/1.1 302 Found\r\nLocation: /second\r\nContent-Length: 0\r\n\r\n")
	client := NewClient(Options{AllowHTTP: true, Resolver: resolver, DialContext: dial}, nil, nil)
	_, err := client.Get("http://probe.example.test/first")
	require.ErrorContains(t, err, "not allowed")
	require.Equal(t, 2, resolver.calls)
	require.Len(t, records, 1)
}

func TestProbeRejectsURLAndHostAmbiguityBeforeDNS(t *testing.T) {
	for _, target := range []string{"http://probe.example.test", "https://user:synthetic@probe.example.test", "https://probe.example.test/#fragment"} {
		resolver := &fakeResolver{answers: ipAnswers("203.0.113.9")}
		client := NewClient(Options{Resolver: resolver}, nil, nil)
		_, err := client.Get(target)
		require.Error(t, err)
		require.Zero(t, resolver.calls)
	}
	resolver := &fakeResolver{answers: ipAnswers("203.0.113.9")}
	client := NewClient(Options{Resolver: resolver}, nil, nil)
	req, _ := http.NewRequest(http.MethodGet, "https://probe.example.test", nil)
	req.Host = "metadata.internal"
	_, err := client.Do(req)
	require.Error(t, err)
	require.Zero(t, resolver.calls)
}

func TestProbeHTTPProxyReceivesPinnedCONNECTNotHostname(t *testing.T) {
	proxyURL, err := url.Parse("http://proxy-user:synthetic@127.0.0.1:8080")
	require.NoError(t, err)
	requestLines := make(chan string, 1)
	dial := func(ctx context.Context, network, destination string) (net.Conn, error) {
		if destination != "127.0.0.1:8080" {
			return nil, errors.New("proxy destination differs from persisted selection")
		}
		client, server := net.Pipe()
		_ = server.SetDeadline(time.Now().Add(3 * time.Second))
		t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
		go func() {
			defer server.Close()
			reader := bufio.NewReader(server)
			request, readErr := http.ReadRequest(reader)
			if readErr != nil {
				return
			}
			requestLines <- request.Method + " " + request.Host
			_, _ = io.WriteString(server, "HTTP/1.1 200 Connection established\r\n\r\n")
			request, readErr = http.ReadRequest(reader)
			if readErr != nil {
				return
			}
			_, _ = io.WriteString(server, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
		}()
		return client, nil
	}
	client := NewClient(Options{AllowHTTP: true, Resolver: &fakeResolver{answers: ipAnswers("203.0.113.9")}, DialContext: dial}, proxyURL, nil)
	resp, err := client.Get("http://probe.example.test/usage")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", strings.TrimSpace(string(body)))
	require.Equal(t, "CONNECT 203.0.113.9:80", <-requestLines)
}

func TestProbeTLSKeepsOriginalSNIWithPinnedDial(t *testing.T) {
	for _, profile := range []*tlsfingerprint.Profile{nil, {Name: "synthetic-fixture"}} {
		names := make(chan string, 1)
		dial := func(_ context.Context, _, destination string) (net.Conn, error) {
			if destination != "203.0.113.9:443" {
				return nil, errors.New("not a pinned destination")
			}
			client, server := net.Pipe()
			_ = server.SetDeadline(time.Now().Add(3 * time.Second))
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			go func() {
				defer server.Close()
				tlsServer := tls.Server(server, &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
					names <- hello.ServerName
					return nil, errors.New("synthetic server stops after ClientHello")
				}})
				_ = tlsServer.Handshake()
			}()
			return client, nil
		}
		client := NewClient(Options{Resolver: &fakeResolver{answers: ipAnswers("203.0.113.9")}, DialContext: dial}, nil, profile)
		_, err := client.Get("https://probe.example.test/usage")
		require.Error(t, err)
		select {
		case name := <-names:
			require.Equal(t, "probe.example.test", name)
		case <-time.After(4 * time.Second):
			t.Fatal("missing bounded TLS ClientHello")
		}
	}
}

func TestProbeSOCKSReceivesLiteralAddress(t *testing.T) {
	proxyURL, err := url.Parse("socks5h://127.0.0.1:1080")
	require.NoError(t, err)
	targets := make(chan []byte, 1)
	dial := func(_ context.Context, _, destination string) (net.Conn, error) {
		if destination != "127.0.0.1:1080" {
			return nil, errors.New("wrong proxy endpoint")
		}
		client, server := net.Pipe()
		_ = server.SetDeadline(time.Now().Add(3 * time.Second))
		t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
		go func() {
			defer server.Close()
			greeting := make([]byte, 3)
			if _, err := io.ReadFull(server, greeting); err != nil {
				return
			}
			if _, err := server.Write([]byte{5, 0}); err != nil {
				return
			}
			connect := make([]byte, 10)
			if _, err := io.ReadFull(server, connect); err != nil {
				return
			}
			targets <- connect
			if _, err := server.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
				return
			}
			if _, err := http.ReadRequest(bufio.NewReader(server)); err != nil {
				return
			}
			_, _ = io.WriteString(server, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
		}()
		return client, nil
	}
	client := NewClient(Options{AllowHTTP: true, Resolver: &fakeResolver{answers: ipAnswers("203.0.113.9")}, DialContext: dial}, proxyURL, nil)
	resp, err := client.Get("http://probe.example.test/usage")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, []byte{5, 1, 0, 1, 203, 0, 113, 9, 0, 80}, <-targets, "SOCKS must receive ATYP IPv4, never a hostname")
}

func TestProbeProxyHandshakeHonorsCancellation(t *testing.T) {
	for _, scheme := range []string{"http", "socks5h"} {
		proxyURL, err := url.Parse(scheme + "://127.0.0.1:1080")
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		connected := make(chan struct{})
		dial := func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			close(connected)
			return client, nil // peer deliberately sends no handshake response
		}
		result := make(chan error, 1)
		go func() {
			conn, err := dialViaProxy(ctx, "tcp", "203.0.113.9:443", proxyURL, dial)
			if conn != nil {
				_ = conn.Close()
			}
			result <- err
		}()
		<-connected
		cancel()
		select {
		case err := <-result:
			require.Error(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("proxy handshake ignored cancellation")
		}
	}
}

func TestProbePrivateExceptionDoesNotAllowSpecialAddresses(t *testing.T) {
	for _, ip := range []string{"0.0.0.0", "::", "169.254.169.254", "fe80::1", "224.0.0.1", "100.100.100.200", "64:ff9b::a9fe:a9fe"} {
		transport := &Transport{options: Options{AllowPrivate: true}}
		_, err := transport.addresses(context.Background(), ip)
		require.Error(t, err, ip)
	}
}
