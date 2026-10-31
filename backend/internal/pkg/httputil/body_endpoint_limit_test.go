package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

type endpointDecodedCounter struct {
	io.Reader
	read int
}

func (r *endpointDecodedCounter) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.read += n
	return n, e
}
func TestEndpointDecodedReaderStopsAtLimitProbe(t *testing.T) {
	r := &endpointDecodedCounter{Reader: strings.NewReader(strings.Repeat("x", 4096))}
	_, err := readDecompressedRequestBodyWithLimit(r, 64)
	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) || maxErr.Limit != 64 || r.read != 65 {
		t.Fatalf("err=%v consumed=%d want65", err, r.read)
	}
}
func TestEndpointDecodedLimitAndExactBoundaryChecksum(t *testing.T) {
	for _, codec := range []string{"gzip", "deflate", "zstd"} {
		for _, size := range []int{63, 64, 65} {
			for _, corrupt := range []bool{false, true} {
				payload := strings.Repeat("x", size)
				var compressed bytes.Buffer
				if codec == "gzip" {
					w := gzip.NewWriter(&compressed)
					_, _ = w.Write([]byte(payload))
					if e := w.Close(); e != nil {
						t.Fatal(e)
					}
				} else if codec == "zstd" {
					w, e := zstd.NewWriter(&compressed, zstd.WithEncoderCRC(true))
					if e != nil {
						t.Fatal(e)
					}
					_, _ = w.Write([]byte(payload))
					if e := w.Close(); e != nil {
						t.Fatal(e)
					}
				} else {
					w := zlib.NewWriter(&compressed)
					_, _ = w.Write([]byte(payload))
					if e := w.Close(); e != nil {
						t.Fatal(e)
					}
				}
				wire := append([]byte(nil), compressed.Bytes()...)
				if corrupt {
					wire[len(wire)-1] ^= 0xff
				}
				req := httptest.NewRequest(http.MethodPost, "/text", bytes.NewReader(wire))
				req.Header.Set("Content-Encoding", codec)
				got, err := ReadRequestBodyWithPreallocLimit(req, 64)
				if corrupt {
					// A decoder may surface integrity failure before the outer size probe.
					if err == nil {
						t.Fatalf("%s size%d checksum ignored", codec, size)
					}
				} else if size > 64 {
					var maxErr *http.MaxBytesError
					if !errors.As(err, &maxErr) {
						t.Fatalf("%s size%d corrupt%v expectedsizeerror: %v", codec, size, corrupt, err)
					}

				} else if err != nil || string(got) != payload {
					t.Fatalf("%s size%d: %v", codec, size, err)
				}
				if err != nil && got != nil {
					t.Fatal("failed body exposed to JSON scanner")
				}
			}
		}
	}
}

func TestEndpointConfiguredIdentityLimitDoesNotInheritCompressionCeiling(t *testing.T) {
	// Serial subtests deliberately exercise the real 64MiB expansion boundary.
	payload := bytes.Repeat([]byte("x"), maxDecompressedBodySize+1)
	endpointLimit := int64(len(payload))
	for _, preread := range []bool{false, true} {
		name := "raw_identity"
		if preread {
			name = "preread_identity"
		}
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/text", bytes.NewReader(payload))
			if preread {
				req.Body = NewPrereadBody(payload)
			}
			got, err := ReadRequestBodyWithPreallocLimit(req, endpointLimit)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("configured identity boundary changed: len=%d err=%v", len(got), err)
			}
		})
	}
	t.Run("compressed_still_has_global_expansion_ceiling", func(t *testing.T) {
		var wire bytes.Buffer
		w := gzip.NewWriter(&wire)
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/text", bytes.NewReader(wire.Bytes()))
		req.Header.Set("Content-Encoding", "gzip")
		got, err := ReadRequestBodyWithPreallocLimit(req, endpointLimit)
		var maxErr *http.MaxBytesError
		if !errors.As(err, &maxErr) || maxErr.Limit != maxDecompressedBodySize || got != nil {
			t.Fatalf("global expansion ceiling changed: len=%d err=%v", len(got), err)
		}
	})
}
