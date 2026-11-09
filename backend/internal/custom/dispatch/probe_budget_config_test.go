package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func probeBudgetTestConfig() Config {
	return Config{
		Version: 1, CoreInstance: "core", ConfigRevision: 1, SnapshotFile: "snapshot.json",
		Targets: []Target{testTarget("a", 1)},
		Probe:   &ProbeConfig{CoreOrigin: "https://core.example", Audience: "core", CredentialsFile: "credentials.json", ReceiptSource: "source"},
		ProbeBudget: &ProbeBudgetConfig{
			Currency: "USD", DailyMicrousd: 1_000_000, ProviderDailyMicrousd: 500_000, ActivationEvidenceRef: "drain-20261107",
			Prices: []ProbePriceBound{{ProviderRef: "provider", Model: "gpt-fixture", Revision: "bound-v1", EvidenceRef: "operator-bound-v1", MaxMicrousd: 1000}},
		},
	}
}

func TestDispatchProbeBudgetConfigDisabledAndRequiresProbe(t *testing.T) {
	for _, probe := range []*ProbeConfig{nil, {}} {
		budget, err := normalizeProbeBudgetConfig(nil, probe, []Target{testTarget("a", 1)})
		require.NoError(t, err)
		require.Nil(t, budget)
	}
	cfg := probeBudgetTestConfig()
	cfg.ProbeBudget = nil
	got, err := normalizeConfig(cfg)
	require.NoError(t, err)
	require.Nil(t, got.ProbeBudget, "prices must never be invented")
	cfg = probeBudgetTestConfig()
	cfg.Probe = nil
	_, err = normalizeConfig(cfg)
	require.ErrorContains(t, err, "budget requires probes")
}

