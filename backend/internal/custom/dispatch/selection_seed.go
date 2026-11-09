package dispatch

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
)

type SelectionSeed struct {
	GatewayInstance string `json:"gateway_instance"`
	ShardRef        string `json:"shard_ref"`
}

func (s SelectionSeed) Validate() error {
	if !validRef(s.GatewayInstance) || !validRef(s.ShardRef) {
		return errors.New("invalid dispatch selection seed")
	}
	return nil
}

func NewSelectorWithSeed(core string, seed SelectionSeed) *Selector {
	selector := NewSelector(core)
	selector.seed = &seed
	selector.seedTotals = make(map[string]int64)
	return selector
}

func (s *Selector) selectionPhase(route Route, period int64) int64 {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		"dispatch-selection-phase-v1", s.core, s.seed.GatewayInstance, s.seed.ShardRef,
		route.PoolRef, route.PolicyRevision, route.Model, route.Region,
	}, "\x00")))
	return int64(binary.BigEndian.Uint64(digest[:8]) % uint64(min(period, int64(MaxCandidates))))
}

// candidates 已按 Ref 排序；路由来自有界快照，不缓存请求中的任意 model。
func (s *Selector) seedWeights(candidates []Candidate, weights, totals map[string]int64) {
	groups := make(map[Route][]Candidate)
	for _, candidate := range candidates {
		if !candidate.Enabled || (candidate.State != Healthy && candidate.State != Degraded) ||
			candidate.EffectiveWeight == 0 || candidate.Active >= candidate.MaxInflight {
			continue
		}
		route := Route{candidate.PoolRef, candidate.PolicyRevision, candidate.Model, candidate.Region}
		groups[route] = append(groups[route], candidate)
	}
	for route, group := range groups {
		var total, divisor int64
		needsSeed := false
		for _, candidate := range group {
			total += candidate.EffectiveWeight
			divisor = weightGCD(divisor, candidate.EffectiveWeight)
			if _, exists := weights[candidate.Ref]; !exists {
				needsSeed = true
			}
		}
		if !needsSeed {
			continue
		}
		phase := s.selectionPhase(route, total/divisor)
		currents := make([]int64, len(group))
		// 只从真实周期的前至多 MaxCandidates 个相位取起点，不按总权重预热。
		for range phase {
			selected := 0
			for i, candidate := range group {
				currents[i] += candidate.EffectiveWeight
				if currents[i] > currents[selected] {
					selected = i
				}
			}
			currents[selected] -= total
		}
		for i, candidate := range group {
			if _, exists := weights[candidate.Ref]; exists {
				continue
			}
			weights[candidate.Ref] = currents[i]
			totals[candidate.Ref] = total
		}
	}
}

type selectionWeight struct {
	candidate *Candidate
	weight    int64
}

func (s *Selector) selectSeededLocked(eligible []selectionWeight, total int64) *Candidate {
	var selected *Candidate
	for _, entry := range eligible {
		ref := entry.candidate.Ref
		current := s.weights[ref]
		// 仅在单位改变时缩放并约束旧累计；正常 SWRR 的累计可能超过 total。
		if previous := s.seedTotals[ref]; previous != total {
			current = current * total / previous
			current = max(-total, min(total, current))
		}
		s.weights[ref] = current + entry.weight
		s.seedTotals[ref] = total
		if selected == nil || s.weights[ref] > s.weights[selected.Ref] {
			selected = entry.candidate
		}
	}
	return selected
}

func weightGCD(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
