package custombridge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

type ForwardConfig struct {
	Version           int    `json:"schema_version"`
	Audience          string `json:"audience"`
	AllowLoopbackHTTP bool   `json:"allow_loopback_http"`
	Keys              []struct {
		ID            string `json:"id"`
		SecretBase64  string `json:"secret_base64"`
		InternalKeyID int64  `json:"internal_key_id"`
		GroupID       int64  `json:"group_id"`
	} `json:"keys"`
}

// LoadForwardConfigFromEnv 关闭时完全不读取配置文件或要求桥接存储。
func LoadForwardConfigFromEnv() (*ForwardConfig, error) {
	enabled := os.Getenv("SUB2API_BRIDGE_ENABLED")
	if enabled == "" {
		return nil, nil
	}
	on, err := strconv.ParseBool(enabled)
	if err != nil {
		return nil, errors.New("invalid SUB2API_BRIDGE_ENABLED")
	}
	if !on {
		return nil, nil
	}
	path := os.Getenv("SUB2API_BRIDGE_CONFIG")
	if path == "" {
		return nil, errors.New("SUB2API_BRIDGE_CONFIG is required when bridge is enabled")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("invalid bridge configuration path")
	}
	root, err := os.OpenRoot(filepath.Dir(resolved))
	if err != nil {
		return nil, errors.New("cannot open bridge configuration directory")
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.Base(resolved))
	if err != nil {
		return nil, errors.New("cannot open bridge configuration")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("cannot read bounded bridge configuration")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cfg ForwardConfig
	if err := decoder.Decode(&cfg); err != nil {
		return nil, errors.New("invalid bridge configuration")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing bridge configuration")
	}
	if _, err := cfg.Verifier(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c ForwardConfig) Verifier() (*ForwardVerifier, error) {
	if c.Version != ForwardVersion {
		return nil, errors.New("unsupported bridge configuration version")
	}
	keys := make([]SigningKey, 0, len(c.Keys))
	for _, configured := range c.Keys {
		secret, err := base64.StdEncoding.DecodeString(configured.SecretBase64)
		if err != nil {
			return nil, errors.New("invalid bridge signing secret encoding")
		}
		keys = append(keys, SigningKey{ID: configured.ID, Secret: secret, InternalKeyID: configured.InternalKeyID, GroupID: configured.GroupID})
	}
	return NewForwardVerifier(c.Audience, keys)
}