func TestDispatchProbeBudgetConfigBoundsAndReferences(t *testing.T) {
	for _, value := range []int64{1, MaxProbePriceMicrousd, MaxProbeBudgetMicrousd} {
		cfg := probeBudgetTestConfig()
		cfg.ProbeBudget.DailyMicrousd = value
		cfg.ProbeBudget.ProviderDailyMicrousd = value
		cfg.ProbeBudget.Prices[0].MaxMicrousd = min(value, MaxProbePriceMicrousd)
		_, err := normalizeConfig(cfg)
		require.NoError(t, err)
	}
	for name, change := range map[string]func(*ProbeBudgetConfig){
		"empty_currency":      func(b *ProbeBudgetConfig) { b.Currency = "" },
		"lowercase_currency":  func(b *ProbeBudgetConfig) { b.Currency = "usd" },
		"other_currency":      func(b *ProbeBudgetConfig) { b.Currency = "EUR" },
		"padded_currency":     func(b *ProbeBudgetConfig) { b.Currency = " USD" },
		"daily_zero":          func(b *ProbeBudgetConfig) { b.DailyMicrousd = 0 },
		"daily_negative":      func(b *ProbeBudgetConfig) { b.DailyMicrousd = -1 },
		"daily_large":         func(b *ProbeBudgetConfig) { b.DailyMicrousd = MaxProbeBudgetMicrousd + 1 },
		"provider_zero":       func(b *ProbeBudgetConfig) { b.ProviderDailyMicrousd = 0 },
		"provider_negative":   func(b *ProbeBudgetConfig) { b.ProviderDailyMicrousd = -1 },
		"provider_large":      func(b *ProbeBudgetConfig) { b.ProviderDailyMicrousd = MaxProbeBudgetMicrousd + 1 },
		"missing_activation":  func(b *ProbeBudgetConfig) { b.ActivationEvidenceRef = "" },
		"invalid_activation":  func(b *ProbeBudgetConfig) { b.ActivationEvidenceRef = "https://example.com/drain" },
		"long_activation":     func(b *ProbeBudgetConfig) { b.ActivationEvidenceRef = strings.Repeat("a", 129) },
		"missing_provider":    func(b *ProbeBudgetConfig) { b.Prices[0].ProviderRef = "" },
		"invalid_provider":    func(b *ProbeBudgetConfig) { b.Prices[0].ProviderRef = "provider/one" },
		"long_provider":       func(b *ProbeBudgetConfig) { b.Prices[0].ProviderRef = strings.Repeat("p", 129) },
		"missing_model":       func(b *ProbeBudgetConfig) { b.Prices[0].Model = "" },
		"invalid_model":       func(b *ProbeBudgetConfig) { b.Prices[0].Model = "model one" },
		"unicode_model":       func(b *ProbeBudgetConfig) { b.Prices[0].Model = "模型" },
		"long_model":          func(b *ProbeBudgetConfig) { b.Prices[0].Model = strings.Repeat("m", 256) },
		"missing_revision":    func(b *ProbeBudgetConfig) { b.Prices[0].Revision = "" },
		"invalid_revision":    func(b *ProbeBudgetConfig) { b.Prices[0].Revision = "revision one" },
		"long_revision":       func(b *ProbeBudgetConfig) { b.Prices[0].Revision = strings.Repeat("r", 129) },
		"missing_evidence":    func(b *ProbeBudgetConfig) { b.Prices[0].EvidenceRef = "" },
		"invalid_evidence":    func(b *ProbeBudgetConfig) { b.Prices[0].EvidenceRef = "https://example.com/quote" },
		"long_evidence":       func(b *ProbeBudgetConfig) { b.Prices[0].EvidenceRef = strings.Repeat("e", 129) },
		"price_zero":          func(b *ProbeBudgetConfig) { b.Prices[0].MaxMicrousd = 0 },
		"price_negative":      func(b *ProbeBudgetConfig) { b.Prices[0].MaxMicrousd = -1 },
		"price_exceeds_daily": func(b *ProbeBudgetConfig) { b.DailyMicrousd = b.Prices[0].MaxMicrousd - 1 },
		"price_large": func(b *ProbeBudgetConfig) {
			b.DailyMicrousd, b.ProviderDailyMicrousd = MaxProbeBudgetMicrousd, MaxProbeBudgetMicrousd
			b.Prices[0].MaxMicrousd = MaxProbePriceMicrousd + 1
		},
		"price_exceeds_provider": func(b *ProbeBudgetConfig) {
			b.ProviderDailyMicrousd = b.Prices[0].MaxMicrousd - 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := probeBudgetTestConfig()
			change(cfg.ProbeBudget)
			_, err := normalizeConfig(cfg)
			require.Error(t, err)
		})
	}
	// Provider 日上限可以高于 global；每次认领仍须同时满足两个上限。
	cfg := probeBudgetTestConfig()
	cfg.ProbeBudget.ProviderDailyMicrousd = MaxProbeBudgetMicrousd
	_, err := normalizeConfig(cfg)
	require.NoError(t, err)
}

func TestDispatchProbeBudgetConfigRequiresExactPrices(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"nil_prices":     func(c *Config) { c.ProbeBudget.Prices = nil },
		"empty_prices":   func(c *Config) { c.ProbeBudget.Prices = []ProbePriceBound{} },
		"other_provider": func(c *Config) { c.ProbeBudget.Prices[0].ProviderRef = "other" },
		"other_model":    func(c *Config) { c.ProbeBudget.Prices[0].Model = "other" },
		"provider_case":  func(c *Config) { c.ProbeBudget.Prices[0].ProviderRef = "Provider" },
		"model_case":     func(c *Config) { c.ProbeBudget.Prices[0].Model = "GPT-fixture" },
		"second_target_unpriced": func(c *Config) {
			target := testTarget("b", 2)
			target.Model = "other"
			c.Targets = append(c.Targets, target)
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := probeBudgetTestConfig()
			change(&cfg)
			_, err := normalizeConfig(cfg)
			require.ErrorContains(t, err, "price for every enabled target")
		})
	}
	for _, enabled := range []bool{false, true} {
		cfg := probeBudgetTestConfig()
		cfg.Targets[0].Enabled = enabled
		cfg.ProbeBudget.Prices = append(cfg.ProbeBudget.Prices, cfg.ProbeBudget.Prices[0])
		cfg.ProbeBudget.Prices[1].Revision, cfg.ProbeBudget.Prices[1].EvidenceRef = "bound-v2", "evidence-v2"
		_, err := normalizeConfig(cfg)
		require.ErrorContains(t, err, "duplicate", "provider/model is unique regardless of revision or enabled targets")
	}
	for _, empty := range [][]ProbePriceBound{nil, {}} {
		cfg := probeBudgetTestConfig()
		cfg.Targets[0].Enabled = false
		cfg.ProbeBudget.Prices = empty
		got, err := normalizeConfig(cfg)
		require.NoError(t, err)
		require.Empty(t, got.ProbeBudget.Prices)
		require.NotNil(t, got.ProbeBudget.Prices, "empty price lists have one canonical JSON representation")
	}
	cfg := probeBudgetTestConfig()
	cfg.Targets = append(cfg.Targets, testTarget("alias", 1))
	_, err := normalizeConfig(cfg)
	require.NoError(t, err, "aliases use the same exact provider/model price")
	for _, count := range []int{1000, 1001} {
		cfg := probeBudgetTestConfig()
		for i := 1; i < count; i++ {
			price := cfg.ProbeBudget.Prices[0]
			price.Model = fmt.Sprintf("extra-%04d", i)
			cfg.ProbeBudget.Prices = append(cfg.ProbeBudget.Prices, price)
		}
		_, err := normalizeConfig(cfg)
		if count == 1000 {
			require.NoError(t, err, "unused valid prices are allowed")
		} else {
			require.Error(t, err)
		}
	}
}

