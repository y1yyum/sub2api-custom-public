package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type channelStatusCatalogProvider interface {
	Catalog(ctx context.Context, q service.CatalogQuery) (*service.ChannelStatusCatalog, error)
	Details(ctx context.Context, q service.CatalogQuery, key service.CatalogRowKey) (*service.CatalogDetails, error)
}

type channelStatusFilterParser interface {
	ParseFilter(rangeValue string, platforms, models []string, groupIDs []int64) (service.ChannelMonitorV2Filter, error)
}

type channelStatusViewerScope interface {
	GetAvailableGroups(ctx context.Context, userID int64) ([]service.Group, error)
	GetUserGroupRates(ctx context.Context, userID int64) (map[int64]float64, error)
}

// ChannelStatusCatalogHandler serves the unified "models and channels" catalog.
// Visibility comes only from the authenticated user's available groups.
type ChannelStatusCatalogHandler struct {
	catalog channelStatusCatalogProvider
	parser  channelStatusFilterParser
	viewer  channelStatusViewerScope
}

func NewChannelStatusCatalogHandler(
	catalog *service.ChannelStatusCatalogService,
	monitor *service.ChannelMonitorV2Service,
	apiKeyService *service.APIKeyService,
) *ChannelStatusCatalogHandler {
	return &ChannelStatusCatalogHandler{catalog: catalog, parser: monitor, viewer: apiKeyService}
}

// catalogDetailRange is the fixed window of the detail view.
const catalogDetailRange = "24h"

// GET /api/v1/channel-monitor-v2/catalog
func (h *ChannelStatusCatalogHandler) Catalog(c *gin.Context) {
	rangeValue, ok := singleQueryValue(c, "range")
	if !ok {
		response.BadRequest(c, "invalid range")
		return
	}
	groups, err := parseChannelMonitorV2GroupIDs(queryList(c, "group_id"))
	if err != nil {
		response.BadRequest(c, "invalid group_id")
		return
	}
	filter, err := h.parser.ParseFilter(rangeValue, queryList(c, "platform"), queryList(c, "model"), groups)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	q, ok := h.scopedQuery(c, filter)
	if !ok {
		return
	}
	c.Writer.Header().Add("Vary", "Accept-Language")
	result, err := h.catalog.Catalog(c.Request.Context(), q)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// GET /api/v1/channel-monitor-v2/catalog/details
func (h *ChannelStatusCatalogHandler) Details(c *gin.Context) {
	groupRaw, ok1 := singleQueryValue(c, "group_id")
	platform, ok2 := singleQueryValue(c, "platform")
	model, ok3 := singleQueryValue(c, "model")
	groupID, convErr := strconv.ParseInt(groupRaw, 10, 64)
	if !ok1 || !ok2 || !ok3 || convErr != nil {
		response.BadRequest(c, "invalid catalog row key")
		return
	}
	key, valid := service.ParseCatalogRowKey(groupID, platform, model)
	if !valid {
		response.BadRequest(c, "invalid catalog row key")
		return
	}
	filter, err := h.parser.ParseFilter(catalogDetailRange, nil, nil, nil)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	q, ok := h.scopedQuery(c, filter)
	if !ok {
		return
	}
	c.Writer.Header().Add("Vary", "Accept-Language")
	result, err := h.catalog.Details(c.Request.Context(), q, key)
	if err != nil {
		if errors.Is(err, service.ErrCatalogRowNotFound) {
			response.NotFound(c, "catalog row not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// singleQueryValue returns the only value of key, "" when absent, and false
// when the parameter is repeated.
func singleQueryValue(c *gin.Context, key string) (string, bool) {
	values := c.QueryArray(key)
	switch len(values) {
	case 0:
		return "", true
	case 1:
		return values[0], true
	}
	return "", false
}

// scopedQuery derives the viewer scope server-side from the auth subject.
func (h *ChannelStatusCatalogHandler) scopedQuery(c *gin.Context, filter service.ChannelMonitorV2Filter) (service.CatalogQuery, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return service.CatalogQuery{}, false
	}
	if h.viewer == nil {
		response.Error(c, http.StatusInternalServerError, "channel status catalog authorization unavailable")
		return service.CatalogQuery{}, false
	}
	groups, err := h.viewer.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return service.CatalogQuery{}, false
	}
	filter.RestrictGroups = true
	filter.AllowedGroupIDs = make([]int64, 0, len(groups))
	for i := range groups {
		filter.AllowedGroupIDs = append(filter.AllowedGroupIDs, groups[i].ID)
	}
	rates, err := h.viewer.GetUserGroupRates(c.Request.Context(), subject.UserID)
	if err != nil {
		// Personal rates only enrich the display; fall back to group rates.
		slog.Warn("channel_status_catalog_user_rates_failed", "error", err, "user_id", subject.UserID)
		rates = nil
	}
	return service.CatalogQuery{Filter: filter, UserRates: rates, AcceptLanguage: c.GetHeader("Accept-Language")}, true
}
