package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func qualitySourceTestTarget() Target {
	return Target{Ref: "target", AccountID: 1, CredentialRef: "credential", CredentialRevision: 1,
		ProviderRef: "provider", PoolRef: "pool", PolicyRevision: "policy", Model: "model", Region: "region",
		BaseWeight: 100, MaxInflight: 10, Enabled: true}
}

func TestQualitySourceBindings(t *testing.T) {
	target := qualitySourceTestTarget()
	other := target
	other.Ref = "other"
	bindings := []QualityBinding{{PlanID: 1, TargetRef: target.Ref, Kind: "char_art"},
		{PlanID: 2, TargetRef: target.Ref, Kind: "char_art"}, {PlanID: 3, TargetRef: target.Ref, Kind: "quiz"}}
	got, err := validateQualityBindings(bindings, []Target{target})
	require.NoError(t, err)
	require.Len(t, got, 3, "distinct plans may supply different questions for the same target and kind")
	for _, tc := range []struct {
		name     string
		bindings []QualityBinding
		targets  []Target
	}{
		{"empty-bindings", nil, []Target{target}},
		{"duplicate-plan", append(bindings, bindings[0]), []Target{target}},
		{"probe", []QualityBinding{{PlanID: 1, TargetRef: target.Ref, Kind: "probe"}}, []Target{target}},
		{"missing-target", bindings, nil},
		{"duplicate-target", bindings, []Target{target, target}},
		{"zero-plan", []QualityBinding{{TargetRef: target.Ref, Kind: "quiz"}}, []Target{target}},
		{"plan-to-two-targets", []QualityBinding{{PlanID: 1, TargetRef: target.Ref, Kind: "quiz"}, {PlanID: 1, TargetRef: other.Ref, Kind: "quiz"}}, []Target{target, other}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateQualityBindings(tc.bindings, tc.targets)
			require.Error(t, err)
		})
	}
	target.Enabled = false
	_, err = validateQualityBindings(bindings, []Target{target})
	require.Error(t, err)
	_, err = NewQualitySource(context.Background(), nil)
	require.Error(t, err)
}

func TestQualitySourcePlanFingerprint(t *testing.T) {
	a := qualityPlan{ID: 1, AccountID: 2, PolicyVersion: 3, Model: "model", Kind: "char_art", Prompt: "draw", Enabled: true,
		CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0)}
	b := a
	b.UpdatedAt = b.UpdatedAt.Add(time.Second)
	require.True(t, sameQualityPlan(a, b), "the old runner advances updated_at after every run")
	for _, mutate := range []func(*qualityPlan){
		func(p *qualityPlan) { p.AccountID++ }, func(p *qualityPlan) { p.PolicyVersion++ },
		func(p *qualityPlan) { p.Model = "other" }, func(p *qualityPlan) { p.Kind = "quiz" },
		func(p *qualityPlan) { p.Prompt = "other" }, func(p *qualityPlan) { p.Enabled = false },
	} {
		b = a
		mutate(&b)
		require.False(t, sameQualityPlan(a, b))
	}
}
