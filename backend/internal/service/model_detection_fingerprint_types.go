package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ModelDetectionFingerprintState is the outcome of comparing a model's probe
// behaviour with its trusted baseline. It is not a TLS, browser or client
// fingerprint and never an authentication verdict.
type ModelDetectionFingerprintState string

const (
	ModelDetectionFingerprintConsistent   ModelDetectionFingerprintState = "consistent"
	ModelDetectionFingerprintSuspected    ModelDetectionFingerprintState = "suspected"
	ModelDetectionFingerprintInconsistent ModelDetectionFingerprintState = "inconsistent"
	ModelDetectionFingerprintNotChecked   ModelDetectionFingerprintState = "not_checked"
)

// ModelDetectionFingerprintReasonNotImplemented is reported by the Noop provider.
const ModelDetectionFingerprintReasonNotImplemented = "not_implemented"

// ModelDetectionFingerprintSummary is the per-row fingerprint result exposed by
// the status catalog. Only a version-neutral summary is carried; raw prompts,
// model output and upstream responses never appear here.
type ModelDetectionFingerprintSummary struct {
	State      ModelDetectionFingerprintState `json:"state"`
	ReasonCode string                         `json:"reason_code"`
	CheckedAt  *time.Time                     `json:"checked_at"`
	Confidence *float64                       `json:"confidence"`
}

// CatalogRowKey is the composite identity of a catalog row.
// Model is stored normalized (see NormalizeCatalogModel).
type CatalogRowKey struct {
	GroupID  int64
	Platform string
	Model    string
}

// NormalizeCatalogModel trims and lower-cases a model name for row identity.
func NormalizeCatalogModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// NewCatalogRowKey builds a key with the model normalized.
func NewCatalogRowKey(groupID int64, platform, model string) CatalogRowKey {
	return CatalogRowKey{GroupID: groupID, Platform: strings.ToLower(strings.TrimSpace(platform)), Model: NormalizeCatalogModel(model)}
}

// String renders the stable row key used by the API and the frontend.
func (k CatalogRowKey) String() string {
	return fmt.Sprintf("%d:%s:%s", k.GroupID, k.Platform, k.Model)
}

// ModelDetectionWindow is the time window a provider should summarize.
type ModelDetectionWindow struct {
	Start time.Time
	End   time.Time
}

// ModelDetectionFingerprintProvider supplies fingerprint summaries for catalog
// rows. Probes, baselines and storage are intentionally out of scope for now;
// only this seam exists so a later implementation can be injected.
type ModelDetectionFingerprintProvider interface {
	Summaries(ctx context.Context, keys []CatalogRowKey, window ModelDetectionWindow) (map[CatalogRowKey]ModelDetectionFingerprintSummary, error)
}

// NoopModelDetectionFingerprintProvider reports every key as not checked.
type NoopModelDetectionFingerprintProvider struct{}

// NotCheckedFingerprintSummary is the summary for rows without a real provider.
func NotCheckedFingerprintSummary() ModelDetectionFingerprintSummary {
	return ModelDetectionFingerprintSummary{
		State:      ModelDetectionFingerprintNotChecked,
		ReasonCode: ModelDetectionFingerprintReasonNotImplemented,
	}
}

func (NoopModelDetectionFingerprintProvider) Summaries(_ context.Context, keys []CatalogRowKey, _ ModelDetectionWindow) (map[CatalogRowKey]ModelDetectionFingerprintSummary, error) {
	out := make(map[CatalogRowKey]ModelDetectionFingerprintSummary, len(keys))
	for _, key := range keys {
		out[key] = NotCheckedFingerprintSummary()
	}
	return out, nil
}
