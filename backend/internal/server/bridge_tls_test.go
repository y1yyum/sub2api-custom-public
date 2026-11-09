//go:build unit

package server

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBridgeForwardTLSListenerIsolation(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bridge-test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	require.NoError(t, err)
	privateKey, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	certFile, keyFile := filepath.Join(t.TempDir(), "certificate.pem"), filepath.Join(t.TempDir(), "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	require.NoError(t, os.WriteFile(certFile, certPEM, 0600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}), 0600))
	t.Setenv("SUB2API_BRIDGE_ENABLED", "true")
	t.Setenv("SUB2API_BRIDGE_TLS_ADDR", "127.0.0.1:0")
	t.Setenv("SUB2API_BRIDGE_TLS_CERT_FILE", certFile)
	t.Setenv("SUB2API_BRIDGE_TLS_KEY_FILE", keyFile)
	listener, err := StartConfiguredBridgeTLS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.TLS.Version < tls.VersionTLS13 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(certPEM))
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	for _, test := range []struct {
		path   string
		status int
	}{{custombridge.ForwardPath, http.StatusAccepted}, {"/health", http.StatusNotFound}, {"/api/v1/admin", http.StatusNotFound}, {"/v1/responses", http.StatusNotFound}} {
		response, err := client.Get("https://" + listener.Addr().String() + test.path)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, test.status, response.StatusCode)
	}
	requireBridgeTLS12Rejected(t, listener.Addr().String())

	t.Run("partial_body_times_out_without_a_signature", func(t *testing.T) {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		require.NoError(t, err)
		var completed atomic.Int64
		verifier, err := custombridge.NewForwardVerifier("core", []custombridge.SigningKey{{ID: "kid", Secret: make([]byte, 32), InternalKeyID: 12, GroupID: 21}})
		require.NoError(t, err)
		router := gin.New()
		router.POST(custombridge.ForwardPath, func(c *gin.Context) {
			if c.GetHeader("Authorization") != "Bearer synthetic-core-key" {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			groupID := int64(21)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 12, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI}})
		}, middleware.BridgeForward(verifier, nil, false, 1024), func(c *gin.Context) { completed.Add(1); c.Status(http.StatusOK) })
		timed, err := startBridgeTLS(router, "127.0.0.1:0", certificate, 75*time.Millisecond)
		require.NoError(t, err)
		defer func() { require.NoError(t, timed.Close()) }()
		connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", timed.Addr().String(), &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13})
		require.NoError(t, err)
		defer func() { _ = connection.Close() }()
		_, err = fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer synthetic-core-key\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{", custombridge.ForwardPath)
		require.NoError(t, err)
		require.NoError(t, connection.SetReadDeadline(time.Now().Add(2*time.Second)))
		response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
		require.NoError(t, err)
		defer func() { _ = response.Body.Close() }()
		require.Equal(t, http.StatusRequestTimeout, response.StatusCode)
		require.Zero(t, completed.Load())
	})
}

func requireBridgeTLS12Rejected(t *testing.T, address string) {
	t.Helper()
	// TLS 1.2 ClientHello 夹具，校验 RFC 8446 附录 D.2 的 protocol_version 告警。
	hello := append([]byte{22, 3, 1, 0, 47, 1, 0, 0, 43, 3, 3}, make([]byte, 32)...)
	hello = append(hello, 0, 0, 2, 0, 0x2f, 1, 0, 0, 0)
	connection, err := net.DialTimeout("tcp", address, time.Second)
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	require.NoError(t, connection.SetDeadline(time.Now().Add(2*time.Second)))
	_, err = connection.Write(hello)
	require.NoError(t, err)
	var alert [7]byte
	_, err = io.ReadFull(connection, alert[:])
	require.NoError(t, err)
	require.Equal(t, byte(21), alert[0], "TLS alert record")
	require.Equal(t, byte(2), alert[5], "fatal alert")
	require.Equal(t, byte(70), alert[6], "protocol_version rejects the legacy handshake")
}

func TestBridgeForwardTLSDisabledAndMisconfigured(t *testing.T) {
	t.Setenv("SUB2API_BRIDGE_ENABLED", "false")
	t.Setenv("SUB2API_BRIDGE_TLS_ADDR", "invalid-address")
	listener, err := StartConfiguredBridgeTLS(nil)
	require.NoError(t, err)
	require.Nil(t, listener)
	t.Setenv("SUB2API_BRIDGE_ENABLED", "true")
	t.Setenv("SUB2API_BRIDGE_TLS_CERT_FILE", "")
	t.Setenv("SUB2API_BRIDGE_TLS_KEY_FILE", "")
	listener, err = StartConfiguredBridgeTLS(nil)
	require.Error(t, err)
	require.Nil(t, listener)
}
