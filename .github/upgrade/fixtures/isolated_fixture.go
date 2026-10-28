// Package isolatedfixture supports restored integration tests only. It is copied
// into the external validation workspace, never linked into the shipped server.
package isolatedfixture

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

// RequireIsolated is defense in depth, not a replacement for the reviewed
// --network=none/cap-drop/no-host-mount runtime boundary.
func RequireIsolated() error {
	if os.Getenv("SUB2API_ISOLATED_INTEGRATION") != "1" {
		return errors.New("isolated integration mode was not explicitly selected")
	}
	if os.Geteuid() == 0 {
		return errors.New("integration fixtures must run as a non-root user")
	}
	if os.Getenv("DOCKER_HOST") != "" {
		return errors.New("Docker endpoint must not be exposed to integration tests")
	}
	for _, socket := range []string{"/var/run/docker.sock", "/run/docker.sock"} {
		if _, err := os.Lstat(socket); err == nil {
			return errors.New("Docker socket must not be mounted into integration tests")
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("cannot verify socket isolation: %w", err)
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("cannot inspect namespace interfaces: %w", err)
	}
	if len(interfaces) == 0 {
		return errors.New("missing loopback interface")
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback == 0 {
			return errors.New("non-loopback network interface found; refusing fixtures")
		}
	}
	return nil
}

// PostgresDSN only accepts fixed roles for fresh disposable databases created
// by the isolated container bootstrap. It never consumes an operator DSN.
func PostgresDSN(purpose string) (string, error) {
	if err := RequireIsolated(); err != nil {
		return "", err
	}
	var database string
	switch purpose {
	case "repository":
		database = "sub2api_repository"
	case "prompt-audit":
		database = "sub2api_prompt_audit"
	default:
		return "", errors.New("unknown disposable database purpose")
	}
	return "postgres://fixture:fixture-only@127.0.0.1:55432/" + database + "?sslmode=disable&TimeZone=UTC", nil
}

// StartRedis creates a fresh real Redis process and Unix socket per invocation.
// No daemon/socket/container manager is contacted and no TCP port is opened.
// The absolute executable and all options are fixed, not supplied by test data.
func StartRedis(ctx context.Context) (*redis.Client, func() error, error) {
	if err := RequireIsolated(); err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "sub2api-redis-")
	if err != nil {
		return nil, nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "redis.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}
	socket := filepath.Join(dir, "redis.sock")
	cmd := exec.Command("/usr/local/bin/redis-server", "--port", "0", "--bind", "127.0.0.1",
		"--protected-mode", "yes", "--unixsocket", socket, "--unixsocketperm", "700",
		"--save", "", "--appendonly", "no", "--daemonize", "no", "--dir", dir)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + dir, "TZ=UTC"}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("start isolated Redis: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 0,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	var once sync.Once
	var cleanupErr error
	stop := func() error {
		once.Do(func() {
			_ = client.Close()
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case waitErr := <-done:
				if waitErr != nil {
					cleanupErr = errors.New("isolated Redis exited unsuccessfully")
				}
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				cleanupErr = errors.New("isolated Redis required forced termination")
			}
			_ = logFile.Close()
			if err := os.RemoveAll(dir); cleanupErr == nil {
				cleanupErr = err
			}
		})
		return cleanupErr
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		pingCtx, cancel := context.WithTimeout(ctx, time.Second)
		err = client.Ping(pingCtx).Err()
		cancel()
		if err == nil {
			return client, stop, nil
		}
		select {
		case processErr := <-done:
			// Return the result to the cleanup owner before invoking stop.
			done <- processErr
			_ = stop()
			return nil, nil, errors.New("isolated Redis exited before readiness")
		case <-ctx.Done():
			_ = stop()
			return nil, nil, ctx.Err()
		case <-deadline.C:
			_ = stop()
			return nil, nil, errors.New("isolated Redis readiness deadline exceeded")
		case <-ticker.C:
		}
	}
}
