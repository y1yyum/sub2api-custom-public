//go:build unit

package server

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBridgeForwardDisabledDoesNotRequireStorage(t *testing.T) {
	t.Setenv("SUB2API_BRIDGE_ENABLED", "false")
	t.Setenv("SUB2API_BRIDGE_CONFIG", "does-not-exist")
	router := gin.New()
	require.NoError(t, registerConfiguredBridge(router, nil, nil, nil, nil, nil, nil))
	require.Empty(t, router.Routes())
}