func TestDispatchProbeBudgetConfigJSONIntegersAndStrictFields(t *testing.T) {
	cfg := probeBudgetTestConfig()
	cfg.ProbeBudget.DailyMicrousd = MaxProbeBudgetMicrousd
	cfg.ProbeBudget.ProviderDailyMicrousd = MaxProbeBudgetMicrousd - 1
	cfg.ProbeBudget.Prices[0].MaxMicrousd = MaxProbePriceMicrousd
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	raw := string(body)
	require.Contains(t, raw, `"daily_microusd":1000000000000000`)
	require.Contains(t, raw, `"provider_daily_microusd":999999999999999`)
	require.Contains(t, raw, `"max_microusd":1000000000000`)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, body, 0600))
	got, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, cfg.ProbeBudget, got.ProbeBudget)
	for name, changes := range map[string][2]string{
		"duplicate_budget":      {`"probe_budget":{`, `"probe_budget":null,"probe_budget":{`},
		"duplicate_limit":       {`"daily_microusd":1000000000000000`, `"daily_microusd":1,"daily_microusd":1000000000000000`},
		"duplicate_price_field": {`"max_microusd":1000000000000`, `"max_microusd":1,"max_microusd":1000000000000`},
		"budget_case":           {`"probe_budget":`, `"Probe_Budget":`},
		"limit_case_alias":      {`"currency":"USD"`, `"Currency":"USD","currency":"USD"`},
		"price_case":            {`"evidence_ref":`, `"Evidence_Ref":`},
		"unknown_budget_field":  {`"currency":"USD"`, `"currency":"USD","unverified":true`},
		"unknown_price_field":   {`"max_microusd":`, `"actual_cost":1,"max_microusd":`},
		"float_daily":           {`"daily_microusd":1000000000000000`, `"daily_microusd":1.5`},
		"float_provider":        {`"provider_daily_microusd":999999999999999`, `"provider_daily_microusd":1.0`},
		"float_price":           {`"max_microusd":1000000000000`, `"max_microusd":1000.0`},
		"exponent_price":        {`"max_microusd":1000000000000`, `"max_microusd":1e3`},
		"quoted_price":          {`"max_microusd":1000000000000`, `"max_microusd":"1000"`},
		"overflow_daily":        {`"daily_microusd":1000000000000000`, `"daily_microusd":9223372036854775808`},
		"negative_price":        {`"max_microusd":1000000000000`, `"max_microusd":-1`},
		"null_price":            {`"max_microusd":1000000000000`, `"max_microusd":null`},
	} {
		t.Run(name, func(t *testing.T) {
			changed := strings.Replace(raw, changes[0], changes[1], 1)
			require.NotEqual(t, raw, changed)
			require.NoError(t, os.WriteFile(path, []byte(changed), 0600))
			_, err := LoadConfig(path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "operator-bound-v1")
		})
	}
}
