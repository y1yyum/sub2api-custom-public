package dispatch

import "time"

type MismatchReason string

const (
	MismatchExpectedAccountAbsent MismatchReason = "expected_account_absent"
	MismatchRoute                 MismatchReason = "route_mismatch"
	MismatchDisabled              MismatchReason = "disabled"
	MismatchHealth                MismatchReason = "health"
	MismatchZeroWeight            MismatchReason = "zero_weight"
	MismatchCapacity              MismatchReason = "capacity"
	MismatchSWRRChoice            MismatchReason = "swrr_choice"
)

// SelectShadow 在同一快照和锁内只选号一次，不改变原决策的失败语义。
func (s *Selector) SelectShadow(route Route, expectedAccountID int64, now time.Time) (Decision, MismatchReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	decision := s.selectLocked(route, now)
	if decision.AccountID <= 0 || decision.AccountID == expectedAccountID {
		return decision, ""
	}

	var present, routed, enabled, healthy, weighted bool
	for _, candidate := range s.current.Load().snapshot.Candidates {
		if candidate.AccountID != expectedAccountID {
			continue
		}
		present = true
		if candidate.PoolRef != route.PoolRef || candidate.PolicyRevision != route.PolicyRevision ||
			candidate.Model != route.Model || candidate.Region != route.Region {
			continue
		}
		routed = true
		if !candidate.Enabled {
			continue
		}
		enabled = true
		// 被硬失效覆盖层剔除的目标按健康原因归类，不新增统计字段。
		if candidate.State != Healthy && candidate.State != Degraded || s.invalidated[candidate.Ref] {
			continue
		}
		healthy = true
		if candidate.EffectiveWeight == 0 {
			continue
		}
		weighted = true
		if candidate.Active < candidate.MaxInflight {
			return decision, MismatchSWRRChoice
		}
	}
	switch {
	case !present:
		return decision, MismatchExpectedAccountAbsent
	case !routed:
		return decision, MismatchRoute
	case !enabled:
		return decision, MismatchDisabled
	case !healthy:
		return decision, MismatchHealth
	case !weighted:
		return decision, MismatchZeroWeight
	default:
		return decision, MismatchCapacity
	}
}
