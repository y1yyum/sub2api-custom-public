package server

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func registerConfiguredBridge(r *gin.Engine, handlers *handler.Handlers, auth middleware.APIKeyAuthMiddleware,
	settings *service.SettingService, ops *service.OpsService, cfg *config.Config, db *sql.DB) error {
	bridgeConfig, err := custombridge.LoadForwardConfigFromEnv()
	if err != nil {
		return err
	}
	if bridgeConfig == nil {
		return nil
	}
	if cfg.RunMode != config.RunModeStandard || cfg.Gateway.MaxBodySize <= 0 {
		return errors.New("bridge requires standard mode and a positive body limit")
	}
	if db == nil {
		return errors.New("bridge database is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 迁移须由显式命令先完成；开启入口不隐式变更 Core 数据库。
	rows, err := db.QueryContext(ctx, `SELECT a.request_digest, a.send_intent_at, o.receipt_id FROM bridge.forward_attempt a CROSS JOIN bridge.receipt_outbox o LIMIT 0`)
	if err != nil {
		return errors.New("bridge schema is unavailable; apply explicit bridge migrations first")
	}
	if err := rows.Close(); err != nil {
		return errors.New("bridge schema check failed")
	}
	verifier, err := bridgeConfig.Verifier()
	if err != nil {
		return err
	}
	telemetryConfig, err := custombridge.LoadTelemetryConfigFromEnv()
	if err != nil {
		return err
	}
	if telemetryConfig != nil {
		if cfg.Gateway.ConcurrencySlotTTLMinutes < 1 || cfg.Gateway.ConcurrencySlotTTLMinutes > 1440 {
			return errors.New("bridge telemetry requires a slot TTL between 1 and 1440 minutes")
		}
		producer, err := custombridge.ActivateTelemetry(ctx, db, *telemetryConfig, cfg.Gateway.ConcurrencySlotTTLMinutes*60)
		if err != nil {
			return err
		}
		r.Use(middleware.BridgeTelemetry(producer))
	}
	routes.RegisterBridgeGatewayRoutes(r, handlers, auth, settings, ops, cfg, bridgeConfig, verifier, custombridge.NewForwardAttemptRepository(db))
	return nil
}
