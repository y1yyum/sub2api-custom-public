package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerCatalogStripConfigRoutes serves the strip rules of the "models and
// channels" page.
func registerCatalogStripConfigRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	admin.GET("/catalog-strip-config", h.ChannelStatusCatalog.GetStripConfig)
	admin.PUT("/catalog-strip-config", h.ChannelStatusCatalog.UpdateStripConfig)
}
