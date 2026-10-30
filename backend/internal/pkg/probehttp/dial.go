package probehttp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"time"
)

// dialVerifiedAddresses races only the fully validated literal answer set. One
// absolute budget covers DNS and every direct/proxy handshake; no retry renews it.
// Staggering follows net.Dialer's default 300ms fast fallback interval, including
// same-family answers. At most the validated answer cap (64) can be in flight.
func dialVerifiedAddresses(parent context.Context, deadline time.Time, network, port string, addresses []netip.Addr, proxyURL *url.URL, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	return dialVerifiedAddressesWithDelay(parent, deadline, network, port, addresses, proxyURL, dial, 300*time.Millisecond)
}

// The private delay parameter permits deterministic scheduler tests; production
// always enters through the fixed 300ms wrapper above.
func dialVerifiedAddressesWithDelay(parent context.Context, deadline time.Time, network, port string, addresses []netip.Addr, proxyURL *url.URL, dial func(context.Context, string, string) (net.Conn, error), fallbackDelay time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, errors.New("no verified probe addresses")
	}
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result)
	next, active := 0, 0
	start := func() {
		target := net.JoinHostPort(addresses[next].String(), port)
		next++
		active++
		go func() {
			conn, err := dialViaProxy(ctx, network, target, proxyURL, dial)
			if err != nil && conn != nil {
				_ = conn.Close()
				conn = nil
			}
			select {
			case results <- result{conn, err}:
			case <-ctx.Done():
				if conn != nil {
					_ = conn.Close()
				}
			}
		}()
	}
	start()
	timer := time.NewTimer(fallbackDelay)
	defer timer.Stop()
	var last error
	for active > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case attempt := <-results:
			active--
			if attempt.err == nil && attempt.conn != nil {
				if err := ctx.Err(); err != nil {
					_ = attempt.conn.Close()
					return nil, err
				}
				return attempt.conn, nil
			}
			last = attempt.err
			if next < len(addresses) {
				start()
				timer.Reset(fallbackDelay)
			}
		case <-timer.C:
			if next < len(addresses) {
				start()
				timer.Reset(fallbackDelay)
			}
		}
	}
	if last == nil {
		last = errors.New("verified probe addresses failed")
	}
	return nil, last
}
