package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func RegisterBridgeGatewayRoutes(r *gin.Engine, h *handler.Handlers, auth middleware.APIKeyAuthMiddleware,
	settings *service.SettingService, ops *service.OpsService, cfg *config.Config,
	bridgeConfig *custombridge.ForwardConfig, verifier *custombridge.ForwardVerifier, attempts custombridge.AttemptStore) {
	if bridgeConfig == nil {
		return
	}
	r.POST(custombridge.ForwardPath,
		middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize), middleware.ClientRequestID(),
		handler.OpsErrorLoggerMiddleware(ops), gin.HandlerFunc(auth),
		middleware.BridgeForward(verifier, attempts, bridgeConfig.AllowLoopbackHTTP, cfg.Gateway.MaxBodySize),
		handler.InboundEndpointMiddleware(), middleware.GatewayJSONModelUniqueness(cfg.Gateway.MaxBodySize), middleware.GroupModelAllowlist(),
		middleware.RequireGroupAssignment(settings, middleware.AnthropicErrorWriter), h.OpenAIGateway.Responses)
}
