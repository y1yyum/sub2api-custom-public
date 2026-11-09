package edge

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

const maxConfigBytes = 2 << 20

type Config struct {
	Version                      int                   `json:"version"`
	Listen                       string                `json:"listen"`
	CoreOrigin                   string                `json:"core_origin"`
	Audience                     string                `json:"audience"`
	AllowLoopbackHTTP            bool                  `json:"allow_loopback_http"`
	MaxBodyBytes                 int64                 `json:"max_body_bytes"`
	SignatureTTLSeconds          int                   `json:"signature_ttl_seconds"`
	RequestTimeoutSeconds        int                   `json:"request_timeout_seconds"`
	DialTimeoutSeconds           int                   `json:"dial_timeout_seconds"`
	ResponseHeaderTimeoutSeconds int                   `json:"response_header_timeout_seconds"`
	SigningKeys                  []KeyConfig           `json:"signing_keys"`
	Grants                       []Grant               `json:"grants"`
	ShadowDispatch               *ShadowDispatchConfig `json:"shadow_dispatch,omitempty"`
	StrictDispatch               *ShadowDispatchConfig `json:"strict_dispatch,omitempty"`
}

type ShadowDispatchConfig struct {
	SnapshotFile             string                      `json:"snapshot_file"`
	SnapshotSubscription     *SnapshotSubscriptionConfig `json:"snapshot_subscription,omitempty"`
	CoreInstance             string                      `json:"core_instance"`
	PollIntervalMilliseconds int                         `json:"poll_interval_milliseconds"`
	RequireLiveLoad          bool                        `json:"require_live_load,omitempty"`
	SelectionSeed            *dispatch.SelectionSeed     `json:"selection_seed,omitempty"`
}

// SnapshotSubscriptionConfig 与 snapshot_file 互斥；poll_interval_milliseconds 此时只作失败后的重试间隔。
type SnapshotSubscriptionConfig struct {
	SocketPath       string `json:"socket_path"`
	KeyID            string `json:"key_id"`
	SecretBase64     string `json:"secret_base64"`
	WaitMilliseconds int    `json:"wait_milliseconds,omitempty"`
	// HardInvalidation 未配置时不订阅失效通道；同一套接字与密钥，独立长轮询。
	HardInvalidation *HardInvalidationSubscriptionConfig `json:"hard_invalidation,omitempty"`
}

type HardInvalidationSubscriptionConfig struct {
	WaitMilliseconds int `json:"wait_milliseconds,omitempty"`
}

type KeyConfig struct {
	ID            string `json:"id"`
	SecretBase64  string `json:"secret_base64"`
	InternalKeyID int64  `json:"internal_key_id"`
	GroupID       int64  `json:"group_id"`
}

type Grant struct {
	ID             string            `json:"grant_id"`
	BearerSHA256   string            `json:"bearer_sha256"`
	SigningKeyID   string            `json:"signing_key_id"`
	AccountID      int64             `json:"account_id"`
	CoreKeyID      int64             `json:"core_key_id"`
	GroupID        int64             `json:"group_id"`
	CoreAPIKey     string            `json:"core_api_key"`
	PoolRef        string            `json:"pool_ref"`
	PolicyRevision string            `json:"policy_revision"`
	PayerRef       string            `json:"payer_ref"`
	ActorRef       string            `json:"actor_ref"`
	Region         string            `json:"region,omitempty"`
	StrictTargets  []dispatch.Permit `json:"strict_targets,omitempty"`
}

