package dispatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	SnapshotVersion  = 1
	MaxCandidates    = 1000
	MaxSnapshotBytes = 2 << 20
)

type State string

const (
	Healthy  State = "healthy"
	Degraded State = "degraded"
	Tripped  State = "tripped"
	Probing  State = "probing"
)

type Target struct {
	Ref                string `json:"ref"`
	AccountID          int64  `json:"account_id"`
	CredentialRef      string `json:"credential_ref"`
	CredentialRevision int64  `json:"credential_revision"`
	ProviderRef        string `json:"provider_ref"`
	QuotaRef           string `json:"quota_ref,omitempty"`
	PoolRef            string `json:"pool_ref"`
	PolicyRevision     string `json:"policy_revision"`
	Model              string `json:"model"`
	Region             string `json:"region"`
	BaseWeight         int64  `json:"base_weight"`
	MaxInflight        int    `json:"max_inflight"`
	Enabled            bool   `json:"enabled"`
}

type Candidate struct {
	Target
	State           State  `json:"state"`
	EffectiveWeight int64  `json:"effective_weight"`
	Active          int    `json:"active"`
	Reason          string `json:"reason"`
}

type Snapshot struct {
	Version        int         `json:"version"`
	CoreInstance   string      `json:"core_instance"`
	Revision       int64       `json:"revision"`
	GeneratedAt    time.Time   `json:"generated_at"`
	ExpiresAt      time.Time   `json:"expires_at"`
	LoadObservedAt time.Time   `json:"load_observed_at,omitzero"`
	LoadExpiresAt  time.Time   `json:"load_expires_at,omitzero"`
	Candidates     []Candidate `json:"candidates"`
}

type Route struct{ PoolRef, PolicyRevision, Model, Region string }

type Decision struct {
	AccountID    int64  `json:"account_id"`
	CandidateRef string `json:"candidate_ref"`
	Revision     int64  `json:"revision"`
	Reason       string `json:"reason"`
}

func validRef(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.:-", char) {
			return false
		}
	}
	return true
}

func validModel(value string) bool {
	return len(value) > 0 && len(value) <= 255 && strings.IndexFunc(value, func(char rune) bool { return char <= 0x20 || char >= 0x7f }) < 0
}

func (t Target) validate() error {
	if !validRef(t.Ref) || !validRef(t.CredentialRef) || !validRef(t.ProviderRef) || !validRef(t.PoolRef) ||
		!validRef(t.PolicyRevision) || !validModel(t.Model) || !validRef(t.Region) || t.AccountID <= 0 ||
		(t.QuotaRef != "" && !validRef(t.QuotaRef)) ||
		t.CredentialRevision < 1 || t.BaseWeight < 1 || t.BaseWeight > 10000 || t.MaxInflight < 1 || t.MaxInflight > 100000 {
		return errors.New("invalid dispatch target metadata")
	}
	return nil
}

func (s Snapshot) Validate(now time.Time) error {
	if s.Version != SnapshotVersion || !validRef(s.CoreInstance) || s.Revision < 1 || s.GeneratedAt.IsZero() ||
		s.GeneratedAt.After(now.Add(5*time.Second)) || !s.ExpiresAt.After(now) || !s.ExpiresAt.After(s.GeneratedAt) ||
		s.ExpiresAt.Sub(s.GeneratedAt) > 30*time.Second || len(s.Candidates) > MaxCandidates {
		return errors.New("invalid or expired dispatch snapshot")
	}
	if !s.LoadObservedAt.IsZero() || !s.LoadExpiresAt.IsZero() {
		if s.LoadObservedAt.IsZero() || s.LoadExpiresAt.IsZero() || s.LoadObservedAt.After(now.Add(5*time.Second)) ||
			!s.LoadExpiresAt.After(now) || !s.LoadExpiresAt.After(s.LoadObservedAt) ||
			s.LoadExpiresAt.Sub(s.LoadObservedAt) > 10*time.Second || s.ExpiresAt.After(s.LoadExpiresAt) {
			return errors.New("invalid or stale dispatch load sample")
		}
	}
	refs := make(map[string]bool, len(s.Candidates))
	for _, c := range s.Candidates {
		if err := c.validate(); err != nil {
			return err
		}
		if refs[c.Ref] || c.Active < 0 || c.Active > 100000 || c.EffectiveWeight < 0 || c.EffectiveWeight > c.BaseWeight ||
			(c.State != Healthy && c.State != Degraded && c.State != Tripped && c.State != Probing) || !validRef(c.Reason) {
			return errors.New("invalid or duplicate dispatch candidate")
		}
		if (!c.Enabled || c.State == Tripped || c.State == Probing) && c.EffectiveWeight != 0 {
			return errors.New("ineligible dispatch candidate has positive weight")
		}
		refs[c.Ref] = true
	}
	return nil
}

