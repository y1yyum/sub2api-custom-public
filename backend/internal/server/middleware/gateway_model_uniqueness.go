package middleware

import (
	"bytes"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/gin-gonic/gin"
)

// GatewayJSONModelUniqueness must run after authentication but before model
// allowlists, composite rewriting, account selection and billing. Admission does
// not depend on an allowlist being enabled. WebSocket frames are checked by the
// Responses handler; multipart/session model semantics remain unchanged.
func GatewayJSONModelUniqueness(maxNormalizedBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil || (c.Request.Method != http.MethodPost && c.Request.Method != http.MethodPut && c.Request.Method != http.MethodPatch) {
			c.Next()
			return
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err == nil {
			requestmodel.ResetRequestBody(c.Request, body)
			// Lenient handlers accept BOM/raw control bytes. Check the same
			// normalized view so a repair cannot conceal duplicates until after
			// routing. Keep the original bytes for the protocol-specific parser.
			candidate := bytes.TrimSpace(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}))
			// Do not normalize arbitrary binary or multipart payloads. Inspect
			// JSON-shaped bodies regardless of a spoofed Content-Type header.
			if len(candidate) > 0 && candidate[0] == '{' {
				var normalized []byte
				normalized, err = httputil.NormalizeLenientJSONRequestBody(body, maxNormalizedBytes)
				if err == nil && requestmodel.HasDuplicateTopLevelModel(normalized) {
					err = requestmodel.ErrDuplicateModelField
				}
			}
		}
		if err == nil {
			c.Next()
			return
		}
		status, message := http.StatusBadRequest, "Failed to read request body"
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status, message = http.StatusRequestEntityTooLarge, "Request body is too large"
		} else if errors.Is(err, requestmodel.ErrDuplicateModelField) {
			message = err.Error()
		}
		path := c.Request.URL.Path
		switch {
		case strings.HasPrefix(path, "/v1beta") || strings.HasPrefix(path, "/antigravity/v1beta"):
			GoogleErrorWriter(c, status, message)
		case strings.Contains(path, "/messages"):
			c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": "invalid_request_error", "message": message}})
		default:
			c.JSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "message": message}})
		}
		c.Abort()
	}
}