// LoadConfig 不把解析器错误原文返回给调用方，避免配置值进入启动日志。
func LoadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, errors.New("无法读取 Edge 配置文件")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(body) > maxConfigBytes {
		return Config{}, errors.New("edge 配置文件读取失败或超过大小限制")
	}
	if err := checkConfigJSON(json.NewDecoder(bytes.NewReader(body)), 0); err != nil {
		return Config{}, errors.New("edge 配置 JSON 非法或包含重复字段")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, errors.New("edge 配置字段或类型无效")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("edge 配置必须为单个 JSON 对象")
	}
	return normalizeConfig(cfg)
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Version != 1 {
		return Config{}, errors.New("edge 配置版本必须为 1")
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8081"
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 0 || portNumber > 65535 {
		return Config{}, errors.New("edge listen 无效")
	}
	if _, err := coreURL(cfg); err != nil {
		return Config{}, err
	}
	if !validReference(cfg.Audience) {
		return Config{}, errors.New("edge audience 无效")
	}
	if cfg.ShadowDispatch != nil && cfg.StrictDispatch != nil {
		return Config{}, errors.New("edge shadow_dispatch 与 strict_dispatch 互斥")
	}
	for _, field := range []**ShadowDispatchConfig{&cfg.ShadowDispatch, &cfg.StrictDispatch} {
		if *field == nil {
			continue
		}
		settings := **field
		if settings.PollIntervalMilliseconds == 0 {
			settings.PollIntervalMilliseconds = 1000
		}
		if settings.SnapshotSubscription != nil {
			if settings.SnapshotFile != "" {
				return Config{}, errors.New("edge dispatch snapshot_file 与 snapshot_subscription 互斥")
			}
			subscription := *settings.SnapshotSubscription
			if subscription.WaitMilliseconds == 0 {
				subscription.WaitMilliseconds = 10000
			}
			_, err := dispatch.DecodeSnapshotSyncSecret(subscription.KeyID, subscription.SecretBase64)
			if err != nil || !dispatch.ValidSnapshotSocketPath(subscription.SocketPath) ||
				subscription.WaitMilliseconds < 1 || subscription.WaitMilliseconds > int(dispatch.MaxSnapshotSyncWait.Milliseconds()) {
				return Config{}, errors.New("edge dispatch 快照订阅套接字、密钥或等待时长无效")
			}
			if subscription.HardInvalidation != nil {
				hard := *subscription.HardInvalidation
				if hard.WaitMilliseconds == 0 {
					hard.WaitMilliseconds = 10000
				}
				if hard.WaitMilliseconds < 1 || hard.WaitMilliseconds > int(dispatch.MaxSnapshotSyncWait.Milliseconds()) {
					return Config{}, errors.New("edge dispatch 硬失效订阅等待时长无效")
				}
				subscription.HardInvalidation = &hard
			}
			settings.SnapshotSubscription = &subscription
		} else if strings.TrimSpace(settings.SnapshotFile) == "" || strings.ContainsRune(settings.SnapshotFile, '\x00') {
			return Config{}, errors.New("edge dispatch 路径、实例或刷新间隔无效")
		}
		if !validReference(settings.CoreInstance) || settings.PollIntervalMilliseconds < 10 || settings.PollIntervalMilliseconds > 30000 {
			return Config{}, errors.New("edge dispatch 路径、实例或刷新间隔无效")
		}
		if settings.SelectionSeed != nil {
			seed := *settings.SelectionSeed
			if err := seed.Validate(); err != nil {
				return Config{}, errors.New("edge dispatch 网关或分片身份无效")
			}
			settings.SelectionSeed = &seed
		}
		*field = &settings
	}
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = 1 << 20
	}
	if cfg.SignatureTTLSeconds == 0 {
		cfg.SignatureTTLSeconds = 30
	}
	if cfg.RequestTimeoutSeconds == 0 {
		cfg.RequestTimeoutSeconds = 300
	}
	if cfg.DialTimeoutSeconds == 0 {
		cfg.DialTimeoutSeconds = 5
	}
	if cfg.ResponseHeaderTimeoutSeconds == 0 {
		cfg.ResponseHeaderTimeoutSeconds = 30
	}
	if cfg.MaxBodyBytes < 1 || cfg.MaxBodyBytes > 16<<20 ||
		cfg.SignatureTTLSeconds < 1 || cfg.SignatureTTLSeconds > 60 ||
		cfg.RequestTimeoutSeconds < cfg.SignatureTTLSeconds || cfg.RequestTimeoutSeconds > 900 ||
		cfg.DialTimeoutSeconds < 1 || cfg.DialTimeoutSeconds > 60 ||
		cfg.ResponseHeaderTimeoutSeconds < 1 || cfg.ResponseHeaderTimeoutSeconds > 300 {
		return Config{}, errors.New("edge 请求大小或超时配置越界")
	}
	keys := make(map[string]KeyConfig, len(cfg.SigningKeys))
	for i, key := range cfg.SigningKeys {
		secret, err := base64.StdEncoding.Strict().DecodeString(key.SecretBase64)
		_, duplicate := keys[key.ID]
		if !validReference(key.ID) || err != nil || len(secret) < 32 || duplicate || key.InternalKeyID <= 0 || key.GroupID <= 0 {
			return Config{}, fmt.Errorf("edge signing_keys 第 %d 项无效或重复", i)
		}
		keys[key.ID] = key
	}
	ids := make(map[string]bool, len(cfg.Grants))
	tokens := make(map[string]bool, len(cfg.Grants))
	cfg.Grants = append([]Grant(nil), cfg.Grants...)
	accountLimits := make(map[int64]int)
	type targetIdentity struct {
		dispatch.Permit
		pool, policy, region string
	}
	targets := make(map[string]targetIdentity)
	for i, grant := range cfg.Grants {
		key, exists := keys[grant.SigningKeyID]
		digest, err := hex.DecodeString(grant.BearerSHA256)
		if !validReference(grant.ID) || ids[grant.ID] || err != nil || len(digest) != 32 ||
			grant.BearerSHA256 != strings.ToLower(grant.BearerSHA256) || tokens[grant.BearerSHA256] ||
			!exists || key.InternalKeyID != grant.CoreKeyID || key.GroupID != grant.GroupID || grant.AccountID <= 0 ||
			!validToken(grant.CoreAPIKey) || !validReference(grant.PoolRef) || !validReference(grant.PolicyRevision) ||
			!validReference(grant.PayerRef) || !validReference(grant.ActorRef) ||
			(grant.Region != "" && !validReference(grant.Region)) || ((cfg.ShadowDispatch != nil || cfg.StrictDispatch != nil) && grant.Region == "") {
			return Config{}, fmt.Errorf("edge grants 第 %d 项无效、重复或超出签名密钥授权", i)
		}
		ids[grant.ID], tokens[grant.BearerSHA256] = true, true
		if cfg.StrictDispatch == nil && len(grant.StrictTargets) != 0 {
			return Config{}, errors.New("edge strict_targets 仅可用于 strict_dispatch")
		}
		if cfg.StrictDispatch != nil {
			if _, err := dispatch.NewPermitSet(grant.StrictTargets); err != nil {
				return Config{}, fmt.Errorf("edge grants 第 %d 项 strict_targets 无效", i)
			}
			for _, permit := range grant.StrictTargets {
				if limit, exists := accountLimits[permit.AccountID]; exists && limit != permit.MaxInflight {
					return Config{}, errors.New("edge strict_targets 账号并发上限不一致")
				}
				accountLimits[permit.AccountID] = permit.MaxInflight
				identity := targetIdentity{permit, grant.PoolRef, grant.PolicyRevision, grant.Region}
				if previous, exists := targets[permit.CandidateRef]; exists && previous != identity {
					return Config{}, errors.New("edge strict_targets 候选身份冲突")
				}
				targets[permit.CandidateRef] = identity
			}
		}
		cfg.Grants[i].StrictTargets = append([]dispatch.Permit(nil), grant.StrictTargets...)
	}
	if len(keys) == 0 || len(cfg.Grants) == 0 {
		return Config{}, errors.New("edge 需要非空 signing_keys 和 grants")
	}
	return cfg, nil
}