func decodeJSON(body []byte, output any) error {
	// 配置和快照是外部边界；拒绝大小写别名、重复字段及未知字段。
	if err := uniqueFields(json.NewDecoder(bytes.NewReader(body)), 0); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("expected a single JSON document")
	}
	return nil
}

func uniqueFields(decoder *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("dispatch JSON nesting limit exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return errors.New("invalid JSON delimiter")
	}
	fields := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := token.(string)
			if !ok || fields[name] || name != strings.ToLower(name) {
				return errors.New("duplicate or invalid JSON field")
			}
			fields[name] = true
		}
		if err := uniqueFields(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func LoadSnapshot(path string, now time.Time) (Snapshot, error) {
	var snapshot Snapshot
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return snapshot, errors.New("dispatch snapshot must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return snapshot, errors.New("dispatch snapshot unavailable")
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return snapshot, errors.New("dispatch snapshot must be a regular file")
	}
	body, err := io.ReadAll(io.LimitReader(file, MaxSnapshotBytes+1))
	if err != nil || len(body) > MaxSnapshotBytes {
		return snapshot, errors.New("dispatch snapshot read limit exceeded")
	}
	if err := decodeJSON(body, &snapshot); err != nil {
		return Snapshot{}, errors.New("invalid dispatch snapshot JSON")
	}
	return snapshot, snapshot.Validate(now)
}

func WriteSnapshot(path string, snapshot Snapshot, now time.Time) error {
	if err := snapshot.Validate(now); err != nil {
		return err
	}
	body, err := json.Marshal(snapshot)
	if err != nil || len(body) > MaxSnapshotBytes {
		return errors.New("dispatch snapshot encode limit exceeded")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".dispatch-snapshot-*")
	if err != nil {
		return errors.New("dispatch snapshot output unavailable")
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("dispatch snapshot publish failed")
	}
	return nil
}

type publishedSnapshot struct {
	snapshot Snapshot
	digest   [32]byte
}

// MaxSnapshotLineages 限制一个 Selector 记住的发布 lineage 数；超过后拒绝新 lineage 直至重启。
const MaxSnapshotLineages = 16

type lineageMark struct {
	revision int64
	digest   [32]byte
}

type Selector struct {
	core       string
	seed       *SelectionSeed
	current    atomic.Pointer[publishedSnapshot]
	mu         sync.Mutex
	weights    map[string]int64
	seedTotals map[string]int64
	active     map[int64]int
	lineage    string
	lineages   map[string]lineageMark
	// 硬失效覆盖层（未启用时均为 nil）：invalidated 为当前快照中被剔除的候选。
	invalidations       map[string]Invalidation
	invalidationLineage string
	invalidated         map[string]bool
}

func NewSelector(coreInstance string) *Selector {
	return &Selector{core: coreInstance, weights: make(map[string]int64), active: make(map[int64]int)}
}

// Update 是文件模式入口，保持只按 revision 单调的原有规则。
func (s *Selector) Update(snapshot Snapshot, now time.Time) error {
	return s.update("", snapshot, now)
}

// UpdateEpoch 按发布 epoch (lineage, revision) 更新：同 lineage 沿用 revision 单调与同号摘要一致；
// 新 lineage 重置 SWRR 状态；回到已见 lineage 时 revision 必须超过其已见水位。
func (s *Selector) UpdateEpoch(lineage string, snapshot Snapshot, now time.Time) error {
	if !validRef(lineage) {
		return errors.New("invalid dispatch publish lineage")
	}
	return s.update(lineage, snapshot, now)
}

func (s *Selector) update(lineage string, snapshot Snapshot, now time.Time) error {
	if err := snapshot.Validate(now); err != nil {
		return err
	}
	if snapshot.CoreInstance != s.core {
		return errors.New("dispatch snapshot core identity mismatch")
	}
	snapshot.Candidates = append([]Candidate(nil), snapshot.Candidates...)
	sort.Slice(snapshot.Candidates, func(i, j int) bool { return snapshot.Candidates[i].Ref < snapshot.Candidates[j].Ref })
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	s.mu.Lock()
	defer s.mu.Unlock()
	weights := make(map[string]int64, len(snapshot.Candidates))
	var seedTotals map[string]int64
	if s.seed != nil {
		seedTotals = make(map[string]int64, len(snapshot.Candidates))
	}
	previous := s.current.Load()
	if previous != nil && lineage != s.lineage {
		if lineage == "" || s.lineage == "" {
			return errors.New("dispatch snapshot source mixes file and epoch updates")
		}
		mark, seen := s.lineages[lineage]
		if seen && snapshot.Revision <= mark.revision {
			return errors.New("dispatch snapshot epoch rollback")
		}
		if !seen && len(s.lineages) >= MaxSnapshotLineages {
			return errors.New("dispatch snapshot lineage limit exceeded")
		}
		previous = nil
	}
	if previous != nil {
		if snapshot.Revision < previous.snapshot.Revision {
			return errors.New("dispatch snapshot revision rollback")
		}
		if snapshot.Revision == previous.snapshot.Revision {
			if digest != previous.digest {
				return errors.New("dispatch snapshot revision conflict")
			}
			return nil
		}
		previousCandidates := make(map[string]Candidate, len(previous.snapshot.Candidates))
		for _, candidate := range previous.snapshot.Candidates {
			previousCandidates[candidate.Ref] = candidate
		}
		for _, candidate := range snapshot.Candidates {
			prior, exists := previousCandidates[candidate.Ref]
			if exists && (prior == candidate || (s.seed != nil && prior.Target == candidate.Target)) {
				if weight, initialized := s.weights[candidate.Ref]; initialized || s.seed == nil {
					weights[candidate.Ref] = weight
					if s.seed != nil {
						seedTotals[candidate.Ref] = s.seedTotals[candidate.Ref]
					}
				}
			}
		}
	}
	if s.seed != nil {
		s.seedWeights(snapshot.Candidates, weights, seedTotals)
	}
	s.current.Store(&publishedSnapshot{snapshot, digest})
	s.weights = weights
	s.seedTotals = seedTotals
	if lineage != "" {
		if s.lineages == nil {
			s.lineages = make(map[string]lineageMark, 1)
		}
		s.lineage, s.lineages[lineage] = lineage, lineageMark{snapshot.Revision, digest}
	}
	s.refreshInvalidatedLocked()
	return nil
}

func (s *Selector) Select(route Route, now time.Time) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectLocked(route, now)
}

