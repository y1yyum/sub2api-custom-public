package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"

	"github.com/stretchr/testify/require"
)

func TestDispatchRedisSourceSelectionAndRedaction(t *testing.T) {
	cfg := dispatch.TelemetryConfig{SlotTTLSeconds: 1800}
	t.Setenv("DISPATCH_REDIS_URL", "")
	t.Setenv("DISPATCH_REDIS_URL_FILE", "")
	_, _, err := openLoadReader(cfg)
	require.Error(t, err)
	path := filepath.Join(t.TempDir(), "redis-url")
	require.NoError(t, os.WriteFile(path, []byte("redis://127.0.0.1:1/0"), 0600))
	t.Setenv("DISPATCH_REDIS_URL_FILE", path)
	client, reader, err := openLoadReader(cfg)
	require.NoError(t, err)
	require.NotNil(t, reader)
	require.NoError(t, client.Close())
	t.Setenv("DISPATCH_REDIS_URL", "redis://127.0.0.1:1")
	_, _, err = openLoadReader(cfg)
	require.Error(t, err)
	t.Setenv("DISPATCH_REDIS_URL_FILE", "")
	t.Setenv("DISPATCH_REDIS_URL", "invalid-sensitive-source")
	_, _, err = openLoadReader(cfg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sensitive-source")
	var value string
	require.Error(t, readRuntimeSecret(filepath.Dir(path), &value))
	require.Error(t, readRuntimeSecret(path+"-missing", &value))
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 16<<10+1)), 0600))
	require.Error(t, readRuntimeSecret(path, &value))
}

func TestDispatchCommandDefaultsDoNotReadConfigOrConnect(t *testing.T) {
	t.Setenv("DISPATCH_DSN", "invalid-sensitive-placeholder")
	for _, args := range [][]string{nil, {"--config", "missing-file"}, {"--help"}} {
		var stdout, stderr bytes.Buffer
		require.Zero(t, run(context.Background(), args, &stdout, &stderr))
		require.Empty(t, stderr.String())
		require.NotEmpty(t, stdout.String())
		require.NotContains(t, stdout.String(), "sensitive-placeholder")
	}
}

func TestDispatchCommandRejectsAmbiguousActions(t *testing.T) {
	for _, args := range [][]string{{"--once"}, {"--run", "--once", "--config", "missing"}, {"--apply-schema", "--once", "--config", "missing"}, {"--observe-file", "missing"}, {"--unknown"}, {"unexpected"}} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, run(context.Background(), args, &stdout, &stderr))
		require.NotEmpty(t, stderr.String())
	}
}

func TestDispatchQualityCommandRequiresExplicitAction(t *testing.T) {
	for _, args := range [][]string{{"--quality-report"}, {"--quality-report", "--once", "--config", "missing"}, {"--apply-schema", "--apply-quality-schema"}, {"--apply-quality-schema", "--run", "--config", "missing"}} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, run(context.Background(), args, &stdout, &stderr))
		require.Empty(t, stdout.String())
	}
}

func TestDispatchHoldCommandRequiresUnambiguousAction(t *testing.T) {
	t.Setenv("DISPATCH_DSN", "invalid-sensitive-test")
	for _, args := range [][]string{
		{"--health-report"},
		{"--health-report", "--once", "--config", "missing"},
		{"--health-report", "--health-holds", "--config", "missing"},
		{"--health-report", "--apply-schema", "--config", "missing"},
		{"--health-holds"},
		{"--release-hold-file", "missing"},
		{"--health-holds", "--once", "--config", "missing"},
		{"--health-holds", "--release-hold-file", "missing", "--config", "missing"},
		{"--release-hold-file", "missing", "--observe-file", "missing", "--config", "missing"},
		{"--release-hold-file", "missing", "--apply-schema", "--config", "missing"},
		{"--health-holds", "--quality-report", "--config", "missing"},
	} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, run(context.Background(), args, &stdout, &stderr))
		require.Empty(t, stdout.String())
		require.NotContains(t, stderr.String(), "sensitive-test")
	}
}

func TestDispatchHoldReleaseRejectsWrongConfigBeforeStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	for _, body := range []string{
		`{"core_instance":"other","config_revision":1,"hold_id":"hold","hold_revision":1,"operation_id":"op","actor":"operator","reason":"已核对","evidence_ref":"review-1"}`,
		`{"core_instance":"core","config_revision":2,"hold_id":"hold","hold_revision":1,"operation_id":"op","actor":"operator","reason":"已核对","evidence_ref":"review-1"}`,
		`{"core_instance":"core","Core_Instance":"core"}`,
		`{"core_instance":"core","config_revision":1,"unknown":"sensitive-test"}`,
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0600))
		var stdout, stderr bytes.Buffer
		require.Equal(t, 1, runHealthHoldAction(t.Context(), nil, dispatch.Config{CoreInstance: "core", ConfigRevision: 1}, false, path, &stdout, &stderr))
		require.Empty(t, stdout.String())
		require.NotContains(t, stderr.String(), "sensitive-test")
	}
}

func TestDispatchCommandConnectionFailureRedactsDSN(t *testing.T) {
	t.Setenv("DISPATCH_DSN", "postgres://sensitive-placeholder:sensitive-placeholder@127.0.0.1:1/missing?sslmode=disable&connect_timeout=1")
	var stdout, stderr bytes.Buffer
	require.Equal(t, 1, run(context.Background(), []string{"--apply-schema"}, &stdout, &stderr))
	require.False(t, strings.Contains(stderr.String(), "sensitive-placeholder"))
}
