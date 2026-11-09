//go:build unit

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBridgeSchemaTelemetryRequiresExplicitAction(t *testing.T) {
	t.Setenv("BRIDGE_CORE_DSN", "")
	require.NoError(t, run(nil))
	require.NoError(t, run([]string{"--dsn-file", "does-not-exist"}))
	require.ErrorContains(t, run([]string{"--apply", "--apply-telemetry"}), "mutually exclusive")
	require.ErrorContains(t, run([]string{"--apply-telemetry"}), "DSN is required")
	require.ErrorContains(t, run([]string{"--apply"}), "DSN is required")
	require.ErrorContains(t, run([]string{"--apply-telemetry", "--dsn-file", "does-not-exist"}), "cannot read")
	require.ErrorContains(t, run([]string{"--apply-telemetry", "unexpected"}), "unexpected")
}
