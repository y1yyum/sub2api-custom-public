package dispatch

import (
	"errors"
	"maps"
)

// MaxInvalidations 与每个 Core 的 hold 定义上限一致；覆盖层按 hold_id 合并，不会超过该数。
const MaxInvalidations = MaxHealthHolds

// Invalidation 是一次 dispatch 自有 hold 激活的独立传播。FloorRevision 是检测时同一库已提交的
// 该 Core 最新快照 revision：同 lineage 中 revision 大于 floor 的快照必然在 hold 提交后生成，
// 已由快照自身体现该 hold；不大于 floor 的快照可能早于 hold，必须继续剔除匹配目标。
type Invalidation struct {
	HoldID         string          `json:"hold_id"`
	HoldRevision   int64           `json:"hold_revision"`
	Class          string          `json:"class"`
	Scope          HealthHoldScope `json:"scope"`
	PublishLineage string          `json:"publish_lineage"`
	FloorRevision  int64           `json:"floor_revision"`
	Sequence       int64           `json:"sequence"`
}

// UnmarshalJSON 区分有效的零水位与缺失或 null；后两者不能作为快照已吸收失效项的依据。
func (i *Invalidation) UnmarshalJSON(body []byte) error {
	type wire Invalidation
	entry := wire{FloorRevision: -1}
	if err := decodeJSON(body, &entry); err != nil {
		return err
	}
	if entry.FloorRevision < 0 {
		return errors.New("invalid dispatch invalidation floor_revision")
	}
	*i = Invalidation(entry)
	return nil
}

// HealthHoldID 返回 hold 的稳定标识；失效项必须携带由 core、类别与范围导出的同一标识。
func HealthHoldID(core, class string, scope HealthHoldScope) string {
	return healthHoldID(core, class, scope)
}

func (i Invalidation) valid(core string) bool {
	return i.HoldRevision >= 1 && i.Scope.valid(i.Class) && i.HoldID == healthHoldID(core, i.Class, i.Scope) &&
		validRef(i.PublishLineage) && i.FloorRevision >= 0 && i.Sequence >= 1
}

// supersedes 只允许收紧：同 lineage 取更高 hold 版本或更高 floor；跨 lineage 只接受服务端当前 lineage。
func (i Invalidation) supersedes(current Invalidation, serverLineage string) bool {
	if i.PublishLineage != current.PublishLineage {
		return i.PublishLineage == serverLineage
	}
	return i.HoldRevision > current.HoldRevision || i.FloorRevision > current.FloorRevision
}

// ApplyInvalidations 把已认证的失效合并进覆盖层，只会剔除候选，从不恢复候选。
// 覆盖项仅在快照已体现对应 hold 时撤除（见 refreshInvalidatedLocked），返回新增或收紧的项数。
func (s *Selector) ApplyInvalidations(serverLineage string, entries []Invalidation) (int, error) {
	if !validRef(serverLineage) || len(entries) > MaxInvalidations {
		return 0, errors.New("invalid dispatch invalidation update")
	}
	for _, entry := range entries {
		if !entry.valid(s.core) {
			return 0, errors.New("invalid dispatch invalidation entry")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.Load() != nil && s.lineage == "" {
		return 0, errors.New("dispatch invalidations require epoch snapshots")
	}
	merged := maps.Clone(s.invalidations)
	if merged == nil {
		merged = make(map[string]Invalidation, len(entries))
	}
	applied := 0
	for _, entry := range entries {
		if current, exists := merged[entry.HoldID]; exists {
			if !entry.supersedes(current, serverLineage) {
				continue
			}
			if entry.PublishLineage == current.PublishLineage {
				entry.FloorRevision = max(entry.FloorRevision, current.FloorRevision)
			}
		}
		merged[entry.HoldID] = entry
		applied++
	}
	if len(merged) > MaxInvalidations {
		return 0, errors.New("dispatch invalidation limit exceeded")
	}
	s.invalidations, s.invalidationLineage = merged, serverLineage
	s.refreshInvalidatedLocked()
	return applied, nil
}

// refreshInvalidatedLocked 撤除当前快照已体现的覆盖项，并重算被剔除的候选。
// 同 lineage：快照 revision 大于 floor 即已体现。跨 lineage：快照处于服务端当前 lineage 时，
// 旧 lineage 的失效已被新 lineage 的首份快照吸收；其余跨 lineage 情形保守剔除。
func (s *Selector) refreshInvalidatedLocked() {
	s.invalidated = nil
	loaded := s.current.Load()
	if loaded == nil || len(s.invalidations) == 0 {
		return
	}
	for id, entry := range s.invalidations {
		sameLineage := entry.PublishLineage == s.lineage
		if sameLineage && loaded.snapshot.Revision > entry.FloorRevision ||
			!sameLineage && s.lineage == s.invalidationLineage && entry.PublishLineage != s.invalidationLineage {
			delete(s.invalidations, id)
		}
	}
	for i := range loaded.snapshot.Candidates {
		candidate := &loaded.snapshot.Candidates[i]
		for _, entry := range s.invalidations {
			if entry.Scope.matches(candidate.Target) {
				if s.invalidated == nil {
					s.invalidated = make(map[string]bool)
				}
				s.invalidated[candidate.Ref] = true
				break
			}
		}
	}
}

// InvalidationCounts 返回覆盖层项数与当前快照中被剔除的候选数，仅用于固定聚合统计。
func (s *Selector) InvalidationCounts() (entries, candidates int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.invalidations), len(s.invalidated)
}
