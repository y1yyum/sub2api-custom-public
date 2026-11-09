package dispatch

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/quality"
	"github.com/stretchr/testify/require"
)

func qualityTestTarget() Target {
	return Target{Ref: "a", AccountID: 1, CredentialRef: "cred", CredentialRevision: 1, ProviderRef: "provider", PoolRef: "pool", PolicyRevision: "policy", Model: "model", Region: "region", Enabled: true, BaseWeight: 100, MaxInflight: 10}
}

func qualityTestTrend(target Target, score int) []qualityTrendSample {
	var samples []qualityTrendSample
	for question := 0; question < 2; question++ {
		for i := 0; i < 5; i++ {
			samples = append(samples, qualityTrendSample{Target: target, PlanID: int64(question + 1), ResultID: int64(question*5 + i + 1), FinishedAt: time.Unix(int64(i+1), 0), Evaluation: qualityEvaluation{
				Result: quality.Result{Kind: quality.KindCharArt, QuestionRef: fmt.Sprint(question), Score: score, Confidence: 90}, Reference: quality.Reference{Median: 100, MAD: 0, CalibratedCoverage: true},
			}})
		}
	}
	return samples
}

func TestDispatchQualityTrendRequiresIndependentEvidence(t *testing.T) {
	target := qualityTestTarget()
	for _, tc := range []struct {
		name   string
		change func([]qualityTrendSample) []qualityTrendSample
		want   int
	}{
		{"persistent_two_questions", func(s []qualityTrendSample) []qualityTrendSample { return s }, 850},
		{"one_question", func(s []qualityTrendSample) []qualityTrendSample { return s[:5] }, 1000},
		{"few_samples", func(s []qualityTrendSample) []qualityTrendSample { return append(s[:4], s[5:9]...) }, 1000},
		{"one_plan", func(s []qualityTrendSample) []qualityTrendSample {
			for i := range s {
				s[i].PlanID = 1
			}
			return s
		}, 1000},
		{"uncalibrated", func(s []qualityTrendSample) []qualityTrendSample {
			for i := range s {
				s[i].Evaluation.Reference.CalibratedCoverage = false
			}
			return s
		}, 1000},
		{"newest_unknown", func(s []qualityTrendSample) []qualityTrendSample { s[4].Evaluation.Result.Confidence = 0; return s }, 1000},
		{"one_question_bad", func(s []qualityTrendSample) []qualityTrendSample {
			for i := 5; i < 10; i++ {
				s[i].Evaluation.Result.Score = 100
			}
			return s
		}, 1000},
		{"wrong_region", func(s []qualityTrendSample) []qualityTrendSample {
			for i := range s {
				s[i].Target.Region = "other"
			}
			return s
		}, 1000},
		{"wrong_model", func(s []qualityTrendSample) []qualityTrendSample {
			for i := range s {
				s[i].Target.Model = "other"
			}
			return s
		}, 1000},
		{"old_credential", func(s []qualityTrendSample) []qualityTrendSample {
			for i := range s {
				s[i].Target.CredentialRevision = 2
			}
			return s
		}, 1000},
		{"wrong_provider", func(s []qualityTrendSample) []qualityTrendSample {
			for i := range s {
				s[i].Target.ProviderRef = "other"
			}
			return s
		}, 1000},
		{"separate_quiz", func(s []qualityTrendSample) []qualityTrendSample {
			for i := range s {
				s[i].Evaluation.Result.Kind = quality.KindQuiz
			}
			return s
		}, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reports := qualityReports([]Target{target}, tc.change(qualityTestTrend(target, 0)))
			require.Len(t, reports, 2)
			require.Equal(t, tc.want, reports[0].FactorMilli)
		})
	}
}

func TestDispatchQualityRecoveryUsesLatestIndependentSamples(t *testing.T) {
	target := qualityTestTarget()
	bad := qualityTestTrend(target, 0)
	good := qualityTestTrend(target, 100)
	for i := range good {
		good[i].ResultID += 10
		good[i].FinishedAt = good[i].FinishedAt.Add(time.Minute)
	}
	reports := qualityReports([]Target{target}, append(bad, good...))
	require.Equal(t, 1000, reports[0].FactorMilli)
	require.Equal(t, "stable", reports[0].Reason)
	alias := target
	alias.Ref = "alias"
	alias.PoolRef = "another-pool"
	require.Equal(t, 850, qualityReports([]Target{alias}, bad)[0].FactorMilli, "candidate aliases share only the exact quality domain")
}

func TestDispatchQualityDriftEvidenceMustSpanPlans(t *testing.T) {
	target := qualityTestTarget()
	bad := qualityTestTrend(target, 0)
	for i := range bad {
		bad[i].PlanID = 1
	}
	good := qualityTestTrend(target, 100)[:5]
	for i := range good {
		good[i].PlanID = 2
		good[i].Evaluation.Result.QuestionRef = "third"
	}
	reports := qualityReports([]Target{target}, append(bad, good...))
	require.Equal(t, 3, reports[0].Questions)
	require.Equal(t, 2, reports[0].Plans)
	require.Equal(t, 2, reports[0].DriftQuestions)
	require.Equal(t, 1, reports[0].DriftPlans)
	require.Equal(t, 1000, reports[0].FactorMilli, "stable samples from another plan cannot validate a one-plan fault")
	for i := range bad {
		if i%5 >= 3 {
			bad[i].PlanID = 2
			bad[i].Evaluation.Result.Score = 100
		}
	}
	reports = qualityReports([]Target{target}, bad)
	require.Equal(t, 1000, reports[0].FactorMilli, "only low scores supply independent fault evidence")
}

func TestDispatchQualityWeightsCannotOverrideHealth(t *testing.T) {
	target := qualityTestTarget()
	for _, state := range []State{Healthy, Degraded, Tripped, Probing} {
		for _, base := range []int64{1, 2, 3, 10, 100, 10000} {
			h := InitialHealth(target, time.Now())
			h.State = state
			target.BaseWeight = base
			candidate := h.Candidate(target)
			before := candidate
			snapshot := Snapshot{Candidates: []Candidate{candidate}}
			applyQualityWeights(&snapshot, []QualityReport{{TargetRef: "a", FactorMilli: 850}})
			after := snapshot.Candidates[0]
			require.Equal(t, before.Target, after.Target)
			require.Equal(t, before.State, after.State)
			require.Equal(t, before.Reason, after.Reason)
			require.LessOrEqual(t, after.EffectiveWeight, before.EffectiveWeight)
			require.GreaterOrEqual(t, after.EffectiveWeight*1000, before.EffectiveWeight*850)
		}
	}
}
