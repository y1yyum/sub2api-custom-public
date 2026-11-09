package edge

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testBearer  = "edge-fixture-bearer"
	testCoreKey = "core-fixture-key"
)

var testSecret = strings.Repeat("x", 32)

func testConfig(origin string) Config {
	digest := sha256.Sum256([]byte(testBearer))
	return Config{
		Version: 1, CoreOrigin: origin, Audience: "core-fixture", AllowLoopbackHTTP: true,
		SigningKeys: []KeyConfig{{ID: "edge-fixture", SecretBase64: base64.StdEncoding.EncodeToString([]byte(testSecret)), InternalKeyID: 101, GroupID: 7}},
		Grants:      []Grant{{ID: "grant-fixture-v1", BearerSHA256: hex.EncodeToString(digest[:]), SigningKeyID: "edge-fixture", AccountID: 42, CoreKeyID: 101, GroupID: 7, CoreAPIKey: testCoreKey, PoolRef: "pool-fixture", PolicyRevision: "policy-1", PayerRef: "payer-fixture", ActorRef: "actor-fixture"}},
	}
}

func configFile(t *testing.T, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "edge.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEdgeConfigLoadAndDefaults(t *testing.T) {
	raw, err := json.Marshal(testConfig("http://127.0.0.1:8080"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configFile(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8081" || cfg.MaxBodyBytes != 1<<20 || cfg.SignatureTTLSeconds != 30 || cfg.RequestTimeoutSeconds != 300 || cfg.DialTimeoutSeconds != 5 || cfg.ResponseHeaderTimeoutSeconds != 30 {
		t.Fatal("配置默认值不正确")
	}
}

func TestEdgeShadowConfiguration(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"empty_path":     func(c *Config) { c.ShadowDispatch.SnapshotFile = "  " },
		"nul_path":       func(c *Config) { c.ShadowDispatch.SnapshotFile = "bad\x00path" },
		"empty_core":     func(c *Config) { c.ShadowDispatch.CoreInstance = "" },
		"invalid_core":   func(c *Config) { c.ShadowDispatch.CoreInstance = "bad instance" },
		"negative_poll":  func(c *Config) { c.ShadowDispatch.PollIntervalMilliseconds = -1 },
		"small_poll":     func(c *Config) { c.ShadowDispatch.PollIntervalMilliseconds = 9 },
		"large_poll":     func(c *Config) { c.ShadowDispatch.PollIntervalMilliseconds = 30001 },
		"missing_region": func(c *Config) { c.Grants[0].Region = "" },
		"invalid_region": func(c *Config) { c.Grants[0].Region = "bad region" },
		"another_grant_missing_region": func(c *Config) {
			g := c.Grants[0]
			g.ID, g.Region = "another-grant", ""
			digest := sha256.Sum256([]byte("another-fixture-bearer"))
			g.BearerSHA256 = hex.EncodeToString(digest[:])
			c.Grants = append(c.Grants, g)
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := shadowConfig(t, "https://core.test")
			change(&cfg)
			if handler, err := NewHandler(cfg); err == nil {
				handler.Close()
				t.Fatal("接受了无效影子配置")
			}
		})
	}
	cfg := shadowConfig(t, "https://core.test")
	cfg.ShadowDispatch.PollIntervalMilliseconds = 0
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(configFile(t, raw))
	if err != nil || loaded.ShadowDispatch == nil || loaded.ShadowDispatch.PollIntervalMilliseconds != 1000 || loaded.Grants[0].Region != "region-a" {
		t.Fatalf("影子配置未解析或默认刷新间隔错误: %v", err)
	}
	for name, raw := range map[string]string{
		"duplicate":      `{"shadow_dispatch":{"core_instance":"one","core_instance":"two"}}`,
		"unknown":        strings.TrimSuffix(string(raw), "}") + `,"shadow_live":true}`,
		"nested_unknown": strings.Replace(string(raw), `"snapshot_file":`, `"strict":true,"snapshot_file":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(configFile(t, []byte(raw))); err == nil {
				t.Fatal("接受了重复字段或未知模式")
			}
		})
	}
}

func TestEdgeRequestBudgetConfiguration(t *testing.T) {
	for _, test := range []struct {
		seconds int
		valid   bool
	}{
		{-1, false}, {1, false}, {29, false}, {30, true}, {300, true}, {900, true}, {901, false},
	} {
		cfg := testConfig("http://127.0.0.1:8080")
		cfg.SignatureTTLSeconds, cfg.RequestTimeoutSeconds = 30, test.seconds
		handler, err := NewHandler(cfg)
		if handler != nil {
			handler.Close()
		}
		if (err == nil) != test.valid {
			t.Errorf("request_timeout_seconds=%d valid=%t err=%v", test.seconds, test.valid, err)
		}
	}
}

func TestEdgeConfigRejectsAmbiguityAndRedactsValues(t *testing.T) {
	raw, err := json.Marshal(testConfig("http://127.0.0.1:8080"))
	if err != nil {
		t.Fatal(err)
	}
	for name, invalid := range map[string]string{
		"duplicate":        `{"version":1,"version":2}`,
		"nested_duplicate": `{"signing_keys":[{"id":"one","id":"two"}]}`,
		"case_alias":       `{"version":1,"Version":2}`,
		"trailing":         string(raw) + `{}`,
		"unknown":          strings.TrimSuffix(string(raw), "}") + `,"` + testSecret + `":true}`,
		"invalid_type":     `{"version":"` + testSecret + `"}`,
		"array":            `[]`, "null": `null`, "large": strings.Repeat(" ", maxConfigBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfig(configFile(t, []byte(invalid)))
			if err == nil {
				t.Fatal("错误配置被接受")
			}
			for _, secret := range []string{testSecret, testBearer, testCoreKey} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("错误泄漏配置值")
				}
			}
		})
	}
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("接受了不存在的配置")
	}
}

func TestEdgeConfigRejectsGrantScopeAndUnsafeOrigins(t *testing.T) {
	cases := map[string]func(*Config){
		"key_scope":         func(c *Config) { c.Grants[0].CoreKeyID++ },
		"group_scope":       func(c *Config) { c.Grants[0].GroupID++ },
		"unknown_signer":    func(c *Config) { c.Grants[0].SigningKeyID = "absent" },
		"duplicate_grant":   func(c *Config) { c.Grants = append(c.Grants, c.Grants[0]) },
		"duplicate_token":   func(c *Config) { g := c.Grants[0]; g.ID = "other-grant"; c.Grants = append(c.Grants, g) },
		"duplicate_key":     func(c *Config) { c.SigningKeys = append(c.SigningKeys, c.SigningKeys[0]) },
		"missing_actor":     func(c *Config) { c.Grants[0].ActorRef = "" },
		"invalid_ref":       func(c *Config) { c.Grants[0].PoolRef = "not/a/core/id" },
		"invalid_account":   func(c *Config) { c.Grants[0].AccountID = 0 },
		"invalid_core_auth": func(c *Config) { c.Grants[0].CoreAPIKey = "line\nbreak" },
		"short_secret":      func(c *Config) { c.SigningKeys[0].SecretBase64 = base64.StdEncoding.EncodeToString([]byte("short")) },
		"invalid_secret":    func(c *Config) { c.SigningKeys[0].SecretBase64 = "%%%" },
		"no_grants":         func(c *Config) { c.Grants = nil },
		"long_ttl":          func(c *Config) { c.SignatureTTLSeconds = 3600 },
		"invalid_listen":    func(c *Config) { c.Listen = "bad-listen" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig("http://127.0.0.1:8080")
			change(&cfg)
			if _, err := NewHandler(cfg); err == nil {
				t.Fatal("越界配置被接受")
			}
		})
	}
	for _, origin := range []string{"http://localhost:8080", "http://192.0.2.1:8080", "http://127.0.0.1.evil.test", "https://user:secret@core.test", "https://core.test/path", "https://core.test?", "https://core.test?x=1", "https://core.test/#fragment", "https://core.test:bad", "ftp://core.test", "https://"} {
		t.Run(origin, func(t *testing.T) {
			if _, err := NewHandler(testConfig(origin)); err == nil {
				t.Fatal("不安全 Core 源地址被接受")
			}
		})
	}
	for _, origin := range []string{"https://core.test", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		handler, err := NewHandler(testConfig(origin))
		if err != nil {
			t.Fatal(err)
		}
		handler.Close()
	}
	cfg := testConfig("http://127.0.0.1:8080")
	cfg.AllowLoopbackHTTP = false
	if _, err := NewHandler(cfg); err == nil {
		t.Fatal("未显式允许的 HTTP 被接受")
	}
}
