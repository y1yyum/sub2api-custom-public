//go:build embed

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBridgeForwardBypassesEmbeddedFrontend(t *testing.T) {
	frontend, err := NewFrontendServer(&mockSettingsProvider{settings: map[string]string{}})
	require.NoError(t, err)
	for _, frontendMiddleware := range []gin.HandlerFunc{frontend.Middleware(), ServeEmbeddedFrontend()} {
		for _, enabled := range []bool{true, false} {
			router := gin.New()
			router.Use(frontendMiddleware)
			called := false
			if enabled {
				router.POST(custombridge.ForwardPath, func(c *gin.Context) {
					called = true
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signature_required"})
				})
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, custombridge.ForwardPath, nil))
			if enabled {
				require.True(t, called)
				require.Equal(t, http.StatusUnauthorized, response.Code)
				require.Contains(t, response.Body.String(), "signature_required")
			} else {
				require.Equal(t, http.StatusNotFound, response.Code)
			}
			require.NotContains(t, response.Header().Get("Content-Type"), "text/html")
		}
	}
}
