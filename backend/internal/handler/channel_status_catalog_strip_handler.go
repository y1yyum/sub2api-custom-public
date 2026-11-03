package handler

import (
	"context"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type catalogStripConfigStore interface {
	GetCatalogStripConfig(ctx context.Context) service.CatalogStripConfig
	UpdateCatalogStripConfig(ctx context.Context, cfg service.CatalogStripConfig) (service.CatalogStripConfig, error)
}

// GET /api/v1/admin/catalog-strip-config
func (h *ChannelStatusCatalogHandler) GetStripConfig(c *gin.Context) {
	response.Success(c, h.strip.GetCatalogStripConfig(c.Request.Context()))
}

// PUT /api/v1/admin/catalog-strip-config
func (h *ChannelStatusCatalogHandler) UpdateStripConfig(c *gin.Context) {
	var req service.CatalogStripConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	saved, err := h.strip.UpdateCatalogStripConfig(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		slog.Info("catalog_strip_config_updated", "admin_id", subject.UserID, "mode", saved.Mode)
	}
	response.Success(c, saved)
}
