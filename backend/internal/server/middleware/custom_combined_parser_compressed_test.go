package middleware

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestCustomCombinedParserCompressedAdmission(t *testing.T) {
	const decodedLimit = 64 << 20
	body := bytes.Repeat([]byte{' '}, decodedLimit+1)
	copy(body, `{"model":"fixture"}`)
	pack := func(body []byte) []byte {
		t.Helper()
		var out bytes.Buffer
		w, err := gzip.NewWriterLevel(&out, gzip.BestSpeed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	corrupt := pack(body[:decodedLimit])
	corrupt[len(corrupt)-8] ^= 1
	for _, tc := range []struct {
		name   string
		packed []byte
		status int
		calls  int
	}{
		{name: "same_value_duplicate", packed: pack([]byte(`{"model":"fixture","model":"fixture"}`)), status: http.StatusBadRequest},
		{name: "different_value_duplicate", packed: pack([]byte(`{"model":"fixture","model":"other"}`)), status: http.StatusBadRequest},
		{name: "valid_control", packed: pack([]byte(`{"model":"fixture"}`)), status: http.StatusOK, calls: 1},
		{name: "decoded_limit_plus_one", packed: pack(body), status: http.StatusRequestEntityTooLarge},
		{name: "checksum_at_decoded_limit", packed: corrupt, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No listener, external account or real billing backend is used. The
			// production admission middleware must abort before either sentinel.
			router := gin.New()
			billingCalls, upstreamCalls := 0, 0
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{
					Platform: service.PlatformOpenAI,
					ModelAllowlist: service.GroupModelAllowlist{
						Enabled: true,
						Models:  []string{"fixture"},
					},
				}})
				c.Next()
			})
			router.Use(GatewayJSONModelUniqueness(decodedLimit + 1))
			router.Use(GroupModelAllowlist())
			router.Use(func(c *gin.Context) {
				billingCalls++
				c.Next()
			})
			router.POST("/v1/responses", func(c *gin.Context) {
				upstreamCalls++
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(tc.packed))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Content-Encoding", "gzip")
			rec := httptest.NewRecorder()
			// Compose both production admission guards; wire bytes fit the limit.
			if len(tc.packed) >= 1<<20 {
				t.Fatal("compressed fixture unexpectedly exceeds wire limit")
			}
			req.Body = http.MaxBytesReader(rec, req.Body, 1<<20)
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status || billingCalls != tc.calls || upstreamCalls != tc.calls {
				t.Fatalf("status=%d want=%d billing=%d upstream=%d wantCalls=%d body=%s", rec.Code, tc.status, billingCalls, upstreamCalls, tc.calls, rec.Body.String())
			}
			if tc.status != http.StatusOK {
				if !strings.Contains(rec.Body.String(), "invalid_request_error") {
					t.Fatal("rejection lost API error")
				}
			}
		})
	}
}
