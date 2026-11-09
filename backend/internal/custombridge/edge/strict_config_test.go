package edge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

func strictConfig(t *testing.T, origin string) Config {
	t.Helper()
	cfg := shadowConfig(t, origin)
	cfg.StrictDispatch, cfg.ShadowDispatch = cfg.ShadowDispatch, nil
	cfg.Grants[0].StrictTargets = []dispatch.Permit{{
		CandidateRef: "candidate-a", AccountID: 7, CredentialRef: "credential-a", CredentialRevision: 1,
		ProviderRef: "provider-fixture", Model: "fixture-model", MaxInflight: 1,
	}}
	return cfg
}

func addStrictGrant(cfg *Config) {
	g := cfg.Grants[0]
	g.ID = "second-grant"
	digest := sha256.Sum256([]byte("second-fixture-bearer"))
	g.BearerSHA256 = hex.EncodeToString(digest[:])
	g.StrictTargets = append([]dispatch.Permit(nil), g.StrictTargets...)
	cfg.Grants = append(cfg.Grants, g)
}

func TestEdgeStrictConfiguration(t *testing.T) {
	defaultHandler, err := NewHandler(testConfig("https://core.test"))
	if err != nil {
		t.Fatal(err)
	}
	defer defaultHandler.Close()
	if defaultHandler.strict != nil || defaultHandler.StrictStats() != (StrictStats{}) {
		t.Fatal("strict 未保持默认关闭")
	}
	for name, change := range map[string]func(*Config){
		"both_modes":             func(c *Config) { clone := *c.StrictDispatch; c.ShadowDispatch = &clone },
		"region_required":        func(c *Config) { c.Grants[0].Region = "" },
		"permits_required":       func(c *Config) { c.Grants[0].StrictTargets = nil },
		"permits_without_strict": func(c *Config) { c.StrictDispatch = nil },
		"permits_shadow":         func(c *Config) { c.ShadowDispatch, c.StrictDispatch = c.StrictDispatch, nil },
		"zero_cap":               func(c *Config) { c.Grants[0].StrictTargets[0].MaxInflight = 0 },
		"bad_identity":           func(c *Config) { c.Grants[0].StrictTargets[0].CredentialRevision = 0 },
		"duplicate_candidate": func(c *Config) {
			c.Grants[0].StrictTargets = append(c.Grants[0].StrictTargets, c.Grants[0].StrictTargets[0])
		},
		"limit_across_grants": func(c *Config) { addStrictGrant(c); c.Grants[1].StrictTargets[0].MaxInflight++ },
		"limit_across_aliases": func(c *Config) {
			addStrictGrant(c)
			c.Grants[1].StrictTargets[0].CandidateRef = "alias"
			c.Grants[1].StrictTargets[0].MaxInflight++
		},
		"account_collision":    func(c *Config) { addStrictGrant(c); c.Grants[1].StrictTargets[0].AccountID++ },
		"credential_collision": func(c *Config) { addStrictGrant(c); c.Grants[1].StrictTargets[0].CredentialRef = "other" },
		"revision_collision":   func(c *Config) { addStrictGrant(c); c.Grants[1].StrictTargets[0].CredentialRevision++ },
		"provider_collision":   func(c *Config) { addStrictGrant(c); c.Grants[1].StrictTargets[0].ProviderRef = "other" },
		"model_collision":      func(c *Config) { addStrictGrant(c); c.Grants[1].StrictTargets[0].Model = "other" },
		"region_collision":     func(c *Config) { addStrictGrant(c); c.Grants[1].Region = "other" },
		"pool_collision":       func(c *Config) { addStrictGrant(c); c.Grants[1].PoolRef = "other" },
		"policy_collision":     func(c *Config) { addStrictGrant(c); c.Grants[1].PolicyRevision = "other" },
		"missing_file":         func(c *Config) { c.StrictDispatch.SnapshotFile = "" },
		"bad_core":             func(c *Config) { c.StrictDispatch.CoreInstance = "invalid instance" },
		"small_poll":           func(c *Config) { c.StrictDispatch.PollIntervalMilliseconds = 9 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := strictConfig(t, "https://core.test")
			change(&cfg)
			if handler, err := NewHandler(cfg); err == nil {
				handler.Close()
				t.Fatal("接受无效 strict 授权")
			}
		})
	}
	cfg := strictConfig(t, "https://core.test")
	addStrictGrant(&cfg)
	cfg.StrictDispatch.PollIntervalMilliseconds = 0
	normalized, err := normalizeConfig(cfg)
	if err != nil || normalized.StrictDispatch.PollIntervalMilliseconds != 1000 {
		t.Fatalf("合法共享授权失败: %v", err)
	}
	cfg.Grants[0].StrictTargets[0].AccountID = 999
	if normalized.Grants[0].StrictTargets[0].AccountID != 7 {
		t.Fatal("未冻结 grant permit 切片")
	}
}

func TestEdgeStrictConfigJSONRejectsAliasesDuplicatesAndUnknown(t *testing.T) {
	raw, err := json.Marshal(strictConfig(t, "https://core.test"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(configFile(t, raw)); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"top_alias":         strings.Replace(string(raw), "strict_dispatch", "Strict_dispatch", 1),
		"nested_alias":      strings.Replace(string(raw), "candidate_ref", "Candidate_ref", 1),
		"unknown":           strings.Replace(string(raw), `"candidate_ref":`, `"unknown":1,"candidate_ref":`, 1),
		"duplicate":         strings.Replace(string(raw), `"candidate_ref":`, `"candidate_ref":"forged","candidate_ref":`, 1),
		"escaped_duplicate": strings.Replace(string(raw), `"candidate_ref":`, `"candidate\u005fref":"forged","candidate_ref":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(configFile(t, []byte(body))); err == nil {
				t.Fatal("接受配置别名、重复或未知字段")
			}
		})
	}
}
