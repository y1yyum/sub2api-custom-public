//go:build unit || integration

package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type countedGatewayBody struct {
	io.Reader
	consumed int
}

func (b *countedGatewayBody) Read(p []byte) (int, error) {
	n, e := b.Reader.Read(p)
	b.consumed += n
	return n, e
}
func (*countedGatewayBody) Close() error { return nil }

func TestGatewayModelGuardFirstReadUsesEndpointLimit(t *testing.T) {
	for _, path := range []string{"/v1/systemone", "/v1/alpha/search", "/v1/embeddings", "/alpha/search", "/embeddings", "/backend-api/codex/alpha/search", "/v1/responses"} {
		for _, knownLength := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/chunked", true: "/length"}[knownLength], func(t *testing.T) {
				const textLimit = 64
				const globalLimit = 4096
				router := gin.New()
				RegisterGatewayRoutes(router, &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}, AsyncImage: handler.NewAsyncImageHandler(nil, nil)}, middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
					id := int64(1)
					c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &id, Group: &service.Group{Platform: service.PlatformOpenAI}})
					c.Next()
				}), nil, nil, nil, nil, nil, &config.Config{Gateway: config.GatewayConfig{MaxBodySize: globalLimit, TextMaxBodySize: textLimit}})
				// Duplicates after the text boundary must not be scanned before rejection.
				payload := `{"padding":"` + strings.Repeat("x", 256) + `","model":"a","model":"b"}`
				body := &countedGatewayBody{Reader: strings.NewReader(payload)}
				req := httptest.NewRequest(http.MethodPost, path, nil)
				req.Body = body
				req.ContentLength = -1
				if knownLength {
					req.ContentLength = int64(len(payload))
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if path == "/v1/responses" {
					require.Equal(t, http.StatusBadRequest, w.Code)
					require.Contains(t, w.Body.String(), "duplicate model field")
					require.Equal(t, len(payload), body.consumed)
				} else {
					require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
					require.LessOrEqual(t, body.consumed, textLimit+1, "first reader must not consume global-limit payload")
					require.NotContains(t, w.Body.String(), "duplicate model field")
				}
			})
		}
	}
}
