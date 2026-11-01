package antigravity

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A concrete, per-client in-memory transport: it never delegates to any default
// transport, opens a socket, or resolves the endpoint's hostname.
type privacyOwnedRoundTripper func(*http.Request) (*http.Response, error)

func (f privacyOwnedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type privacyOwnedResponseBody struct {
	io.Reader
	closed bool
}

func (b *privacyOwnedResponseBody) Close() error {
	b.closed = true
	return nil
}

func TestClientSetUserSettingsOwnedTransport(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		response  string
		wantError bool
	}{
		{"success", http.StatusOK, `{"userSettings":{}}`, false},
		{"http_error", http.StatusForbidden, `{"error":"synthetic denial"}`, true},
		{"invalid_json", http.StatusOK, `not-json`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			body := &privacyOwnedResponseBody{Reader: strings.NewReader(tc.response)}
			transport := privacyOwnedRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", req.Method)
				}
				if req.URL.String() != privacyBaseURL+"/v1internal:setUserSettings" {
					t.Errorf("unexpected endpoint: %s", req.URL)
				}
				if req.Host != "daily-cloudcode-pa.googleapis.com" {
					t.Errorf("unexpected Host: %s", req.Host)
				}
				if req.Header.Get("Authorization") != "Bearer synthetic-privacy-test" {
					t.Error("unexpected synthetic authorization")
				}
				if req.Header.Get("Content-Type") != "application/json" {
					t.Error("missing JSON content type")
				}
				if req.Header.Get("Accept") != "*/*" {
					t.Error("unexpected Accept header")
				}
				if req.Header.Get("User-Agent") == "" {
					t.Error("missing user agent")
				}
				if req.Header.Get("X-Goog-Api-Client") != "gl-node/22.21.1" {
					t.Error("unexpected API client header")
				}
				payload, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read in-memory request: %v", err)
				}
				if err := req.Body.Close(); err != nil {
					t.Fatalf("close in-memory request: %v", err)
				}
				if string(payload) != `{"user_settings":{}}` {
					t.Errorf("unexpected privacy payload: %s", payload)
				}
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: body, Request: req}, nil
			})
			// Do not call NewClient: that constructor intentionally builds the
			// production transport. This transport sends no network requests.
			client := &Client{httpClient: &http.Client{Transport: transport}}
			result, err := client.SetUserSettings(context.Background(), "synthetic-privacy-test")
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError = %t", err, tc.wantError)
			}
			if !tc.wantError && (result == nil || !result.IsSuccess()) {
				t.Fatalf("unexpected privacy result: %+v", result)
			}
			if calls != 1 {
				t.Errorf("transport calls = %d, want 1", calls)
			}
			if !body.closed {
				t.Error("response body was not closed")
			}
		})
	}
}
