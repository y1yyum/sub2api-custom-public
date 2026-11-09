//go:build unit

package server

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBridgeTelemetryStartupRejectsUnsafeSlotTTL(t *testing.T) {
	dir := t.TempDir()
	forward := map[string]any{"schema_version": 1, "audience": "core", "keys": []map[string]any{{"id": "key", "secret_base64": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))), "internal_key_id": 1, "group_id": 1}}}
	encoded, err := json.Marshal(forward)
	require.NoError(t, err)
	forwardPath := filepath.Join(dir, "forward.json")
	require.NoError(t, os.WriteFile(forwardPath, encoded, 0600))
	telemetry := custombridge.TelemetryConfig{SchemaVersion: 1, CoreInstance: "core", ConfigRevision: 1, Targets: []custombridge.TelemetryTarget{{
		TargetRef: "target", AccountID: 1, CredentialRef: "credential", CredentialRevision: 1, CredentialSHA256: strings.Repeat("a", 64), ProviderRef: "provider", PoolRef: "pool", PolicyRevision: "policy", Model: "model", Region: "region",
	}}}
	encoded, err = json.Marshal(telemetry)
	require.NoError(t, err)
	telemetryPath := filepath.Join(dir, "telemetry.json")
	require.NoError(t, os.WriteFile(telemetryPath, encoded, 0600))
	t.Setenv("SUB2API_BRIDGE_ENABLED", "true")
	t.Setenv("SUB2API_BRIDGE_CONFIG", forwardPath)
	t.Setenv("SUB2API_BRIDGE_TELEMETRY_CONFIG", telemetryPath)
	for _, minutes := range []int{-1, 0, 1441, int(^uint(0) >> 1)} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectQuery("SELECT a.request_digest").WillReturnRows(sqlmock.NewRows([]string{"request_digest", "send_intent_at", "receipt_id"}))
		app := &config.Config{RunMode: config.RunModeStandard}
		app.Gateway.MaxBodySize = 1024
		app.Gateway.ConcurrencySlotTTLMinutes = minutes
		err = registerConfiguredBridge(gin.New(), nil, nil, nil, nil, app, db)
		require.ErrorContains(t, err, "slot TTL")
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	}
}

func TestBridgeTelemetryDisabledStartupDoesNotReadConfiguration(t *testing.T) {
	t.Setenv("SUB2API_BRIDGE_ENABLED", "false")
	t.Setenv("SUB2API_BRIDGE_TELEMETRY_CONFIG", "does-not-exist")
	require.NoError(t, registerConfiguredBridge(gin.New(), nil, nil, nil, nil, nil, nil))
}
