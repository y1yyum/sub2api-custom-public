package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// unifiedModelChannelStatusGuard hides the catalog endpoints behind their own
// opt-in setting. A disabled switch answers like a disabled V2 monitor so the
// two cannot be told apart from outside; read errors fail closed.
func unifiedModelChannelStatusGuard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService == nil || !settingService.GetUnifiedModelChannelStatusEnabled(c.Request.Context()) {
			response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
			c.Abort()
			return
		}
		c.Next()
	}
}
