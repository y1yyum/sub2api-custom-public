package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func BridgeTelemetry(producer *custombridge.TelemetryProducer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == custombridge.ForwardPath && producer != nil {
			c.Request = c.Request.WithContext(custombridge.WithTelemetryProducer(c.Request.Context(), producer))
		}
		c.Next()
	}
}

func BridgeForward(verifier *custombridge.ForwardVerifier, attempts custombridge.AttemptStore, allowLoopbackHTTP bool, maxBody int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !bridgeSecurePeer(c.Request, allowLoopbackHTTP) {
			bridgeForwardError(c, http.StatusForbidden, "bridge_transport_required")
			return
		}
		key, ok := GetAPIKeyFromContext(c)
		if !ok || key == nil || key.GroupID == nil || key.Group == nil || key.Group.Platform != service.PlatformOpenAI {
			bridgeForwardError(c, http.StatusForbidden, "bridge_identity_denied")
			return
		}
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			bridgeForwardError(c, http.StatusBadRequest, "bridge_json_required")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxBody))
		if err != nil {
			var limit *http.MaxBytesError
			var timeout net.Error
			if errors.As(err, &limit) {
				bridgeForwardError(c, http.StatusRequestEntityTooLarge, "bridge_body_limit")
			} else if errors.As(err, &timeout) && timeout.Timeout() {
				bridgeForwardError(c, http.StatusRequestTimeout, "bridge_body_timeout")
			} else {
				bridgeForwardError(c, http.StatusBadRequest, "bridge_body_unreadable")
			}
			return
		}
		envelope, err := verifier.Verify(c.Request, body, custombridge.ForwardPrincipal{InternalKeyID: key.ID, GroupID: *key.GroupID}, time.Now())
		if err != nil {
			bridgeForwardError(c, http.StatusUnauthorized, "bridge_signature_invalid")
			return
		}
		coreID := service.CaptureBridgeCoreRequestID(c.Request.Context(), "")
		if err := attempts.Receive(c.Request.Context(), envelope, coreID); err != nil {
			if errors.Is(err, custombridge.ErrAttemptReplay) || errors.Is(err, custombridge.ErrAttemptConflict) {
				bridgeForwardError(c, http.StatusConflict, "bridge_attempt_exists")
			} else {
				bridgeForwardError(c, http.StatusServiceUnavailable, "bridge_storage_unavailable")
			}
			return
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := attempts.Finish(ctx, key.ID, envelope.AttemptID); err != nil {
				slog.Warn("bridge attempt finalization failed")
			}
		}()
		ctx, cancel := context.WithDeadline(c.Request.Context(), time.Unix(envelope.Deadline, 0))
		defer cancel()
		deadline, _ := ctx.Deadline()
		controller := http.NewResponseController(c.Writer)
		if err := controller.SetWriteDeadline(deadline); err != nil {
			bridgeForwardError(c, http.StatusServiceUnavailable, "bridge_write_deadline_unavailable")
			return
		}
		ctx = custombridge.WithCoreRequestID(ctx, coreID)
		ctx = custombridge.WithRequest(ctx, custombridge.Request{ReservationID: envelope.ReservationID, AttemptID: envelope.AttemptID, AccountID: envelope.AccountID})
		ctx = custombridge.WithSendGuard(ctx, func(ctx context.Context) error { return attempts.ClaimSend(ctx, key.ID, envelope.AttemptID) })
		ctx, observation := custombridge.TelemetryProducerFromContext(ctx).Begin(ctx, envelope, coreID)
		if observation != nil {
			defer func() {
				finishCtx, finishCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer finishCancel()
				if err := observation.Finish(finishCtx, c.Writer.Status(), ctx.Err()); err != nil {
					category := "storage_unavailable"
					if errors.Is(err, custombridge.ErrTelemetryFenced) {
						category = "source_fenced"
					}
					slog.Warn("bridge telemetry finalization failed", "category", category)
				}
			}()
		}
		c.Request = c.Request.WithContext(ctx)
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Request.ContentLength = int64(len(body))
		c.Request.GetBody = nil
		// 只允许固定的协议头影响 Core；桥接认证材料不进入后续日志或 Provider。
		c.Request.Header = http.Header{"Content-Type": {"application/json"}, "Accept": {c.GetHeader("Accept")}, "User-Agent": {"sub2api-bridge/1"}}
		c.Request.URL.Path = envelope.Endpoint
		c.Request.URL.RawPath = ""
		c.Header("X-Bridge-Request-ID", envelope.RequestID)
		c.Header("X-Bridge-Reservation-ID", envelope.ReservationID)
		c.Header("X-Bridge-Attempt-ID", envelope.AttemptID)
		c.Next()
	}
}

func bridgeSecurePeer(request *http.Request, allowLoopbackHTTP bool) bool {
	if request.TLS != nil {
		return true
	}
	if !allowLoopbackHTTP {
		return false
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return false
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func bridgeForwardError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": "bridge_error", "code": code, "message": "Bridge request rejected"}})
}
