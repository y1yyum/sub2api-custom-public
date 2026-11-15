package upstreamsites

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type monitorRoundTrip func(*http.Request) (*http.Response, error)

func (f monitorRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMonitorUsesConfiguredProbeBudget(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		timeout      int
		parentBudget time.Duration
		want         time.Duration
	}{
		{"long probe", "health", 60, time.Minute * 2, time.Minute},
		{"short probe", "health", 3, time.Minute * 2, 3 * time.Second},
		{"parent cancellation budget", "health", 60, 10 * time.Second, 10 * time.Second},
		{"wallet default", "balance", 60, time.Minute * 2, 25 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := monitorClient("")
			defer client.CloseIdleConnections()
			client.Transport = monitorRoundTrip(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				require.True(t, ok)
				remaining := time.Until(deadline)
				require.InDelta(t, tc.want.Seconds(), remaining.Seconds(), 1)
				body := `{"success":true,"isValid":true,"unit":"USD","balance":"1"}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})
			p := &Manager{client: client}
			c := DefaultConfig()
			c.BaseURL = "https://example.com"
			c.Probes = []Probe{{ID: "health", URL: c.BaseURL, IntervalMinutes: 5, TimeoutSeconds: tc.timeout, JSONPath: "success", Expected: "true"}}
			ctx, cancel := context.WithTimeout(context.Background(), tc.parentBudget)
			defer cancel()
			require.Equal(t, Healthy, p.observe(ctx, c, Secrets{}, tc.source).Outcome)
		})
	}
}

func TestWalletAdaptersKeepAccountAmounts(t *testing.T) {
	for _, tt := range []struct{ adapter, body, amount, unit string }{
		{"sub2api", `{"isValid":true,"unit":"USD","balance":"-0.1234567890","remaining":999}`, "-0.123456789", "USD"},
		{"zivv", `{"is_available":true,"currency":"USD","balance":"3","total_used":"2"}`, "3", "USD"},
		{"derouter", `{"available":"7.25","balance":"10.25","locked_balance":"3"}`, "7.25", "USD"},
		{"kimi_balance", `{"data":{"available_balance":"8.25"}}`, "8.25", "CNY"},
		{"deepseek_balance", `{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":"-1.5"}]}`, "-1.5", "CNY"},
	} {
		t.Run(tt.adapter, func(t *testing.T) {
			values, err := parseWallet(tt.adapter, []byte(tt.body))
			require.NoError(t, err)
			require.Equal(t, tt.amount, values[0].Available)
			require.Equal(t, tt.unit, values[0].Unit)
		})
	}
	for _, body := range []string{`{"isValid":true,"quota":100}`, `<html>blocked</html>`, `{"isValid":true,"unit":"USD","balance":"NaN"}`} {
		_, err := parseWallet("sub2api", []byte(body))
		require.Error(t, err)
	}
}

func TestMonitorClassifiesFailuresWithoutZeroBalance(t *testing.T) {
	for _, tt := range []struct {
		status  int
		outcome string
	}{{401, Authentication}, {403, Authentication}, {429, RateLimited}, {503, Unreachable}, {200, Unknown}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
			_, _ = w.Write([]byte(`{"quota":123}`))
		}))
		p := &Manager{client: server.Client()}
		config := DefaultConfig()
		config.BaseURL = server.URL
		result := p.observe(context.Background(), config, Secrets{}, "balance")
		require.Equal(t, tt.outcome, result.Outcome)
		require.Empty(t, result.Balances)
		server.Close()
	}
	for _, err := range []error{context.Canceled, &tls.CertificateVerificationError{Err: errors.New("TLS")}, &url.Error{Err: errors.New("invalid header")}} {
		require.False(t, isNetworkOutage(err))
	}
	require.True(t, isNetworkOutage(&net.OpError{Op: "dial", Err: errors.New("refused")}))
	_, err := publicDial(context.Background(), "tcp", "127.0.0.1:1")
	require.Error(t, err)
}

func TestProbeRequiresExplicitJSONAndSameOriginCredentials(t *testing.T) {
	for _, tt := range []struct{ body, outcome string }{{`{"success":true}`, Healthy}, {`{"success":false}`, ProbeUnhealthy}, {`{}`, Unknown}, {`<html>denied</html>`, Unknown}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tt.body)) }))
		p := &Manager{client: server.Client()}
		config := DefaultConfig()
		config.BaseURL = server.URL
		config.Probes = []Probe{{ID: "health", URL: server.URL, IntervalMinutes: 5, TimeoutSeconds: 3, JSONPath: "success", Expected: "true", UseWalletKey: true, SuccessCodes: []int{200}}}
		require.NoError(t, config.Validate())
		require.Equal(t, tt.outcome, p.observe(context.Background(), config, Secrets{}, "health").Outcome)
		config.Probes[0].URL = "https://different.example.com"
		require.Error(t, config.Validate())
		server.Close()
	}
}
