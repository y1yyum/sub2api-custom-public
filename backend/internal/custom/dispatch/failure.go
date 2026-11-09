package dispatch

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxHealthHolds          = 4096
	FailureQuotaExhausted   = "quota_exhausted"
	FailureCredentialBanned = "credential_banned"
	FailureCapabilityDenied = "capability_denied"
)

var (
	ErrHealthHoldRevision          = errors.New("dispatch health hold revision mismatch")
	ErrHealthHoldOperationConflict = errors.New("dispatch health hold operation conflict")
	ErrHealthHoldNotFound          = errors.New("dispatch health hold not found")
)

type HealthHoldScope struct {
	Kind          string `json:"kind"`
	ProviderRef   string `json:"provider_ref,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
	QuotaRef      string `json:"quota_ref,omitempty"`
	AccountID     int64  `json:"account_id,omitempty"`
	Model         string `json:"model,omitempty"`
	Region        string `json:"region,omitempty"`
}

type HealthHoldReport struct {
	ID             string           `json:"id"`
	CoreInstance   string           `json:"core_instance"`
	Revision       int64            `json:"revision"`
	Class          string           `json:"class"`
	Scope          HealthHoldScope  `json:"scope"`
	Active         bool             `json:"active"`
	ConfigRevision int64            `json:"config_revision"`
	TriggerEventID string           `json:"trigger_event_id"`
	TriggeredAt    time.Time        `json:"triggered_at"`
	ReleasedAt     *time.Time       `json:"released_at,omitempty"`
	Targets        []Target         `json:"targets"`
	LastRelease    *HealthHoldAudit `json:"last_release,omitempty"`
}

type HoldReleaseRequest struct {
	CoreInstance   string `json:"core_instance"`
	ConfigRevision int64  `json:"config_revision"`
	HoldID         string `json:"hold_id"`
	HoldRevision   int64  `json:"hold_revision"`
	OperationID    string `json:"operation_id"`
	Actor          string `json:"actor"`
	Reason         string `json:"reason"`
	EvidenceRef    string `json:"evidence_ref"`
}

type HealthHoldAudit struct {
	ID             string    `json:"id"`
	CoreInstance   string    `json:"core_instance"`
	HoldID         string    `json:"hold_id"`
	HoldRevision   int64     `json:"hold_revision"`
	Action         string    `json:"action"`
	ConfigRevision int64     `json:"config_revision"`
	EventID        string    `json:"event_id,omitempty"`
	OperationID    string    `json:"operation_id,omitempty"`
	Actor          string    `json:"actor,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	EvidenceRef    string    `json:"evidence_ref,omitempty"`
	RecordedAt     time.Time `json:"recorded_at"`
}

type HoldReleaseResult struct {
	Hold       HealthHoldReport `json:"hold"`
	Audit      HealthHoldAudit  `json:"audit"`
	Idempotent bool             `json:"idempotent"`
}

func (r HoldReleaseRequest) Validate() error {
	if !validRef(r.CoreInstance) || r.ConfigRevision < 1 || !validRef(r.HoldID) || r.HoldRevision < 1 ||
		!validRef(r.OperationID) || !validRef(r.Actor) || !validRef(r.EvidenceRef) ||
		!utf8.ValidString(r.Reason) || strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 512 ||
		strings.IndexFunc(r.Reason, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }) >= 0 {
		return errors.New("invalid dispatch health hold release request")
	}
	return nil
}

func LoadHoldReleaseRequest(path string) (HoldReleaseRequest, error) {
	var request HoldReleaseRequest
	if err := readJSONFile(path, 16<<10, &request); err != nil {
		return HoldReleaseRequest{}, err
	}
	if err := request.Validate(); err != nil {
		return HoldReleaseRequest{}, err
	}
	return request, nil
}

func validFailureClass(o Observation) bool {
	switch o.FailureClass {
	case "":
		return true
	case FailureQuotaExhausted:
		return o.ProviderEvidence && o.Outcome == UpstreamRateLimit
	case FailureCredentialBanned, FailureCapabilityDenied:
		return o.ProviderEvidence && o.Outcome == UpstreamAuth
	default:
		return false
	}
}

func failureScope(class string, target Target) HealthHoldScope {
	if class == FailureCredentialBanned {
		return HealthHoldScope{Kind: "credential", ProviderRef: target.ProviderRef, CredentialRef: target.CredentialRef}
	}
	if class == FailureQuotaExhausted && target.QuotaRef != "" {
		return HealthHoldScope{Kind: "quota", ProviderRef: target.ProviderRef, QuotaRef: target.QuotaRef}
	}
	return HealthHoldScope{Kind: "account_model_region", AccountID: target.AccountID, Model: target.Model, Region: target.Region}
}

func (s HealthHoldScope) matches(target Target) bool {
	switch s.Kind {
	case "credential":
		return s.ProviderRef == target.ProviderRef && s.CredentialRef == target.CredentialRef
	case "quota":
		return s.ProviderRef == target.ProviderRef && s.QuotaRef == target.QuotaRef
	case "account_model_region":
		return s.AccountID == target.AccountID && s.Model == target.Model && s.Region == target.Region
	default:
		return false
	}
}

func (s HealthHoldScope) valid(class string) bool {
	switch {
	case class == FailureCredentialBanned && s.Kind == "credential":
		return validRef(s.ProviderRef) && validRef(s.CredentialRef) && s.QuotaRef == "" && s.AccountID == 0 && s.Model == "" && s.Region == ""
	case class == FailureQuotaExhausted && s.Kind == "quota":
		return validRef(s.ProviderRef) && validRef(s.QuotaRef) && s.CredentialRef == "" && s.AccountID == 0 && s.Model == "" && s.Region == ""
	case (class == FailureQuotaExhausted || class == FailureCapabilityDenied) && s.Kind == "account_model_region":
		return s.AccountID > 0 && validModel(s.Model) && validRef(s.Region) && s.CredentialRef == "" && s.ProviderRef == "" && s.QuotaRef == ""
	default:
		return false
	}
}

func healthHoldID(core, class string, scope HealthHoldScope) string {
	body, _ := json.Marshal(struct {
		Core, Class string
		Scope       HealthHoldScope
	}{core, class, scope})
	return fmt.Sprintf("hold:%x", sha256.Sum256(body))
}
