package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"net/http"
)

func registerUpstreamSiteRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUp middleware.StepUpAuthMiddleware) {
	serve := func(c *gin.Context) {
		http.StripPrefix("/api/v1/admin/upstream-sites", h.Admin.UpstreamSites.Handler()).ServeHTTP(c.Writer, c.Request)
	}
	admin.GET("/upstream-sites/*path", serve)
	admin.POST("/upstream-sites/*path", gin.HandlerFunc(stepUp), serve)
	admin.PUT("/upstream-sites/*path", gin.HandlerFunc(stepUp), serve)
	admin.DELETE("/upstream-sites/*path", gin.HandlerFunc(stepUp), serve)
}
