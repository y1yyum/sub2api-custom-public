//go:build unit

package custombridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func telemetryTestErrorRules() []ErrorRule {
	return []ErrorRule{
		{Status: 429, Code: "operator-quota", FailureClass: "quota_exhausted"},
		{Status: 403, Code: "operator-banned", Type: "operator-account", FailureClass: "credential_banned"},
		{Status: 403, Code: "operator-model", FailureClass: "capability_denied"},
	}
}

func TestTelemetryErrorRulesRequireUnambiguousOperatorPolicy(t *testing.T) {
	for name, rules := range map[string][]ErrorRule{
		"status":            {{Status: 401, Code: "x", FailureClass: "credential_banned"}},
		"quota_status":      {{Status: 403, Code: "x", FailureClass: "quota_exhausted"}},
		"ban_status":        {{Status: 429, Code: "x", FailureClass: "credential_banned"}},
		"capability_status": {{Status: 429, Code: "x", FailureClass: "capability_denied"}},
		"empty_class":       {{Status: 403, Code: "x"}},
		"unknown_class":     {{Status: 403, Code: "x", FailureClass: "operator-guess"}},
		"empty_code":        {{Status: 403, FailureClass: "credential_banned"}},
		"blank_code":        {{Status: 403, Code: " ", FailureClass: "credential_banned"}},
		"long_code":         {{Status: 403, Code: strings.Repeat("x", 256), FailureClass: "credential_banned"}},
		"invalid_code":      {{Status: 403, Code: "\xff", FailureClass: "credential_banned"}},
		"control_type":      {{Status: 403, Code: "x", Type: "bad\nvalue", FailureClass: "credential_banned"}},
		"duplicate":         {{Status: 403, Code: "x", FailureClass: "credential_banned"}, {Status: 403, Code: "x", FailureClass: "credential_banned"}},
		"conflict":          {{Status: 403, Code: "x", FailureClass: "credential_banned"}, {Status: 403, Code: "x", FailureClass: "capability_denied"}},
		"wildcard_overlap":  {{Status: 403, Code: "x", FailureClass: "credential_banned"}, {Status: 403, Code: "x", Type: "specific", FailureClass: "capability_denied"}},
		"count":             make([]ErrorRule, 65),
	} {
		t.Run(name, func(t *testing.T) {
			cfg := telemetryTestConfig(t)
			cfg.Targets[0].ErrorRules = rules
			require.Error(t, cfg.Validate())
		})
	}
	cfg := telemetryTestConfig(t)
	cfg.Targets[0].ErrorRules = []ErrorRule{
		{Status: 403, Code: "same", Type: "account", FailureClass: "credential_banned"},
		{Status: 403, Code: "same", Type: "model", FailureClass: "capability_denied"},
	}
	require.NoError(t, cfg.Validate(), "distinct exact types do not overlap")
	for _, variant := range []string{"account_alias", "credential_alias", "credential_hash_alias"} {
		t.Run(variant, func(t *testing.T) {
			cfg := telemetryTestConfig(t)
			cfg.Targets[0].ErrorRules = telemetryTestErrorRules()
			alias := cfg.Targets[0]
			alias.TargetRef, alias.Model = "alias", "alias-model"
			if variant != "account_alias" {
				alias.AccountID++
			}
			if variant == "credential_hash_alias" {
				alias.CredentialRef = "alias-credential"
			}
			alias.ErrorRules = nil
			cfg.Targets = append(cfg.Targets, alias)
			require.Error(t, cfg.Validate())
			cfg.Targets[1].ErrorRules = slices.Clone(cfg.Targets[0].ErrorRules)
			slices.Reverse(cfg.Targets[1].ErrorRules)
			require.NoError(t, cfg.Validate(), "policy equality ignores rule order")
		})
	}
}

func TestTelemetryErrorRulesDigestPreservesDefaultBytes(t *testing.T) {
	cfg := telemetryTestConfig(t)
	legacy := `{"schema_version":1,"core_instance":"core-a","config_revision":1,"targets":[{"target_ref":"target-a","account_id":42,"credential_ref":"cred-a","credential_revision":2,"credential_sha256":"` + cfg.Targets[0].CredentialSHA256 + `","provider_ref":"provider-a","pool_ref":"pool-a","policy_revision":"policy-a","model":"model-a","region":"region-a"}]}`
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.Equal(t, legacy, string(raw), "the disabled policy preserves the prior digest input bytes")
	want := sha256.Sum256([]byte(legacy))
	digest, err := cfg.Digest()
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(want[:]), digest)
	cfg.Targets[0].ErrorRules = []ErrorRule{}
	empty, err := cfg.Digest()
	require.NoError(t, err)
	require.Equal(t, digest, empty)
	cfg.Targets[0].ErrorRules = telemetryTestErrorRules()
	before := slices.Clone(cfg.Targets[0].ErrorRules)
	configured, err := cfg.Digest()
	require.NoError(t, err)
	require.NotEqual(t, digest, configured)
	require.Equal(t, before, cfg.Targets[0].ErrorRules, "Digest must not sort the caller's shared slice")
	slices.Reverse(cfg.Targets[0].ErrorRules)
	reordered, err := cfg.Digest()
	require.NoError(t, err)
	require.Equal(t, configured, reordered)
	cfg.Targets[0].ErrorRules[0].Code += "-other"
	changed, err := cfg.Digest()
	require.NoError(t, err)
	require.NotEqual(t, configured, changed)
	raw, err = json.Marshal(TelemetryEvent{})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "failure_class")
}

func TestTelemetryErrorRulesConfigRejectsAliases(t *testing.T) {
	cfg := telemetryTestConfig(t)
	cfg.Targets[0].ErrorRules = telemetryTestErrorRules()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "telemetry.json")
	t.Setenv("SUB2API_BRIDGE_ENABLED", "true")
	t.Setenv("SUB2API_BRIDGE_TELEMETRY_CONFIG", path)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	loaded, err := LoadTelemetryConfigFromEnv()
	require.NoError(t, err)
	require.Equal(t, cfg, *loaded)
	for _, body := range []string{
		strings.Replace(string(raw), `"code":"operator-quota"`, `"code":"operator-quota","code":"operator-quota"`, 1),
		strings.Replace(string(raw), `"code":"operator-quota"`, `"code":"operator-quota","c\u006fde":"other"`, 1),
		strings.Replace(string(raw), `"error_rules"`, `"Error_Rules"`, 1),
		strings.Replace(string(raw), `"failure_class"`, `"Failure_Class"`, 1),
		strings.Replace(string(raw), `"status":429`, `"status":429,"message":"must-not-escape"`, 1),
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0600))
		_, err := LoadTelemetryConfigFromEnv()
		require.Error(t, err)
		require.NotContains(t, err.Error(), "must-not-escape")
	}
}