func coreURL(cfg Config) (*url.URL, error) {
	origin, err := url.Parse(cfg.CoreOrigin)
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery ||
		origin.Fragment != "" || origin.RawFragment != "" || origin.Opaque != "" || origin.RawPath != "" ||
		(origin.Path != "" && origin.Path != "/") {
		return nil, errors.New("edge core_origin 必须为不含认证、路径或查询的固定源地址")
	}
	if origin.Port() != "" {
		port, err := strconv.Atoi(origin.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("edge core_origin 端口无效")
		}
	}
	if origin.Scheme == "http" && cfg.AllowLoopbackHTTP {
		address, err := netip.ParseAddr(origin.Hostname())
		if err == nil && address.Zone() == "" && address.IsLoopback() {
			return origin, nil
		}
	}
	if origin.Scheme != "https" || origin.Hostname() == "" {
		return nil, errors.New("edge Core 仅允许 HTTPS 或显式启用的字面回环 HTTP")
	}
	return origin, nil
}

func validReference(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.:-", char) {
			return false
		}
	}
	return true
}

func validToken(value string) bool {
	if len(value) == 0 || len(value) > 4096 {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char >= 0x7f {
			return false
		}
	}
	return true
}

func checkConfigJSON(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("配置嵌套过深")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return errors.New("配置分隔符无效")
	}
	fields := make(map[string]bool)
	for decoder.More() {
		if delimiter == '{' {
			field, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := field.(string)
			if !ok || name != strings.ToLower(name) || fields[name] {
				return errors.New("配置字段重复")
			}
			fields[name] = true
		}
		if err := checkConfigJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
