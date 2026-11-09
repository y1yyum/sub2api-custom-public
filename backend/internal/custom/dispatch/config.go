package dispatch

import (
	"encoding/base64"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
)

type Config struct {
	Version                  int                   `json:"version"`
	CoreInstance             string                `json:"core_instance"`
	ConfigRevision           int64                 `json:"config_revision"`
	SnapshotFile             string                `json:"snapshot_file"`
	SnapshotTTLSeconds       int                   `json:"snapshot_ttl_seconds"`
	PollIntervalMilliseconds int                   `json:"poll_interval_milliseconds"`
	Targets                  []Target              `json:"targets"`
	Probe                    *ProbeConfig          `json:"probe,omitempty"`
	Telemetry                *TelemetryConfig      `json:"telemetry,omitempty"`
	Quality                  *QualityConfig        `json:"quality,omitempty"`
	Scheduled                *ScheduledConfig      `json:"scheduled,omitempty"`
	Routing                  *RoutingConfig        `json:"routing,omitempty"`
	HealthWindow             *HealthWindowConfig   `json:"health_window,omitempty"`
	ProbeSchedule            *ProbeScheduleConfig  `json:"probe_schedule,omitempty"`
	ProbeBudget              *ProbeBudgetConfig    `json:"probe_budget,omitempty"`
	SnapshotServer           *SnapshotServerConfig `json:"snapshot_server,omitempty"`
	Monitoring               *MonitoringConfig     `json:"monitoring,omitempty"`
}

type TelemetryConfig struct {
	SlotTTLSeconds    int `json:"slot_ttl_seconds"`
	LoadMaxAgeSeconds int `json:"load_max_age_seconds"`
	MaxObservations   int `json:"max_observations"`
}

