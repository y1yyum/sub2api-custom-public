package dispatch

import (
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
)

// MonitoringConfig 只影响本进程的指标、聚合与清理，不进入共享调度摘要，
// 因而启停不改变配置版本、快照或 probe policy。
type MonitoringConfig struct {
	MetricsListen string             `json:"metrics_listen,omitempty"`
	Aggregation   *AggregationConfig `json:"aggregation,omitempty"`
	Retention     *RetentionConfig   `json:"retention,omitempty"`
}

// AggregationConfig 的两个保留期都必须由操作者给出；聚合表不能无限增长。
type AggregationConfig struct {
	MinuteRetentionDays int `json:"minute_retention_days"`
	HourRetentionDays   int `json:"hour_retention_days"`
}

// RetentionConfig 中 0 表示该表不清理；整个块缺省时不删除任何原始历史。
type RetentionConfig struct {
	ObservationDays int `json:"observation_days,omitempty"`
	ProbeDays       int `json:"probe_days,omitempty"`
}

const (
	maxMinuteRetentionDays = 31
	maxHourRetentionDays   = 400
	maxRawRetentionDays    = 366
)

func normalizeMonitoringConfig(cfg *MonitoringConfig) (*MonitoringConfig, error) {
	if cfg == nil {
		return nil, nil
	}
	if cfg.MetricsListen == "" && cfg.Aggregation == nil && cfg.Retention == nil {
		return nil, errors.New("dispatch monitoring requires metrics_listen, aggregation or retention")
	}
	if cfg.MetricsListen != "" {
		if _, _, err := MetricsListenAddress(cfg.MetricsListen); err != nil {
			return nil, err
		}
	}
	if a := cfg.Aggregation; a != nil {
		if a.MinuteRetentionDays < 1 || a.MinuteRetentionDays > maxMinuteRetentionDays ||
			a.HourRetentionDays < a.MinuteRetentionDays || a.HourRetentionDays > maxHourRetentionDays {
			return nil, errors.New("invalid dispatch metric aggregation retention")
		}
	}
	if r := cfg.Retention; r != nil {
		if r.ObservationDays == 0 && r.ProbeDays == 0 ||
			r.ObservationDays < 0 || r.ObservationDays > maxRawRetentionDays ||
			r.ProbeDays < 0 || r.ProbeDays > maxRawRetentionDays {
			return nil, errors.New("invalid dispatch history retention")
		}
	}
	return cfg, nil
}

// MetricsListenAddress 只接受字面回环 TCP 地址或绝对路径 Unix socket，
// 不解析主机名，避免指标端点因 DNS 或通配地址暴露到外部网络。
func MetricsListenAddress(value string) (network, address string, err error) {
	if path, ok := strings.CutPrefix(value, "unix:"); ok {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 100 {
			return "", "", errors.New("dispatch metrics socket must be a clean absolute path")
		}
		return "unix", path, nil
	}
	addr, err := netip.ParseAddrPort(value)
	if err != nil || addr.Port() == 0 || addr.Addr().Zone() != "" || !addr.Addr().IsLoopback() {
		return "", "", errors.New("dispatch metrics requires a literal loopback address or unix socket")
	}
	return "tcp", addr.String(), nil
}
