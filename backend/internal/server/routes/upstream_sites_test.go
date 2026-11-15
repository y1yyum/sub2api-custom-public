package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpstreamSitesRequireAdminAndStepUp(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			group := r.Group("/api/v1/admin")
			group.Use(func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
			registerUpstreamSiteRoutes(group, &handler.Handlers{}, func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/admin/upstream-sites/sites", nil))
			require.Equal(t, 401, w.Code)
			if method == "GET" {
				return
			}
			r = gin.New()
			group = r.Group("/api/v1/admin")
			registerUpstreamSiteRoutes(group, &handler.Handlers{}, func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) })
			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/admin/upstream-sites/sites", nil))
			require.Equal(t, 403, w.Code)
		})
	}
}
