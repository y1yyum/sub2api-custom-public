package dispatch

import (
	"errors"
	"sync"
	"time"
)

// Permit 是运行时授权配置；快照只能缩小候选，不能扩大授权或并发上限。
type Permit struct {
	CandidateRef       string `json:"candidate_ref"`
	AccountID          int64  `json:"account_id"`
	CredentialRef      string `json:"credential_ref"`
	CredentialRevision int64  `json:"credential_revision"`
	ProviderRef        string `json:"provider_ref"`
	Model              string `json:"model"`
	MaxInflight        int    `json:"max_inflight"`
}

type PermitSet struct {
	byRef map[string]Permit
}

func NewPermitSet(permits []Permit) (*PermitSet, error) {
	if len(permits) == 0 || len(permits) > MaxCandidates {
		return nil, errors.New("invalid dispatch permit count")
	}
	set := &PermitSet{byRef: make(map[string]Permit, len(permits))}
	limits := make(map[int64]int)
	for _, permit := range permits {
		if !validRef(permit.CandidateRef) || permit.AccountID <= 0 || !validRef(permit.CredentialRef) || permit.CredentialRevision < 1 ||
			!validRef(permit.ProviderRef) || !validModel(permit.Model) || permit.MaxInflight < 1 || permit.MaxInflight > 100000 {
			return nil, errors.New("invalid dispatch permit")
		}
		if _, exists := set.byRef[permit.CandidateRef]; exists {
			return nil, errors.New("duplicate dispatch permit")
		}
		if limit, exists := limits[permit.AccountID]; exists && limit != permit.MaxInflight {
			return nil, errors.New("inconsistent dispatch account limit")
		}
		limits[permit.AccountID] = permit.MaxInflight
		set.byRef[permit.CandidateRef] = permit
	}
	return set, nil
}

func (p Permit) matches(candidate Candidate) bool {
	return p.AccountID == candidate.AccountID && p.CredentialRef == candidate.CredentialRef &&
		p.CredentialRevision == candidate.CredentialRevision && p.ProviderRef == candidate.ProviderRef && p.Model == candidate.Model
}

// Acquire 的占位覆盖本 Selector 的所有 grant、模型和候选别名，不是跨进程硬并发。
// release 可重复调用；快照更新和候选移除不会释放仍在途的账号占位。
func (s *Selector) Acquire(route Route, permits *PermitSet, now time.Time) (Decision, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	loaded := s.current.Load()
	if loaded == nil {
		return Decision{Reason: "snapshot_unavailable"}, nil
	}
	snapshot := &loaded.snapshot
	decision := Decision{Revision: snapshot.Revision, Reason: "no_eligible_route"}
	if !now.Before(snapshot.ExpiresAt) || now.Before(snapshot.GeneratedAt.Add(-5*time.Second)) {
		decision.Reason = "snapshot_expired"
		return decision, nil
	}
	if permits == nil {
		return decision, nil
	}
	externalActive := make(map[int64]int)
	accountLimits := make(map[int64]int)
	for _, candidate := range snapshot.Candidates {
		externalActive[candidate.AccountID] = max(externalActive[candidate.AccountID], candidate.Active)
		if previous, exists := accountLimits[candidate.AccountID]; !exists || candidate.MaxInflight < previous {
			accountLimits[candidate.AccountID] = candidate.MaxInflight
		}
	}
	var selected *Candidate
	var total int64
	var eligible []selectionWeight
	for i := range snapshot.Candidates {
		candidate := &snapshot.Candidates[i]
		permit, allowed := permits.byRef[candidate.Ref]
		if !allowed || !permit.matches(*candidate) || candidate.PoolRef != route.PoolRef || candidate.PolicyRevision != route.PolicyRevision ||
			candidate.Model != route.Model || candidate.Region != route.Region || !candidate.Enabled ||
			(candidate.State != Healthy && candidate.State != Degraded) || candidate.EffectiveWeight == 0 || s.invalidated[candidate.Ref] {
			continue
		}
		limit := min(permit.MaxInflight, accountLimits[candidate.AccountID])
		active := externalActive[candidate.AccountID] + s.active[candidate.AccountID]
		if active >= limit {
			continue
		}
		weight := max(int64(1), candidate.EffectiveWeight*int64(max(5, 100*(limit-active)/limit))/100)
		total += weight
		if s.seed != nil {
			eligible = append(eligible, selectionWeight{candidate, weight})
			continue
		}
		s.weights[candidate.Ref] += weight
		if selected == nil || s.weights[candidate.Ref] > s.weights[selected.Ref] {
			selected = candidate
		}
	}
	if s.seed != nil {
		selected = s.selectSeededLocked(eligible, total)
	}
	if selected == nil {
		return decision, nil
	}
	s.weights[selected.Ref] -= total
	accountID := selected.AccountID
	s.active[accountID]++
	decision.AccountID, decision.CandidateRef, decision.Reason = accountID, selected.Ref, "selected"
	var once sync.Once
	return decision, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.active[accountID] <= 1 {
				delete(s.active, accountID)
			} else {
				s.active[accountID]--
			}
		})
	}
}
