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

type combinedAdmissionCountingReader struct {
	io.Reader
	bytes int
}

func (r *combinedAdmissionCountingReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.bytes += n
	return n, e
}

func TestCustomCombinedFirstReaderEndpointAndDecodeContracts(t *testing.T) {
	const limit = 1024
	pack := func(body string) []byte {
		t.Helper()
		var out bytes.Buffer
		w := gzip.NewWriter(&out)
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	valid := `{"model":"fixture"}`
	exact := valid + strings.Repeat(" ", limit-len(valid))
	corrupt := pack(exact)
	corrupt[len(corrupt)-8] ^= 1
	cases := []struct {
		name      string
		body      []byte
		encoding  string
		status    int
		duplicate bool
	}{
		{"raw_exact", []byte(exact), "", http.StatusNoContent, false},
		{"raw_late_duplicate_over_cap", []byte(exact + `{"model":"a","model":"b"}`), "", http.StatusRequestEntityTooLarge, false},
		{"decoded_exact", pack(exact), "gzip", http.StatusNoContent, false},
		{"decoded_over_cap_before_duplicate", pack(exact + `{"model":"a","model":"b"}`), "gzip", http.StatusRequestEntityTooLarge, false},
		{"checksum_at_decoded_cap", corrupt, "gzip", http.StatusBadRequest, false},
		{"duplicate_within_cap", pack(`{"model":"a","model":"a"}`), "gzip", http.StatusBadRequest, true},
	}
	for _, tc := range cases {
		for _, knownLength := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/unknown_length", true: "/known_length"}[knownLength], func(t *testing.T) {
				source := &combinedAdmissionCountingReader{Reader: bytes.NewReader(tc.body)}
				req := httptest.NewRequest(http.MethodPost, "/text", source)
				if knownLength {
					req.ContentLength = int64(len(tc.body))
				} else {
					req.ContentLength = -1
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Content-Encoding", tc.encoding)
				calls := 0
				router := gin.New()
				router.POST("/text", GatewayJSONModelUniquenessWithEndpointLimits(8192, map[string]int64{"/text": limit}), GroupModelAllowlist(), func(c *gin.Context) { calls++; c.Status(http.StatusNoContent) })
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != tc.status {
					t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
				}
				if source.bytes > limit+1 {
					t.Fatalf("first reader consumed %d raw bytes before rejection", source.bytes)
				}
				if strings.Contains(rec.Body.String(), "duplicate model field") != tc.duplicate {
					t.Fatalf("unexpected parser precedence: %s", rec.Body.String())
				}
				wantCalls := 0
				if tc.status == http.StatusNoContent {
					wantCalls = 1
				}
				if calls != wantCalls {
					t.Fatalf("downstream calls=%d want=%d", calls, wantCalls)
				}
			})
		}
	}
}