func (s *Selector) selectLocked(route Route, now time.Time) Decision {
	loaded := s.current.Load()
	if loaded == nil {
		return Decision{Reason: "snapshot_unavailable"}
	}
	snapshot := &loaded.snapshot
	decision := Decision{Revision: snapshot.Revision, Reason: "no_eligible_route"}
	if !now.Before(snapshot.ExpiresAt) || now.Before(snapshot.GeneratedAt.Add(-5*time.Second)) {
		decision.Reason = "snapshot_expired"
		return decision
	}
	var selected *Candidate
	var total int64
	var eligible []selectionWeight
	for i := range snapshot.Candidates {
		c := &snapshot.Candidates[i]
		if c.PoolRef != route.PoolRef || c.PolicyRevision != route.PolicyRevision || c.Model != route.Model || c.Region != route.Region ||
			!c.Enabled || (c.State != Healthy && c.State != Degraded) || c.Active >= c.MaxInflight || c.EffectiveWeight == 0 || s.invalidated[c.Ref] {
			continue
		}
		total += c.EffectiveWeight
		if s.seed != nil {
			eligible = append(eligible, selectionWeight{c, c.EffectiveWeight})
			continue
		}
		s.weights[c.Ref] += c.EffectiveWeight
		if selected == nil || s.weights[c.Ref] > s.weights[selected.Ref] {
			selected = c
		}
	}
	if s.seed != nil {
		selected = s.selectSeededLocked(eligible, total)
	}
	if selected == nil {
		return decision
	}
	s.weights[selected.Ref] -= total
	decision.AccountID, decision.CandidateRef, decision.Reason = selected.AccountID, selected.Ref, "selected"
	return decision
}
