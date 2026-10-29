package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGatewayModelUniquenessBeforeRoutingAndBilling(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/responses", "/v1/chat/completions", "/v1/messages", "/messages/count_tokens", "/v1/embeddings", "/v1/images/generations", "/backend-api/codex/responses", "/v1beta/models/test:generateContent", "/antigravity/v1/messages"} {
		for _, body := range []string{`{"model":"a","model":"b"}`, `{"model":"a","model":"a"}`, `{"mo\u0064el":null,"MODEL":"a"}`, "\xef\xbb\xbf{\"model\":\"a\",\"model\":\"a\"}", "{\"model\":\"a\",\"model\":\"a\",\"input\":\"raw\nnewline\"}"} {
			r := gin.New()
			reached := false
			r.POST("/*path", GatewayJSONModelUniqueness(4096), GroupModelAllowlist(), func(c *gin.Context) {
				reached = true // Composite rewrite, scheduling and billing must not run.
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest || reached || !strings.Contains(w.Body.String(), "duplicate model field") {
				t.Fatalf("%s: code=%d reached=%v body=%s", path, w.Code, reached, w.Body.String())
			}
		}
	}
}

func TestGatewayModelUniquenessPreservesSingleAndNestedBody(t *testing.T) {
	body := `{"model":"a","input":{"model":"b"}}`
	r := gin.New()
	r.POST("/v1/responses", GatewayJSONModelUniqueness(4096), func(c *gin.Context) {
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil || string(raw) != body {
			t.Fatalf("body changed: %q err=%v", raw, err)
		}
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("valid request rejected: %d", w.Code)
	}
}

func TestGatewayModelUniquenessPreservesReadLimit(t *testing.T) {
	r := gin.New()
	r.POST("/v1/responses", RequestBodyLimit(8), GatewayJSONModelUniqueness(4096), func(c *gin.Context) { t.Fatal("oversized body reached handler") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"long"}`)))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", w.Code)
	}
}

func TestGatewayModelUniquenessDoesNotTrustContentType(t *testing.T) {
	for _, contentType := range []string{"application/json", "text/plain", "multipart/form-data; boundary=synthetic", "application/octet-stream"} {
		r := gin.New()
		r.POST("/v1/responses", GatewayJSONModelUniqueness(4096), func(c *gin.Context) { t.Fatal("duplicate reached handler") })
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"a","model":"a"}`))
		req.Header.Set("Content-Type", contentType)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d", contentType, w.Code)
		}
	}
}

func TestGatewayModelUniquenessDoesNotNormalizeBinaryOrMultipart(t *testing.T) {
	for _, body := range []string{"\x00\x01\xff\xfe", "--synthetic\r\nContent-Disposition: form-data; name=\"session\"\r\n\r\n{\"model\":\"a\",\"model\":\"b\"}\r\n--synthetic--\r\n"} {
		r := gin.New()
		r.POST("/v1/images/edits", GatewayJSONModelUniqueness(1), func(c *gin.Context) {
			raw, err := io.ReadAll(c.Request.Body)
			if err != nil || string(raw) != body {
				t.Fatalf("non-JSON body changed: %q err=%v", raw, err)
			}
			c.Status(http.StatusNoContent)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(body)))
		if w.Code != http.StatusNoContent {
			t.Fatalf("non-JSON body rejected: %d", w.Code)
		}
	}
}

func TestGatewayModelUniquenessEndpointExactBoundary(t *testing.T) {
	for _, extra := range []string{"", " "} {
		body := `{"model":"a","padding":"` + strings.Repeat("x", 38) + `"}`
		limit := int64(len(body))
		body += extra
		r := gin.New()
		r.POST("/text", GatewayJSONModelUniquenessWithEndpointLimits(4096, map[string]int64{"/text": limit}), func(c *gin.Context) {
			raw, err := io.ReadAll(c.Request.Body)
			if err != nil || string(raw) != body {
				t.Fatalf("accepted body changed: %q %v", raw, err)
			}
			c.Status(http.StatusNoContent)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/text", strings.NewReader(body)))
		want := http.StatusNoContent
		if extra != "" {
			want = http.StatusRequestEntityTooLarge
		}
		if w.Code != want {
			t.Fatalf("extra %q: status=%d want=%d", extra, w.Code, want)
		}
	}
}

func TestGatewayModelUniquenessEndpointCompressedExpansion(t *testing.T) {
	payload := `{"padding":"` + strings.Repeat("x", 4096) + `","model":"a","model":"b"}`
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	_, _ = zip.Write([]byte(payload))
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	if compressed.Len() >= 256 {
		t.Fatal("fixture wire must fit endpoint cap")
	}
	r := gin.New()
	r.POST("/text", GatewayJSONModelUniquenessWithEndpointLimits(8192, map[string]int64{"/text": 256}), func(c *gin.Context) { t.Fatal("oversized decoded body reached handler") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/text", bytes.NewReader(compressed.Bytes()))
	req.Header.Set("Content-Encoding", "gzip")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge || strings.Contains(w.Body.String(), "duplicate model field") {
		t.Fatalf("decoded cap must precede scan: %d %s", w.Code, w.Body.String())
	}
}
