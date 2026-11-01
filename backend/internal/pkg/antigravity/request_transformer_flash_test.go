package antigravity

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildGenerationConfigFlashThinkingBudget(t *testing.T) {
	models := []struct {
		name   string
		capped bool
	}{
		{"gemini-2.5-flash", true},
		{"gemini-2.5-flash-lite", true},
		{"gemini-2.5-flash-thinking", true},
		{"GEMINI-2.5-FLASH", true},
		{"gemini-3-flash", true},
		{"gemini-3.1-flash-image-preview", true},
		{"gemini-3.6-flash-high", true},
		{"gemini-3.7-flash-tiered", true},
		{"gemini-3.8-flash", true},
		{"GeMiNi-3.8-FlAsH-Preview", true},
		{"gemini-3.8-pro", false},
		{"claude-sonnet-4-5", false},
		{"custom-flash", false},
		{"gemini-3.8-flashlight", false},
		{"gemini-3.8-pro-flash", false},
		{"models/gemini-3.8-flash", false},
		// Preserve the existing 2.5 substring guard for legacy aliases.
		{"legacy/gemini-2.5-flash", true},
		{"gemini-2.5-flashlight", true},
	}
	budgets := []int{-2, -1, 0, 1, 24575, 24576, 24577, 32768}
	for _, model := range models {
		t.Run(model.name, func(t *testing.T) {
			for _, mode := range []string{"enabled", "adaptive"} {
				for _, budget := range budgets {
					req := &ClaudeRequest{Model: model.name, MaxTokens: 64000,
						Thinking: &ThinkingConfig{Type: mode, BudgetTokens: budget}}
					got := buildGenerationConfig(req)
					want := budget
					if want <= 0 {
						want = -1
					}
					if model.capped && want > 24576 {
						want = 24576
					}
					if got.ThinkingConfig == nil || got.ThinkingConfig.ThinkingBudget != want || !got.ThinkingConfig.IncludeThoughts {
						t.Fatalf("mode=%s budget=%d: got %+v, want thinking budget %d", mode, budget, got.ThinkingConfig, want)
					}
					if got.MaxOutputTokens != 64000 || req.Thinking.BudgetTokens != budget {
						t.Fatalf("mode=%s budget=%d: changed output limit or input request", mode, budget)
					}
				}
			}
		})
	}
}

func TestBuildGenerationConfigFlashThinkingCompatibility(t *testing.T) {
	for _, thinking := range []*ThinkingConfig{nil, {Type: "disabled", BudgetTokens: 32768}, {Type: "enabled"}, {Type: "enabled", BudgetTokens: -1}} {
		req := &ClaudeRequest{Model: "gemini-3.8-flash", MaxTokens: 100, Thinking: thinking}
		got := buildGenerationConfig(req)
		if thinking == nil || thinking.Type == "disabled" {
			if got.ThinkingConfig != nil {
				t.Fatalf("disabled thinking became enabled: %+v", got)
			}
		} else if got.ThinkingConfig == nil || got.ThinkingConfig.ThinkingBudget != -1 {
			t.Fatalf("dynamic thinking budget changed: %+v", got.ThinkingConfig)
		}
		if got.MaxOutputTokens != 100 {
			t.Fatalf("non-positive budget raised max_tokens: %d", got.MaxOutputTokens)
		}
	}
	for _, tc := range []struct{ budget, maxTokens, wantMax int }{
		{1, 100, 100}, {24576, 24576, 25576}, {32768, 100, 25576}, {32768, 30000, 30000},
	} {
		got := buildGenerationConfig(&ClaudeRequest{Model: "gemini-3.8-flash", MaxTokens: tc.maxTokens,
			Thinking: &ThinkingConfig{Type: "enabled", BudgetTokens: tc.budget}})
		wantBudget := tc.budget
		if wantBudget > 24576 {
			wantBudget = 24576
		}
		if got.ThinkingConfig == nil || got.ThinkingConfig.ThinkingBudget != wantBudget {
			t.Fatalf("%+v: thinking config=%+v", tc, got.ThinkingConfig)
		}
		if got.MaxOutputTokens != tc.wantMax {
			t.Fatalf("%+v: max_tokens=%d", tc, got.MaxOutputTokens)
		}
	}
	got := buildGenerationConfig(&ClaudeRequest{Model: "claude-opus-4-6", Thinking: &ThinkingConfig{Type: "adaptive", BudgetTokens: 1}})
	if got.ThinkingConfig.ThinkingBudget != 24576 {
		t.Fatal("changed Opus adaptive override")
	}
}

func TestTransformClaudeToGeminiFlashThinkingUsesMappedModel(t *testing.T) {
	for _, tc := range []struct {
		requested, mapped string
		want              int
	}{
		{"custom-alias", "gemini-3.8-flash", 24576},
		{"gemini-3.8-flash", "gemini-3.8-pro", 32768},
		{"gemini-3.8-flash", "", 24576},
	} {
		t.Run(tc.requested+"/"+tc.mapped, func(t *testing.T) {
			req := &ClaudeRequest{Model: tc.requested, MaxTokens: 64000,
				Messages: []ClaudeMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
				Thinking: &ThinkingConfig{Type: "enabled", BudgetTokens: 32768}}
			before, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := TransformClaudeToGeminiWithOptions(req, "synthetic-project", tc.mapped, TransformOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var got V1InternalRequest
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if got.Request.GenerationConfig == nil || got.Request.GenerationConfig.ThinkingConfig == nil || got.Request.GenerationConfig.ThinkingConfig.ThinkingBudget != tc.want {
				t.Fatalf("unexpected serialized config: %s", body)
			}
			after, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("transform mutated caller request")
			}
		})
	}
}
