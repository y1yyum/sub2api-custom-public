package custombridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxTelemetryConfigBytes = 2 << 20

type TelemetryTarget struct {
	TargetRef          string      `json:"target_ref"`
	AccountID          int64       `json:"account_id"`
	CredentialRef      string      `json:"credential_ref"`
	CredentialRevision int64       `json:"credential_revision"`
	CredentialSHA256   string      `json:"credential_sha256"`
	ProviderRef        string      `json:"provider_ref"`
	PoolRef            string      `json:"pool_ref"`
	PolicyRevision     string      `json:"policy_revision"`
	Model              string      `json:"model"`
	Region             string      `json:"region"`
	ErrorRules         []ErrorRule `json:"error_rules,omitempty"`
}

// ErrorRule is an operator-supplied exact match, not a provider standard.
type ErrorRule struct {
	Status       int    `json:"status"`
	Code         string `json:"code"`
	Type         string `json:"type,omitempty"`
	FailureClass string `json:"failure_class"`
}

func telemetryErrorToken(value string) bool {
	return len(value) > 0 && len(value) <= 255 && utf8.ValidString(value) && strings.TrimSpace(value) != "" &&
		strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

func canonicalTelemetryErrorRules(rules []ErrorRule) []ErrorRule {
	rules = slices.Clone(rules)
	sort.Slice(rules, func(i, j int) bool {
		a, b := rules[i], rules[j]
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Type < b.Type
	})
	return rules
}

func validateTelemetryErrorRules(rules []ErrorRule) error {
	if len(rules) > 64 {
		return errors.New("too many bridge telemetry error rules")
	}
	for i, rule := range rules {
		compatible := rule.Status == 429 && rule.FailureClass == "quota_exhausted" ||
			rule.Status == 403 && (rule.FailureClass == "credential_banned" || rule.FailureClass == "capability_denied")
		if !compatible || !telemetryErrorToken(rule.Code) || rule.Type != "" && !telemetryErrorToken(rule.Type) {
			return errors.New("invalid bridge telemetry error rule")
		}
		for _, prior := range rules[:i] {
			if rule.Status == prior.Status && rule.Code == prior.Code && (rule.Type == prior.Type || rule.Type == "" || prior.Type == "") {
				return errors.New("ambiguous bridge telemetry error rules")
			}
		}
	}
	return nil
}

type TelemetryConfig struct {
	SchemaVersion  int               `json:"schema_version"`
	CoreInstance   string            `json:"core_instance"`
	ConfigRevision int64             `json:"config_revision"`
	Targets        []TelemetryTarget `json:"targets"`
}

// LoadTelemetryConfigFromEnv leaves old bridge paths independent of telemetry.
func LoadTelemetryConfigFromEnv() (*TelemetryConfig, error) {
	enabled, _ := strconv.ParseBool(os.Getenv("SUB2API_BRIDGE_ENABLED"))
	path := os.Getenv("SUB2API_BRIDGE_TELEMETRY_CONFIG")
	if !enabled || path == "" {
		return nil, nil
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("invalid bridge telemetry configuration path")
	}
	root, err := os.OpenRoot(filepath.Dir(resolved))
	if err != nil {
		return nil, errors.New("bridge telemetry configuration directory unavailable")
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.Base(resolved))
	if err != nil {
		return nil, errors.New("bridge telemetry configuration unavailable")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, MaxTelemetryConfigBytes+1))
	if err != nil || len(raw) > MaxTelemetryConfigBytes {
		return nil, errors.New("bridge telemetry configuration exceeds read limit")
	}
	if err := telemetryStrictJSON(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cfg TelemetryConfig
	if err := decoder.Decode(&cfg); err != nil {
		return nil, errors.New("invalid bridge telemetry configuration")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c TelemetryConfig) Validate() error {
	if c.SchemaVersion != 1 || !forwardID.MatchString(c.CoreInstance) || c.ConfigRevision < 1 || len(c.Targets) < 1 || len(c.Targets) > 1000 {
		return errors.New("invalid bridge telemetry identity or target count")
	}
	refs := map[string]bool{}
	routes := map[telemetryRoute]bool{}
	accounts := map[int64]TelemetryTarget{}
	credentials := map[string]TelemetryTarget{}
	credentialRules := map[string][]ErrorRule{}
	for _, target := range c.Targets {
		if err := validateTelemetryErrorRules(target.ErrorRules); err != nil {
			return err
		}
		target.ErrorRules = canonicalTelemetryErrorRules(target.ErrorRules)
		for _, ref := range []string{target.TargetRef, target.CredentialRef, target.ProviderRef, target.PoolRef, target.PolicyRevision, target.Region} {
			if !forwardID.MatchString(ref) {
				return errors.New("invalid bridge telemetry target reference")
			}
		}
		if target.AccountID < 1 || target.CredentialRevision < 1 || !telemetryDigestValid(target.CredentialSHA256) ||
			len(target.Model) < 1 || len(target.Model) > 255 || strings.IndexFunc(target.Model, func(r rune) bool { return r <= 0x20 || r >= 0x7f }) >= 0 {
			return errors.New("invalid bridge telemetry target identity")
		}
		route := target.route()
		if refs[target.TargetRef] || routes[route] {
			return errors.New("ambiguous bridge telemetry target mapping")
		}
		refs[target.TargetRef], routes[route] = true, true
		if prior, ok := accounts[target.AccountID]; ok && (prior.CredentialRef != target.CredentialRef || prior.CredentialRevision != target.CredentialRevision || prior.CredentialSHA256 != target.CredentialSHA256 || prior.ProviderRef != target.ProviderRef || !slices.Equal(prior.ErrorRules, target.ErrorRules)) {
			return errors.New("inconsistent bridge telemetry account mapping")
		}
		if prior, ok := credentials[target.CredentialRef]; ok && (prior.CredentialRevision != target.CredentialRevision || prior.CredentialSHA256 != target.CredentialSHA256 || prior.ProviderRef != target.ProviderRef || !slices.Equal(prior.ErrorRules, target.ErrorRules)) {
			return errors.New("inconsistent bridge telemetry credential mapping")
		}
		if prior, ok := credentialRules[target.CredentialSHA256]; ok && !slices.Equal(prior, target.ErrorRules) {
			return errors.New("inconsistent bridge telemetry credential error rules")
		}
		credentialRules[target.CredentialSHA256] = target.ErrorRules
		accounts[target.AccountID], credentials[target.CredentialRef] = target, target
	}
	return nil
}

func (c TelemetryConfig) Digest() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	c.Targets = append([]TelemetryTarget(nil), c.Targets...)
	for i := range c.Targets {
		c.Targets[i].ErrorRules = canonicalTelemetryErrorRules(c.Targets[i].ErrorRules)
	}
	sort.Slice(c.Targets, func(i, j int) bool { return c.Targets[i].TargetRef < c.Targets[j].TargetRef })
	raw, err := json.Marshal(c)
	if err != nil {
		return "", errors.New("invalid bridge telemetry configuration")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (c TelemetryConfig) ActivationDigest(slotTTLSeconds int) (string, error) {
	if slotTTLSeconds < 1 || slotTTLSeconds > 86400 {
		return "", errors.New("invalid bridge telemetry slot TTL")
	}
	configDigest, err := c.Digest()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(configDigest + ":" + strconv.Itoa(slotTTLSeconds)))
	return hex.EncodeToString(digest[:]), nil
}

// TelemetryCredentialSHA256 uses encoding/json's sorted object keys, including
// nested maps, and its default numeric and HTML escaping rules.
func TelemetryCredentialSHA256(credentials map[string]any) (string, error) {
	if len(credentials) == 0 {
		return "", errors.New("bridge telemetry credentials unavailable")
	}
	raw, err := json.Marshal(credentials)
	if err != nil || len(raw) > MaxTelemetryConfigBytes {
		return "", errors.New("bridge telemetry credentials cannot be hashed")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func telemetryDigestValid(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type telemetryRoute struct {
	accountID           int64
	pool, policy, model string
}

func (t TelemetryTarget) route() telemetryRoute {
	return telemetryRoute{t.AccountID, t.PoolRef, t.PolicyRevision, t.Model}
}

// Reject duplicate and case-folded JSON keys before decoding trusted config.
func telemetryStrictJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 8 {
			return errors.New("invalid bridge telemetry configuration depth")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || key != strings.ToLower(key) || seen[key] {
					return errors.New("invalid bridge telemetry configuration key")
				}
				seen[key] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		case json.Delim('['):
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		}
		return err
	}
	if err := walk(0); err != nil {
		return errors.New("invalid bridge telemetry configuration JSON")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing bridge telemetry configuration JSON")
	}
	return nil
}
