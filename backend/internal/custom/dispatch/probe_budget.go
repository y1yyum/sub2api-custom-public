package dispatch

import (
	"errors"
	"sort"
)

const (
	MaxProbeBudgetMicrousd int64 = 1_000_000_000_000_000
	MaxProbePriceMicrousd  int64 = 1_000_000_000_000
)

type ProbeBudgetConfig struct {
	Currency              string            `json:"currency"`
	DailyMicrousd         int64             `json:"daily_microusd"`
	ProviderDailyMicrousd int64             `json:"provider_daily_microusd"`
	ActivationEvidenceRef string            `json:"activation_evidence_ref"`
	Prices                []ProbePriceBound `json:"prices"`
}

// 价格依据是操作者确认的整次请求成本上界，并非已核验的供应商报价。
type ProbePriceBound struct {
	ProviderRef string `json:"provider_ref"`
	Model       string `json:"model"`
	Revision    string `json:"revision"`
	EvidenceRef string `json:"evidence_ref"`
	MaxMicrousd int64  `json:"max_microusd"`
}

func normalizeProbeBudgetConfig(budget *ProbeBudgetConfig, probe *ProbeConfig, targets []Target) (*ProbeBudgetConfig, error) {
	if budget == nil {
		return nil, nil
	}
	if probe == nil {
		return nil, errors.New("dispatch probe budget requires probes")
	}
	if budget.Currency != "USD" || !validRef(budget.ActivationEvidenceRef) ||
		budget.DailyMicrousd < 1 || budget.DailyMicrousd > MaxProbeBudgetMicrousd ||
		budget.ProviderDailyMicrousd < 1 || budget.ProviderDailyMicrousd > MaxProbeBudgetMicrousd ||
		len(budget.Prices) > MaxCandidates {
		return nil, errors.New("invalid dispatch probe budget limits or activation evidence")
	}
	prices := make(map[[2]string]bool, len(budget.Prices))
	for _, price := range budget.Prices {
		if !validRef(price.ProviderRef) || !validModel(price.Model) || !validRef(price.Revision) || !validRef(price.EvidenceRef) ||
			price.MaxMicrousd < 1 || price.MaxMicrousd > MaxProbePriceMicrousd ||
			price.MaxMicrousd > budget.DailyMicrousd || price.MaxMicrousd > budget.ProviderDailyMicrousd {
			return nil, errors.New("invalid dispatch probe budget price bound")
		}
		key := [2]string{price.ProviderRef, price.Model}
		if prices[key] {
			return nil, errors.New("duplicate dispatch probe budget price")
		}
		prices[key] = true
	}
	for _, target := range targets {
		if target.Enabled && !prices[[2]string{target.ProviderRef, target.Model}] {
			return nil, errors.New("dispatch probe budget requires a price for every enabled target")
		}
	}
	normalized := *budget
	normalized.Prices = append([]ProbePriceBound{}, budget.Prices...)
	sort.Slice(normalized.Prices, func(i, j int) bool {
		a, b := normalized.Prices[i], normalized.Prices[j]
		if a.ProviderRef != b.ProviderRef {
			return a.ProviderRef < b.ProviderRef
		}
		return a.Model < b.Model
	})
	return &normalized, nil
}
