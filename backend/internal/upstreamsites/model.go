package upstreamsites

import (
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// model.go defines upstream wallets, address templates, and monitoring policies.
// A site owns monitoring credentials independently from provider forwarding
// credentials. Provider membership never implies URL inheritance: endpoints are
// explicit form templates, and control decisions restrict the entire membership.

// Stable outcomes distinguish evidence of unreachability from credential and
// monitoring failures; only qualifying outcomes participate in automatic control.
const (
	Healthy        = "healthy"
	Unreachable    = "unreachable"
	Unknown        = "unknown"
	Authentication = "authentication"
	RateLimited    = "rate_limited"
	Configuration  = "configuration"
	MonitorFailure = "monitor_failure"
	ProbeUnhealthy = "probe_unhealthy"
)

// Domain errors distinguish invalid configuration, concurrent edits, and absence
// without exposing credentials or untrusted upstream response bodies to clients.
var (
	ErrNotificationSkipped = errors.New("notification suppressed by recipient preferences")
	ErrNotificationNotSent = errors.New("notification was not sent")
	ErrInvalid             = errors.New("invalid upstream site configuration")
	ErrConflict            = errors.New("upstream site changed; reload before saving")
	ErrNotFound            = errors.New("upstream site not found")
	ErrBound               = errors.New("upstream site still has bound providers")
	ErrBlocked             = errors.New("provider is restricted by its upstream site")
	ErrBusy                = errors.New("upstream site observation already running")
)

// Endpoint is a copyable protocol address, never a dynamic forwarding reference.
type Endpoint struct {
	ID       string `json:"id"`       // stable template identity
	Name     string `json:"name"`     // administrator template label
	Protocol string `json:"protocol"` // protocol catalogue identifier
	URL      string `json:"url"`      // complete forwarding root
	Default  bool   `json:"default"`  // preferred protocol template
}

// Threshold defines hysteresis in one explicitly selected wallet currency.
type Threshold struct {
	Unit    string `json:"unit"`    // upstream currency identifier
	Low     string `json:"low"`     // exclusive suspension threshold
	Recover string `json:"recover"` // inclusive recovery threshold
}

// Policy controls site restrictions independently from providers' manual switches.
type Policy struct {
	Mode              string      `json:"mode"`                // off, balance, probe, combined
	AutoRecover       bool        `json:"auto_recover"`        // permit evidence-based recovery
	Thresholds        []Threshold `json:"thresholds"`          // independently denominated balance rules
	BalanceMatch      string      `json:"balance_match"`       // any or all
	LowCount          int         `json:"low_count"`           // consecutive low observations
	FailureCount      int         `json:"failure_count"`       // consecutive unreachable observations
	RecoveryCount     int         `json:"recovery_count"`      // consecutive healthy observations
	MinSuspendSeconds int         `json:"min_suspend_seconds"` // minimum restriction duration
}

// Probe specifies a read-only endpoint and an explicit JSON response validator.
type Probe struct {
	ID              string `json:"id"`               // stable observation source
	URL             string `json:"url"`              // complete read-only target
	Required        bool   `json:"required"`         // failure restricts entire site
	IntervalMinutes int    `json:"interval_minutes"` // scheduled observation interval
	TimeoutSeconds  int    `json:"timeout_seconds"`  // total request budget
	JSONPath        string `json:"json_path"`        // required dotted JSON field
	Expected        string `json:"expected"`         // required field string representation
	UseWalletKey    bool   `json:"use_wallet_key"`   // explicit same-origin credential reuse
	SuccessCodes    []int  `json:"success_codes"`    // accepted HTTP status codes
}

// AlertPolicy selects persisted notifications without changing provider eligibility.
type AlertPolicy struct {
	DailyMetric string `json:"daily_metric"` // estimated or account consumption
	Control     bool   `json:"control"`      // send restriction transition mail
	Unit        string `json:"unit"`         // alert amount currency
	LowBalance  string `json:"low_balance"`  // optional exclusive balance threshold
	DailySpend  string `json:"daily_spend"`  // optional selected consumption threshold
	Failures    int    `json:"failures"`     // optional consecutive failure threshold
	Email       string `json:"email"`        // optional administrator notification address
}

// Config is a versioned administrator document with no plaintext secrets.
type Config struct {
	RawRetentionDays     int         `json:"raw_retention_days"`     // observation retention in days
	SummaryRetentionDays int         `json:"summary_retention_days"` // summary and event retention
	Name                 string      `json:"name"`                   // administrator site label
	Notes                string      `json:"notes"`                  // optional administrator notes
	BaseURL              string      `json:"base_url"`               // independent wallet query root
	Adapter              string      `json:"adapter"`                // fixed wallet format
	Endpoints            []Endpoint  `json:"endpoints"`              // copyable protocol address templates
	AutoQuery            bool        `json:"auto_query"`             // enable scheduled wallet observations
	IntervalMinutes      int         `json:"interval_minutes"`       // wallet query interval
	Timezone             string      `json:"timezone"`               // IANA statistics timezone
	Policy               Policy      `json:"policy"`                 // automatic eligibility rules
	Probes               []Probe     `json:"probes"`                 // independent health observation sources
	Alerts               AlertPolicy `json:"alerts"`                 // notification-only thresholds
}

// Secrets is encrypted as one document and excluded from all site responses.
type Secrets struct {
	ProxyURL     string            `json:"proxy_url"`      // encrypted monitoring proxy connection
	Key          string            `json:"key"`            // wallet query credential
	WalletToken  string            `json:"wallet_token"`   // optional New API account token
	WalletUserID string            `json:"wallet_user_id"` // optional New API account identity
	ProbeKeys    map[string]string `json:"probe_keys"`     // explicit probe-specific credentials
}

// Balance preserves decimal wallet amounts without conflating missing and zero.
type Balance struct {
	Unit      string  `json:"unit"`            // upstream currency identifier
	Available string  `json:"available"`       // spendable wallet amount
	Total     *string `json:"total,omitempty"` // optional unfrozen account total
	Used      *string `json:"used,omitempty"`  // optional account cumulative consumption
}

// Observation is one accepted source result; errors never manufacture balance samples.
type Observation struct {
	ConfigVersion   int64      `json:"config_version"`        // accepted source configuration revision
	RetryAt         *time.Time `json:"retry_at,omitempty"`    // bounded upstream rate-limit deadline
	RunID           string     `json:"run_id"`                // unique persisted round
	Source          string     `json:"source"`                // balance or probe identity
	At              time.Time  `json:"at"`                    // UTC observation completion
	Outcome         string     `json:"outcome"`               // structured evidence classification
	Code            string     `json:"code"`                  // safe diagnostic identifier
	HTTPStatus      int        `json:"http_status,omitempty"` // observed upstream status
	Balances        []Balance  `json:"balances,omitempty"`    // wallet-only normalized amounts
	IntervalMinutes int        `json:"interval_minutes"`      // interval at observation time
	WalletEpoch     int64      `json:"wallet_epoch"`          // immutable wallet identity segment
	StatisticsEpoch int64      `json:"statistics_epoch"`      // immutable calendar segment
	Timezone        string     `json:"timezone"`              // calendar used for this observation
}

// Counter tracks independent rounds and fresh evidence for one monitoring source.
type Counter struct {
	Failures    int       `json:"failures"`     // consecutive unreachable observation count
	Successes   int       `json:"successes"`    // consecutive healthy observation count
	Low         int       `json:"low"`          // consecutive low wallet count
	Recovered   int       `json:"recovered"`    // consecutive above-recovery wallet count
	LastCounted time.Time `json:"last_counted"` // last distinct control round
	LastHealthy time.Time `json:"last_healthy"` // latest validated healthy evidence
	LastRun     string    `json:"last_run"`     // idempotent control observation identity
}

// State contains durable reasons whose union determines effective site restriction.
type State struct {
	Manual   bool                 `json:"manual"`   // administrator suspension remains independent
	Reasons  map[string]time.Time `json:"reasons"`  // reason to original suspension instant
	Counters map[string]Counter   `json:"counters"` // source-isolated evidence counters
	Version  int64                `json:"version"`  // optimistic manual state revision
}

// Site combines public configuration and state with private encrypted persistence.
type Site struct {
	Costs           []CostSummary        `json:"costs"`
	Today           []DailyStat          `json:"today"`                      // current wallet local-day summaries
	HasProxy        bool                 `json:"has_proxy"`                  // monitoring proxy presence only
	ID              int64                `json:"id"`                         // persisted site identity
	Config          Config               `json:"config"`                     // administrator editable configuration
	ConfigVersion   int64                `json:"config_version"`             // optimistic configuration revision
	WalletEpoch     int64                `json:"wallet_epoch"`               // wallet identity revision
	StatisticsEpoch int64                `json:"statistics_epoch"`           // calendar identity revision
	Archived        bool                 `json:"archived"`                   // monitoring lifecycle suspension
	HasKey          bool                 `json:"has_key"`                    // wallet credential presence only
	State           State                `json:"state"`                      // persisted admission restrictions
	Latest          *Observation         `json:"latest,omitempty"`           // last successful wallet observation
	LastObservation *Observation         `json:"last_observation,omitempty"` // latest classified monitoring result
	NextDue         map[string]time.Time `json:"next_due"`                   // persisted source monitoring deadlines
	ProviderCount   int                  `json:"provider_count"`             // active bound provider count
	Ciphertext      string               `json:"-"`                          // encrypted monitoring credential document
	WalletChanged   bool                 `json:"-"`                          // service-verified wallet identity change
}

// DefaultConfig supplies conservative scheduling and recovery settings for new sites.
func DefaultConfig() Config {
	return Config{Adapter: "sub2api", IntervalMinutes: 60, Timezone: "UTC", Policy: Policy{
		Mode: "off", AutoRecover: false, BalanceMatch: "any", LowCount: 1,
		FailureCount: 3, RecoveryCount: 2, MinSuspendSeconds: 120,
	}, Endpoints: []Endpoint{}, Probes: []Probe{}}
}

// Blocked reports the union of manual and persisted automatic restrictions.
func (s State) Blocked() bool { return s.Manual || len(s.Reasons) > 0 }

// Amount parses bounded fixed-point monetary input without floating-point conversion.
// Values exceeding numeric(30,10) are rejected instead of rounded; callers retain
// negative wallet debt while applying their own threshold sign requirements.
func Amount(raw string) (decimal.Decimal, error) {
	if raw == "" || len(raw) > 64 {
		return decimal.Zero, ErrInvalid
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Exponent() < -10 || v.Exponent() > 20 || v.Abs().GreaterThanOrEqual(decimal.New(1, 20)) {
		return decimal.Zero, ErrInvalid
	}
	return v, nil
}

// Validate checks all control sources before configuration can be persisted.
// It normalizes a missing name from the host and enables wallet polling when
// a balance policy depends on it. Address templates remain independent values.
func (c *Config) Validate() error {
	if c.Endpoints == nil {
		c.Endpoints = []Endpoint{}
	}
	if c.Probes == nil {
		c.Probes = []Probe{}
	}
	if c.Policy.Thresholds == nil {
		c.Policy.Thresholds = []Threshold{}
	}
	if c.RawRetentionDays == 0 {
		c.RawRetentionDays = 90
	}
	if c.SummaryRetentionDays == 0 {
		c.SummaryRetentionDays = 365
	}
	if c.RawRetentionDays < 7 || c.RawRetentionDays > 3650 || c.SummaryRetentionDays < c.RawRetentionDays || c.SummaryRetentionDays > 3650 {
		return ErrInvalid
	}
	if c.Alerts.DailyMetric == "" {
		c.Alerts.DailyMetric = "estimated"
	}
	if c.Alerts.DailyMetric != "estimated" && c.Alerts.DailyMetric != "account" {
		return ErrInvalid
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ErrInvalid
	}
	if c.Name == "" {
		c.Name = u.Hostname()
	}
	if len(c.Name) > 100 || len(c.Notes) > 2000 || len(c.BaseURL) > 2048 {
		return ErrInvalid
	}
	switch c.Adapter {
	case "sub2api", "new_api", "zivv", "derouter", "kimi_balance", "deepseek_balance":
	default:
		return ErrInvalid
	}
	if c.IntervalMinutes < 5 || c.IntervalMinutes > 1440 {
		return ErrInvalid
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return ErrInvalid
	}
	p := c.Policy
	if p.Mode != "off" && p.Mode != "balance" && p.Mode != "probe" && p.Mode != "combined" {
		return ErrInvalid
	}
	if p.Mode == "off" && p.AutoRecover {
		return ErrInvalid
	}
	if p.LowCount < 1 || p.LowCount > 100 || p.FailureCount < 1 || p.FailureCount > 100 || p.RecoveryCount < 1 || p.RecoveryCount > 100 || p.MinSuspendSeconds < 0 || p.MinSuspendSeconds > 86400 {
		return ErrInvalid
	}
	if p.BalanceMatch != "any" && p.BalanceMatch != "all" {
		return ErrInvalid
	}
	units := map[string]bool{}
	for _, t := range p.Thresholds {
		low, e1 := Amount(t.Low)
		high, e2 := Amount(t.Recover)
		if e1 != nil || e2 != nil || high.LessThan(low) || !validUnit(t.Unit) || units[t.Unit] {
			return ErrInvalid
		}
		units[t.Unit] = true
	}
	if c.balanceControl() {
		if len(p.Thresholds) == 0 {
			return ErrInvalid
		}
		c.AutoQuery = true
	}
	if len(c.Endpoints) > 32 || len(c.Probes) > 16 {
		return ErrInvalid
	}
	ids, defaults := map[string]bool{}, map[string]bool{}
	for _, e := range c.Endpoints {
		if e.ID == "" || len(e.ID) > 64 || ids[e.ID] || e.Protocol == "" || !validURL(e.URL) || (e.Default && defaults[e.Protocol]) {
			return ErrInvalid
		}
		ids[e.ID] = true
		if e.Default {
			defaults[e.Protocol] = true
		}
	}
	ids = map[string]bool{}
	required := 0
	for _, probe := range c.Probes {
		if probe.ID == "" || probe.ID == "balance" || strings.ContainsAny(probe.ID, ":/ ") || len(probe.ID) > 64 || ids[probe.ID] || !validURL(probe.URL) || probe.IntervalMinutes < 1 || probe.IntervalMinutes > 1440 || probe.TimeoutSeconds < 3 || probe.TimeoutSeconds > 60 || probe.JSONPath == "" || len(probe.JSONPath) > 200 || len(probe.Expected) > 500 {
			return ErrInvalid
		}
		ids[probe.ID] = true
		if strings.ContainsAny(probe.JSONPath, "#@|[](){}\\") {
			return ErrInvalid
		}
		for _, code := range probe.SuccessCodes {
			if code < 200 || code > 299 {
				return ErrInvalid
			}
		}
		if probe.UseWalletKey {
			v, _ := url.Parse(probe.URL)
			if !strings.EqualFold(v.Scheme, u.Scheme) || !strings.EqualFold(v.Host, u.Host) {
				return ErrInvalid
			}
		}
		if probe.Required {
			required++
		}
	}
	if c.probeControl() && required == 0 {
		return ErrInvalid
	}
	for _, value := range []string{c.Alerts.LowBalance, c.Alerts.DailySpend} {
		if value != "" {
			if _, err := Amount(value); err != nil || !validUnit(c.Alerts.Unit) {
				return ErrInvalid
			}
		}
	}
	if c.Alerts.LowBalance != "" {
		value, _ := Amount(c.Alerts.LowBalance)
		if value.IsNegative() {
			return ErrInvalid
		}
	}
	if c.Alerts.DailySpend != "" {
		value, _ := Amount(c.Alerts.DailySpend)
		if !value.IsPositive() {
			return ErrInvalid
		}
	}
	if c.Alerts.Failures < 0 || c.Alerts.Failures > 100 {
		return ErrInvalid
	}
	if c.Alerts.Email != "" {
		address, err := mail.ParseAddress(c.Alerts.Email)
		if err != nil || address.Address != c.Alerts.Email {
			return ErrInvalid
		}
	}
	return nil
}

// validURL permits HTTP roots while excluding credential-bearing and ambiguous input.
func validURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2048 && u.Hostname() != "" && u.User == nil && u.Fragment == "" && u.RawQuery == "" && (u.Scheme == "https" || u.Scheme == "http")
}

// validUnit limits monetary policies to explicitly supported wallet denominations.
func validUnit(unit string) bool { return unit == "USD" || unit == "CNY" || unit == "TOKENS" }

// balanceControl reports whether wallet evidence contributes automatic restriction reasons.
func (c Config) balanceControl() bool {
	return c.Policy.Mode == "balance" || c.Policy.Mode == "combined"
}

// probeControl reports whether required probes contribute automatic restriction reasons.
func (c Config) probeControl() bool { return c.Policy.Mode == "probe" || c.Policy.Mode == "combined" }
