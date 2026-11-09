//go:build unit

package custombridge

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func telemetryTestConfig(t *testing.T) TelemetryConfig {
	t.Helper()
	digest, err := TelemetryCredentialSHA256(map[string]any{"api_key": "synthetic-test-key", "base_url": "http://127.0.0.1:12345"})
	require.NoError(t, err)
	return TelemetryConfig{SchemaVersion: 1, CoreInstance: "core-a", ConfigRevision: 1, Targets: []TelemetryTarget{{
		TargetRef: "target-a", AccountID: 42, CredentialRef: "cred-a", CredentialRevision: 2, CredentialSHA256: digest,
		ProviderRef: "provider-a", PoolRef: "pool-a", PolicyRevision: "policy-a", Model: "model-a", Region: "region-a",
	}}}
}

func TestTelemetryConfigurationIsExplicitAndBounded(t *testing.T) {
	t.Setenv("SUB2API_BRIDGE_ENABLED", "false")
	t.Setenv("SUB2API_BRIDGE_TELEMETRY_CONFIG", "does-not-exist")
	cfg, err := LoadTelemetryConfigFromEnv()
	require.NoError(t, err)
	require.Nil(t, cfg)
	t.Setenv("SUB2API_BRIDGE_ENABLED", "true")
	t.Setenv("SUB2API_BRIDGE_TELEMETRY_CONFIG", "")
	cfg, err = LoadTelemetryConfigFromEnv()
	require.NoError(t, err)
	require.Nil(t, cfg)
	path := filepath.Join(t.TempDir(), "telemetry.json")
	t.Setenv("SUB2API_BRIDGE_TELEMETRY_CONFIG", path)
	_, err = LoadTelemetryConfigFromEnv()
	require.ErrorContains(t, err, "unavailable")
	raw, err := json.Marshal(telemetryTestConfig(t))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	cfg, err = LoadTelemetryConfigFromEnv()
	require.NoError(t, err)
	require.Equal(t, telemetryTestConfig(t), *cfg)
	for name, body := range map[string]string{
		"duplicate":  strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"case_alias": strings.Replace(string(raw), `"schema_version"`, `"Schema_Version"`, 1),
		"unknown":    strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1,"secret":"must-not-escape"`, 1),
		"trailing":   string(raw) + ` {}`,
		"oversize":   strings.Repeat(" ", MaxTelemetryConfigBytes+1),
		"deep":       strings.Repeat("[", 20) + strings.Repeat("]", 20),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(path, []byte(body), 0600))
			_, err := LoadTelemetryConfigFromEnv()
			require.Error(t, err)
			require.NotContains(t, err.Error(), "must-not-escape")
		})
	}
}

func TestTelemetryConfigurationPinsUnambiguousIdentity(t *testing.T) {
	for name, change := range map[string]func(*TelemetryConfig){
		"revision": func(c *TelemetryConfig) { c.ConfigRevision = 0 },
		"empty":    func(c *TelemetryConfig) { c.Targets = nil },
		"many":     func(c *TelemetryConfig) { c.Targets = make([]TelemetryTarget, 1001) },
		"duplicate_ref": func(c *TelemetryConfig) {
			target := c.Targets[0]
			target.Model = "other"
			c.Targets = append(c.Targets, target)
		},
		"ambiguous_route": func(c *TelemetryConfig) {
			target := c.Targets[0]
			target.TargetRef = "other"
			target.Region = "other"
			c.Targets = append(c.Targets, target)
		},
		"account_credential": func(c *TelemetryConfig) {
			target := c.Targets[0]
			target.TargetRef = "other"
			target.Model = "other"
			target.CredentialRevision++
			c.Targets = append(c.Targets, target)
		},
		"credential_provider": func(c *TelemetryConfig) {
			target := c.Targets[0]
			target.TargetRef = "other"
			target.AccountID++
			target.ProviderRef = "other"
			c.Targets = append(c.Targets, target)
		},
		"hash":   func(c *TelemetryConfig) { c.Targets[0].CredentialSHA256 = strings.Repeat("g", 64) },
		"model":  func(c *TelemetryConfig) { c.Targets[0].Model = "bad\nmodel" },
		"region": func(c *TelemetryConfig) { c.Targets[0].Region = "bad region" },
	} {
		t.Run(name, func(t *testing.T) { cfg := telemetryTestConfig(t); change(&cfg); require.Error(t, cfg.Validate()) })
	}
	cfg := telemetryTestConfig(t)
	target := cfg.Targets[0]
	target.TargetRef = "another"
	target.Model = "another"
	cfg.Targets = append(cfg.Targets, target)
	digest, err := cfg.Digest()
	require.NoError(t, err)
	cfg.Targets[0], cfg.Targets[1] = cfg.Targets[1], cfg.Targets[0]
	reordered, err := cfg.Digest()
	require.NoError(t, err)
	require.Equal(t, digest, reordered)
	cfg.ConfigRevision++
	changed, err := cfg.Digest()
	require.NoError(t, err)
	require.NotEqual(t, digest, changed)
	firstTTL, err := cfg.ActivationDigest(900)
	require.NoError(t, err)
	secondTTL, err := cfg.ActivationDigest(1800)
	require.NoError(t, err)
	require.NotEqual(t, firstTTL, secondTTL)
	for _, ttl := range []int{-1, 0, 86401, int(^uint(0) >> 1)} {
		_, err := cfg.ActivationDigest(ttl)
		require.Error(t, err)
	}
}

func TestTelemetryCredentialDigestCanonicalObjectKeys(t *testing.T) {
	first, err := TelemetryCredentialSHA256(map[string]any{"b": map[string]any{"z": true, "a": "<synthetic>"}, "a": float64(1)})
	require.NoError(t, err)
	second, err := TelemetryCredentialSHA256(map[string]any{"a": float64(1), "b": map[string]any{"a": "<synthetic>", "z": true}})
	require.NoError(t, err)
	require.Equal(t, first, second)
	_, err = TelemetryCredentialSHA256(nil)
	require.Error(t, err)
	_, err = TelemetryCredentialSHA256(map[string]any{"unsupported": math.Inf(1)})
	require.Error(t, err)
}
