package dispatch

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/custom/quality"
)

type QualityConfig struct {
	BaselineFile   string                 `json:"baseline_file"`
	Bindings       []QualityBinding       `json:"bindings"`
	MaxResults     int                    `json:"max_results"`
	ApplyWeight    bool                   `json:"apply_weight"`
	Deferral       *QualityDeferralConfig `json:"deferral,omitempty"`
	baselineBody   []byte
	baselineDigest string
	baseline       *quality.Baseline
}

type qualityConfigVersion struct {
	Bindings            []QualityBinding
	MaxResults          int
	ApplyWeight         bool
	BaselineDigest      string
	ScorerRevision      string
	TemplateRevision    string
	CalibrationRevision string
	Deferral            *QualityDeferralConfig `json:",omitempty"`
}

func (q *QualityConfig) version() *qualityConfigVersion {
	if q == nil {
		return nil
	}
	bindings := append([]QualityBinding(nil), q.Bindings...)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].PlanID < bindings[j].PlanID })
	return &qualityConfigVersion{bindings, q.MaxResults, q.ApplyWeight, q.baselineDigest, quality.ScorerRevision, quality.TemplateRevision, quality.CalibrationRevision, q.Deferral}
}

func normalizeQualityConfig(q QualityConfig, targets []Target) (QualityConfig, error) {
	if q.MaxResults == 0 {
		q.MaxResults = 1000
	}
	if strings.TrimSpace(q.BaselineFile) == "" || q.MaxResults < 1 || q.MaxResults > 10000 || len(q.Bindings) < 1 || len(q.Bindings) > MaxCandidates {
		return QualityConfig{}, errors.New("invalid dispatch quality configuration")
	}
	if _, err := validateQualityBindings(q.Bindings, targets); err != nil {
		return QualityConfig{}, err
	}
	body, err := readQualityBaseline(q.BaselineFile)
	if err != nil {
		return QualityConfig{}, err
	}
	baseline, err := quality.ParseBaseline(body)
	if err != nil {
		return QualityConfig{}, errors.New("invalid dispatch quality baseline")
	}
	q.baseline = baseline
	q.baselineBody = body
	q.baselineDigest = fmt.Sprintf("%x", sha256.Sum256(body))
	return q, nil
}

func readQualityBaseline(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, errors.New("dispatch quality baseline unavailable")
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(filepath.Base(path))
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("dispatch quality baseline must be regular")
	}
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, errors.New("dispatch quality baseline unavailable")
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("dispatch quality baseline must be regular")
	}
	body, err := io.ReadAll(io.LimitReader(file, MaxSnapshotBytes+1))
	if err != nil || len(body) > MaxSnapshotBytes {
		return nil, errors.New("dispatch quality baseline read limit exceeded")
	}
	return body, nil
}