type ProbeConfig struct {
	CoreOrigin            string `json:"core_origin"`
	Audience              string `json:"audience"`
	AllowLoopbackHTTP     bool   `json:"allow_loopback_http"`
	CredentialsFile       string `json:"credentials_file"`
	ReceiptSource         string `json:"receipt_source"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
	ReceiptWaitSeconds    int    `json:"receipt_wait_seconds"`
	LeaseSeconds          int    `json:"lease_seconds"`
	GlobalConcurrent      int    `json:"global_concurrent"`
	ProviderRPM           int    `json:"provider_rpm"`
	DailyRequests         int    `json:"daily_requests"`
}

type ProbeCredentials struct {
	KID           string `json:"kid"`
	SecretBase64  string `json:"secret_base64"`
	CoreAPIKey    string `json:"core_api_key"`
	InternalKeyID int64  `json:"internal_key_id"`
	GroupID       int64  `json:"group_id"`
}

func readJSONFile(path string, limit int64, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("dispatch runtime file unavailable")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(body)) > limit {
		return errors.New("dispatch runtime file exceeds read limit")
	}
	if err := decodeJSON(body, value); err != nil {
		return errors.New("invalid dispatch runtime JSON")
	}
	return nil
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	if err := readJSONFile(path, MaxSnapshotBytes, &cfg); err != nil {
		return Config{}, err
	}
	return normalizeConfig(cfg)
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Version != 1 || cfg.ConfigRevision < 1 || !validRef(cfg.CoreInstance) || strings.TrimSpace(cfg.SnapshotFile) == "" || len(cfg.Targets) > MaxCandidates {
		return Config{}, errors.New("invalid dispatch configuration")
	}
	if cfg.SnapshotTTLSeconds == 0 {
		cfg.SnapshotTTLSeconds = 30
	}
	if cfg.PollIntervalMilliseconds == 0 {
		cfg.PollIntervalMilliseconds = 5000
		if cfg.Telemetry != nil {
			cfg.PollIntervalMilliseconds = 1000
		}
	}
	if cfg.SnapshotTTLSeconds < 1 || cfg.SnapshotTTLSeconds > 30 || cfg.PollIntervalMilliseconds < 100 || cfg.PollIntervalMilliseconds >= cfg.SnapshotTTLSeconds*1000 {
		return Config{}, errors.New("invalid dispatch refresh limits")
	}
	refs := map[string]bool{}
	for _, target := range cfg.Targets {
		if err := target.validate(); err != nil {
			return Config{}, err
		}
		if refs[target.Ref] {
			return Config{}, errors.New("duplicate dispatch target")
		}
		refs[target.Ref] = true
	}
	if cfg.Probe != nil {
		probe := *cfg.Probe
		if probe.RequestTimeoutSeconds == 0 {
			probe.RequestTimeoutSeconds = 10
		}
		if probe.ReceiptWaitSeconds == 0 {
			probe.ReceiptWaitSeconds = 3
		}
		if probe.LeaseSeconds == 0 {
			probe.LeaseSeconds = 30
		}
		if probe.GlobalConcurrent == 0 {
			probe.GlobalConcurrent = 1
		}
		if probe.ProviderRPM == 0 {
			probe.ProviderRPM = 5
		}
		if probe.DailyRequests == 0 {
			probe.DailyRequests = 50
		}
		if !validRef(probe.Audience) || !validRef(probe.ReceiptSource) || strings.TrimSpace(probe.CredentialsFile) == "" ||
			probe.RequestTimeoutSeconds < 6 || probe.RequestTimeoutSeconds > 60 || probe.ReceiptWaitSeconds < 1 || probe.ReceiptWaitSeconds > 30 ||
			probe.LeaseSeconds < probe.RequestTimeoutSeconds+probe.ReceiptWaitSeconds+5 || probe.LeaseSeconds > 120 ||
			probe.GlobalConcurrent < 1 || probe.GlobalConcurrent > 16 || probe.ProviderRPM < 1 || probe.ProviderRPM > 100 || probe.DailyRequests < 1 || probe.DailyRequests > 100000 {
			return Config{}, errors.New("invalid dispatch probe limits")
		}
		if _, err := probeOrigin(probe); err != nil {
			return Config{}, err
		}
		cfg.Probe = &probe
	}
	if cfg.Telemetry != nil {
		telemetry := *cfg.Telemetry
		if telemetry.SlotTTLSeconds == 0 {
			telemetry.SlotTTLSeconds = 1800
		}
		if telemetry.LoadMaxAgeSeconds == 0 {
			telemetry.LoadMaxAgeSeconds = 5
		}
		if telemetry.MaxObservations == 0 {
			telemetry.MaxObservations = 10000
		}
		if telemetry.SlotTTLSeconds < 1 || telemetry.SlotTTLSeconds > 86400 ||
			telemetry.LoadMaxAgeSeconds < 1 || telemetry.LoadMaxAgeSeconds > 10 ||
			cfg.PollIntervalMilliseconds >= telemetry.LoadMaxAgeSeconds*1000 ||
			telemetry.MaxObservations < 1 || telemetry.MaxObservations > 100000 {
			return Config{}, errors.New("invalid dispatch telemetry limits")
		}
		cfg.Telemetry = &telemetry
	}
	if cfg.Quality != nil {
		quality, err := normalizeQualityConfig(*cfg.Quality, cfg.Targets)
		if err != nil {
			return Config{}, err
		}
		cfg.Quality = &quality
		if err := validateQualityDeferral(cfg); err != nil {
			return Config{}, err
		}
	}
	if cfg.Scheduled != nil {
		scheduled, err := normalizeScheduledConfig(*cfg.Scheduled)
		if err != nil {
			return Config{}, err
		}
		cfg.Scheduled = &scheduled
	}
	var err error
	cfg.Routing, err = normalizeRoutingConfig(cfg.Routing, cfg.Telemetry)
	if err != nil {
		return Config{}, err
	}
	cfg.HealthWindow, err = normalizeHealthWindowConfig(cfg.HealthWindow, cfg.Routing, cfg.Telemetry)
	if err != nil {
		return Config{}, err
	}
	cfg.ProbeSchedule, err = normalizeProbeScheduleConfig(cfg.ProbeSchedule, cfg.Probe, cfg.HealthWindow, cfg.Telemetry)
	if err != nil {
		return Config{}, err
	}
	cfg.ProbeBudget, err = normalizeProbeBudgetConfig(cfg.ProbeBudget, cfg.Probe, cfg.Targets)
	if err != nil {
		return Config{}, err
	}
	if cfg.SnapshotServer != nil {
		server, err := normalizeSnapshotServerConfig(*cfg.SnapshotServer)
		if err != nil {
			return Config{}, err
		}
		cfg.SnapshotServer = &server
	}
	cfg.Monitoring, err = normalizeMonitoringConfig(cfg.Monitoring)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadProbeCredentials(path, audience string) (ProbeCredentials, error) {
	var creds ProbeCredentials
	if err := readJSONFile(path, 64<<10, &creds); err != nil {
		return creds, err
	}
	if _, err := creds.signingKey(audience); err != nil {
		return ProbeCredentials{}, err
	}
	return creds, nil
}

func (c ProbeCredentials) signingKey(audience string) (custombridge.SigningKey, error) {
	secret, err := base64.StdEncoding.Strict().DecodeString(c.SecretBase64)
	if err != nil || len(secret) < 32 || len(c.CoreAPIKey) < 1 || len(c.CoreAPIKey) > 4096 || strings.IndexFunc(c.CoreAPIKey, func(r rune) bool { return r <= 0x20 || r >= 0x7f }) >= 0 {
		return custombridge.SigningKey{}, errors.New("invalid dispatch service credentials")
	}
	key := custombridge.SigningKey{ID: c.KID, Secret: secret, InternalKeyID: c.InternalKeyID, GroupID: c.GroupID}
	if _, err := custombridge.NewForwardVerifier(audience, []custombridge.SigningKey{key}); err != nil {
		return custombridge.SigningKey{}, errors.New("invalid dispatch service credentials")
	}
	return key, nil
}

func probeOrigin(cfg ProbeConfig) (*url.URL, error) {
	origin, err := url.Parse(cfg.CoreOrigin)
	if err != nil || origin.Hostname() == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.RawFragment != "" || origin.Opaque != "" || origin.RawPath != "" || (origin.Path != "" && origin.Path != "/") {
		return nil, errors.New("invalid dispatch Core origin")
	}
	if origin.Port() != "" {
		port, err := strconv.Atoi(origin.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("invalid dispatch Core port")
		}
	}
	if origin.Scheme == "http" && cfg.AllowLoopbackHTTP {
		address, err := netip.ParseAddr(origin.Hostname())
		if err == nil && address.Zone() == "" && address.IsLoopback() {
			origin.Path = custombridge.ForwardPath
			return origin, nil
		}
	}
	if origin.Scheme != "https" {
		return nil, errors.New("dispatch Core requires HTTPS or explicit literal loopback HTTP")
	}
	origin.Path = custombridge.ForwardPath
	return origin, nil
}

func (c Config) ProbeLimits() ProbeLimits {
	if c.Probe == nil {
		return ProbeLimits{ConfigRevision: c.ConfigRevision}
	}
	return ProbeLimits{ConfigRevision: c.ConfigRevision, LeaseDuration: time.Duration(c.Probe.LeaseSeconds) * time.Second, GlobalConcurrent: c.Probe.GlobalConcurrent, ProviderRPM: c.Probe.ProviderRPM, DailyRequests: c.Probe.DailyRequests}
}

func LoadObservations(path, coreInstance string) ([]Observation, error) {
	var batch struct {
		Version      int           `json:"version"`
		CoreInstance string        `json:"core_instance"`
		Observations []Observation `json:"observations"`
	}
	if err := readJSONFile(path, MaxSnapshotBytes, &batch); err != nil {
		return nil, err
	}
	if batch.Version != 1 || batch.CoreInstance != coreInstance || len(batch.Observations) < 1 || len(batch.Observations) > 1000 {
		return nil, errors.New("invalid dispatch observation batch identity or size")
	}
	return batch.Observations, nil
}
