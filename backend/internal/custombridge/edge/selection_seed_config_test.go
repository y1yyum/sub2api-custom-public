package edge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

func TestEdgeSelectionSeedConfiguration(t *testing.T) {
	for _, strict := range []bool{false, true} {
		mode := "shadow"
		if strict {
			mode = "strict"
		}
		t.Run(mode, func(t *testing.T) {
			for name, seed := range map[string]dispatch.SelectionSeed{
				"empty":           {},
				"gateway_missing": {ShardRef: "shard-1"},
				"shard_missing":   {GatewayInstance: "gateway-1"},
				"gateway_space":   {GatewayInstance: "bad gateway", ShardRef: "shard-1"},
				"shard_separator": {GatewayInstance: "gateway-1", ShardRef: "bad/shard"},
				"gateway_long":    {GatewayInstance: strings.Repeat("a", 129), ShardRef: "shard-1"},
				"shard_nul":       {GatewayInstance: "gateway-1", ShardRef: "bad\x00shard"},
			} {
				t.Run(name, func(t *testing.T) {
					cfg := selectionModeConfig(t, "https://core.test", strict)
					selectionSettings(&cfg, strict).SelectionSeed = &seed
					if handler, err := NewHandler(cfg); err == nil {
						handler.Close()
						t.Fatal("invalid selection identity was accepted")
					}
					raw, err := json.Marshal(cfg)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := LoadConfig(configFile(t, raw)); err == nil {
						t.Fatal("file accepted invalid selection identity")
					}
				})
			}

			cfg := selectionModeConfig(t, "https://core.test", strict)
			settings := selectionSettings(&cfg, strict)
			legacy, err := json.Marshal(cfg)
			if err != nil || strings.Contains(string(legacy), "selection_seed") {
				t.Fatal("legacy configuration gained selection identity")
			}
			settings.SelectionSeed = &dispatch.SelectionSeed{GatewayInstance: "gateway-1", ShardRef: "shard-1"}
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadConfig(configFile(t, raw))
			if err != nil || *selectionSettings(&loaded, strict).SelectionSeed != *settings.SelectionSeed {
				t.Fatalf("selection identity did not round-trip: %v", err)
			}
			normalized, err := normalizeConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			settings.SelectionSeed.GatewayInstance = "changed-after-validation"
			if selectionSettings(&normalized, strict).SelectionSeed.GatewayInstance != "gateway-1" {
				t.Fatal("selection identity was not frozen")
			}
			for name, malformed := range map[string]string{
				"duplicate":  strings.Replace(string(raw), `"gateway_instance":"gateway-1"`, `"gateway_instance":"gateway-1","gateway_instance":"gateway-2"`, 1),
				"unknown":    strings.Replace(string(raw), `"shard_ref":"shard-1"`, `"shard_ref":"shard-1","extra":true`, 1),
				"case_alias": strings.Replace(string(raw), `"shard_ref"`, `"Shard_Ref"`, 1),
			} {
				t.Run(name, func(t *testing.T) {
					if _, err := LoadConfig(configFile(t, []byte(malformed))); err == nil {
						t.Fatal("malformed selection identity was accepted")
					}
				})
			}
		})
	}
}

func selectionModeConfig(t *testing.T, origin string, strict bool) Config {
	t.Helper()
	if strict {
		return strictConfig(t, origin)
	}
	return shadowConfig(t, origin)
}

func selectionSettings(cfg *Config, strict bool) *ShadowDispatchConfig {
	if strict {
		return cfg.StrictDispatch
	}
	return cfg.ShadowDispatch
}
