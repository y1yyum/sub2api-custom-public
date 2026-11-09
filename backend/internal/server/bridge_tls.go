package server

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
)

type BridgeTLSListener struct {
	server   *http.Server
	listener net.Listener
	done     chan struct{}
}

// StartConfiguredBridgeTLS 为受信入口提供独立 TLS，不改变原 HTTP 监听和健康检查。
func StartConfiguredBridgeTLS(handler http.Handler) (*BridgeTLSListener, error) {
	value := os.Getenv("SUB2API_BRIDGE_ENABLED")
	if value == "" {
		return nil, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return nil, errors.New("invalid SUB2API_BRIDGE_ENABLED")
	}
	if !enabled {
		return nil, nil
	}
	address := os.Getenv("SUB2API_BRIDGE_TLS_ADDR")
	if address == "" {
		return nil, nil
	}
	certFile, keyFile := os.Getenv("SUB2API_BRIDGE_TLS_CERT_FILE"), os.Getenv("SUB2API_BRIDGE_TLS_KEY_FILE")
	if certFile == "" || keyFile == "" {
		return nil, errors.New("bridge TLS certificate and key files are required")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, errors.New("cannot load bridge TLS certificate and key")
	}
	return startBridgeTLS(handler, address, certificate, 30*time.Second)
}

func startBridgeTLS(handler http.Handler, address string, certificate tls.Certificate, readTimeout time.Duration) (*BridgeTLSListener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, errors.New("cannot listen on bridge TLS address")
	}
	privateHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != custombridge.ForwardPath {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: privateHandler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: readTimeout, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}}}
	runtime := &BridgeTLSListener{server: server, listener: listener, done: make(chan struct{})}
	go func() {
		defer close(runtime.done)
		if err := server.Serve(tls.NewListener(listener, server.TLSConfig)); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			log.Print("bridge TLS listener stopped unexpectedly")
		}
	}()
	return runtime, nil
}

func (l *BridgeTLSListener) Addr() net.Addr { return l.listener.Addr() }

func (l *BridgeTLSListener) Close() error {
	_ = l.listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := l.server.Shutdown(ctx)
	if err != nil {
		_ = l.server.Close()
	}
	select {
	case <-l.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return err
}
