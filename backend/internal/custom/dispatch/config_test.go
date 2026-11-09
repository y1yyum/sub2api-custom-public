package dispatch

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDispatchTelemetryConfigDefaultsAndBounds(t *testing.T) {
	base := Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, SnapshotFile: "snapshot.json", Telemetry: &TelemetryConfig{}}
	cfg, err := normalizeConfig(base)
	require.NoError(t, err)
	require.Equal(t, 1000, cfg.PollIntervalMilliseconds)
	require.Equal(t, 1800, cfg.Telemetry.SlotTTLSeconds)
	require.Equal(t, 5, cfg.Telemetry.LoadMaxAgeSeconds)
	require.Equal(t, 10000, cfg.Telemetry.MaxObservations)
	for name, change := range map[string]func(*Config){
		"ttl":       func(c *Config) { c.Telemetry.SlotTTLSeconds = 86401 },
		"freshness": func(c *Config) { c.Telemetry.LoadMaxAgeSeconds = 11 },
		"backlog":   func(c *Config) { c.Telemetry.MaxObservations = 100001 },
		"poll":      func(c *Config) { c.PollIntervalMilliseconds = 5000 },
	} {
		t.Run(name, func(t *testing.T) {
			c := cfg
			value := *cfg.Telemetry
			c.Telemetry = &value
			change(&c)
			_, err := normalizeConfig(c)
			require.Error(t, err)
		})
	}
}

func TestDispatchConfigDefaultsAndProbeBudgetBoundary(t *testing.T) {
	cfg, err := normalizeConfig(Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, SnapshotFile: "snapshot.json", Targets: []Target{testTarget("a", 1)}})
	require.NoError(t, err)
	require.Nil(t, cfg.Probe)
	require.Equal(t, 30, cfg.SnapshotTTLSeconds)
	require.Equal(t, 5000, cfg.PollIntervalMilliseconds)
	base := ProbeConfig{CoreOrigin: "http://127.0.0.1:8080", AllowLoopbackHTTP: true, Audience: "core", CredentialsFile: "credentials.json", ReceiptSource: "source"}
	cfg.Probe = &base
	cfg, err = normalizeConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, 10, cfg.Probe.RequestTimeoutSeconds)
	require.Equal(t, 1, cfg.Probe.GlobalConcurrent)
	require.Equal(t, 50, cfg.Probe.DailyRequests)
	for name, change := range map[string]func(*ProbeConfig){
		"unbounded_concurrency": func(p *ProbeConfig) { p.GlobalConcurrent = 17 },
		"unbounded_daily":       func(p *ProbeConfig) { p.DailyRequests = 100001 },
		"short_lease":           func(p *ProbeConfig) { p.LeaseSeconds = 17 },
		"short_execution":       func(p *ProbeConfig) { p.RequestTimeoutSeconds = 1 },
		"external_http":         func(p *ProbeConfig) { p.CoreOrigin = "http://example.com" },
		"hostname_loopback":     func(p *ProbeConfig) { p.CoreOrigin = "http://localhost:8080" },
		"query":                 func(p *ProbeConfig) { p.CoreOrigin = "https://example.com?" },
		"credentials_url":       func(p *ProbeConfig) { p.CoreOrigin = "https://user:placeholder@example.com" },
		"path":                  func(p *ProbeConfig) { p.CoreOrigin = "https://example.com/arbitrary" },
		"missing_source":        func(p *ProbeConfig) { p.ReceiptSource = "" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *cfg.Probe
			change(&changed)
			test := cfg
			test.Probe = &changed
			_, err := normalizeConfig(test)
			require.Error(t, err)
		})
	}
}

func TestDispatchConfigJSONAndCredentialErrorsAreRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, SnapshotFile: "snapshot.json", Targets: []Target{testTarget("a", 1)}}
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0600))
	_, err = LoadConfig(path)
	require.NoError(t, err)
	for _, raw := range []string{`{"version":1,"version":1}`, `{"version":"sensitive-placeholder"}`, `{"private":"sensitive-placeholder"}`, `null`} {
		require.NoError(t, os.WriteFile(path, []byte(raw), 0600))
		_, err := LoadConfig(path)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "sensitive-placeholder")
	}
	credentials := ProbeCredentials{KID: "key", SecretBase64: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("placeholder-", 4))), CoreAPIKey: "synthetic-service-token", InternalKeyID: 1, GroupID: 2}
	body, err = json.Marshal(credentials)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0600))
	got, err := LoadProbeCredentials(path, "core")
	require.NoError(t, err)
	require.Equal(t, credentials, got)
	credentials.SecretBase64 = "sensitive-placeholder"
	body, err = json.Marshal(credentials)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0600))
	_, err = LoadProbeCredentials(path, "core")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sensitive-placeholder")
}

func TestDispatchObservationImportBindsCoreAndSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"core_instance":"core-a","observations":[{"event_id":"event"}]}`), 0600))
	got, err := LoadObservations(path, "core-a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	_, err = LoadObservations(path, "core-b")
	require.Error(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"core_instance":"core-a","observations":[]}`), 0600))
	_, err = LoadObservations(path, "core-a")
	require.Error(t, err)
}
