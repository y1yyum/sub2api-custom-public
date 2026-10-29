package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func customBoundaryCompress(t *testing.T, encoding string, body []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	var writer io.WriteCloser
	var err error
	switch encoding {
	case "gzip", "x-gzip":
		writer, err = gzip.NewWriterLevel(&out, gzip.BestSpeed)
	case "deflate":
		writer, err = zlib.NewWriterLevel(&out, zlib.BestSpeed)
	case "zstd":
		writer, err = zstd.NewWriter(&out, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	default:
		t.Fatalf("unsupported fixture encoding %q", encoding)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func customBoundaryRequest(raw []byte, encoding string) *http.Request {
	return &http.Request{
		Body:          io.NopCloser(bytes.NewReader(raw)),
		ContentLength: int64(len(raw)),
		Header: http.Header{
			"Content-Encoding": []string{encoding},
			"Content-Length":   []string{fmt.Sprint(len(raw))},
		},
	}
}

func customBoundaryAssertUnchanged(t *testing.T, req *http.Request, raw []byte, encoding string) {
	t.Helper()
	if req.ContentLength != int64(len(raw)) || req.Header.Get("Content-Encoding") != encoding || req.Header.Get("Content-Length") != fmt.Sprint(len(raw)) {
		t.Fatal("failed decode changed request length or encoding metadata")
	}
}

func TestCustomCompressedBodyBoundaryAndChecksum(t *testing.T) {
	// Sequential subtests keep the real 64 MiB limit covered without parallel
	// allocations. A valid JSON prefix must not make an oversized suffix vanish.
	body := bytes.Repeat([]byte{' '}, maxDecompressedBodySize+1)
	copy(body, `{"input":"original"}`)
	body[maxDecompressedBodySize] = 'x'
	for _, encoding := range []string{"gzip", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			t.Run("limit_minus_one", func(t *testing.T) {
				below := customBoundaryCompress(t, encoding, body[:maxDecompressedBodySize-1])
				req := customBoundaryRequest(below, encoding)
				got, err := ReadRequestBodyWithPrealloc(req)
				if err != nil || !bytes.Equal(got, body[:maxDecompressedBodySize-1]) || req.ContentLength != maxDecompressedBodySize-1 {
					t.Fatalf("below limit changed: bytes=%d err=%v", len(got), err)
				}
			})
			packed := customBoundaryCompress(t, encoding, body[:maxDecompressedBodySize])
			t.Run("exact_limit", func(t *testing.T) {
				req := customBoundaryRequest(packed, encoding)
				got, err := ReadRequestBodyWithPrealloc(req)
				if err != nil || !bytes.Equal(got, body[:maxDecompressedBodySize]) {
					t.Fatalf("exact limit: bytes=%d err=%v", len(got), err)
				}
				if req.ContentLength != maxDecompressedBodySize || req.Header.Get("Content-Encoding") != "" || req.Header.Get("Content-Length") != "" {
					t.Fatal("successful decode did not normalize request metadata")
				}
			})
			t.Run("checksum_at_limit", func(t *testing.T) {
				corrupt := append([]byte(nil), packed...)
				if encoding == "gzip" {
					corrupt[len(corrupt)-8] ^= 1 // gzip CRC32 precedes ISIZE.
				} else {
					corrupt[len(corrupt)-1] ^= 1 // zlib/zstd trailing checksum.
				}
				req := customBoundaryRequest(corrupt, encoding)
				got, err := ReadRequestBodyWithPrealloc(req)
				if err == nil || got != nil {
					t.Fatalf("corrupt exact-limit stream accepted: bytes=%d err=%v", len(got), err)
				}
				if encoding == "gzip" && !errors.Is(err, gzip.ErrChecksum) {
					t.Fatalf("gzip checksum identity lost: %v", err)
				}
				if encoding == "deflate" && !errors.Is(err, zlib.ErrChecksum) {
					t.Fatalf("deflate checksum identity lost: %v", err)
				}
				customBoundaryAssertUnchanged(t, req, corrupt, encoding)
			})
			t.Run("truncated_trailer_at_limit", func(t *testing.T) {
				truncated := packed[:len(packed)-1]
				req := customBoundaryRequest(truncated, encoding)
				got, err := ReadRequestBodyWithPrealloc(req)
				if err == nil || got != nil {
					t.Fatalf("truncated exact-limit stream accepted: bytes=%d err=%v", len(got), err)
				}
				customBoundaryAssertUnchanged(t, req, truncated, encoding)
			})
			t.Run("limit_plus_one", func(t *testing.T) {
				oversize := customBoundaryCompress(t, encoding, body)
				req := customBoundaryRequest(oversize, encoding)
				got, err := ReadRequestBodyWithPrealloc(req)
				var maxErr *http.MaxBytesError
				if !errors.As(err, &maxErr) || maxErr.Limit != maxDecompressedBodySize || got != nil {
					t.Fatalf("oversize body not rejected with wrapped limit error: bytes=%d err=%v", len(got), err)
				}
				customBoundaryAssertUnchanged(t, req, oversize, encoding)
			})
		})
	}
}

func TestCustomCompressedBodySmallEmptyAndEncodingContracts(t *testing.T) {
	for _, encoding := range []string{"gzip", "x-gzip", "deflate", "zstd"} {
		for _, body := range [][]byte{nil, []byte(`{"model":"fixture"}`)} {
			t.Run(fmt.Sprintf("%s/%d", encoding, len(body)), func(t *testing.T) {
				raw := customBoundaryCompress(t, encoding, body)
				// Header normalization remains case/whitespace insensitive.
				req := customBoundaryRequest(raw, " "+strings.ToUpper(encoding)+" ")
				got, err := ReadRequestBodyWithPrealloc(req)
				if err != nil || !bytes.Equal(got, body) || req.ContentLength != int64(len(body)) || req.Header.Get("Content-Encoding") != "" || req.Header.Get("Content-Length") != "" {
					t.Fatalf("successful empty/small decode changed contract: bytes=%d err=%v", len(got), err)
				}
			})
		}
	}
	for _, encoding := range []string{"gzip", "deflate", "zstd", "br", "gzip, deflate"} {
		t.Run("invalid/"+encoding, func(t *testing.T) {
			raw := []byte("not a compressed stream")
			req := customBoundaryRequest(raw, encoding)
			got, err := ReadRequestBodyWithPrealloc(req)
			if err == nil || got != nil {
				t.Fatalf("invalid encoding/stream accepted: bytes=%d err=%v", len(got), err)
			}
			customBoundaryAssertUnchanged(t, req, raw, encoding)
		})
	}
}

type customBoundaryErrorReader struct {
	err error
}

func (r customBoundaryErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestCustomCompressedBodyReadErrorsRemainDiscoverable(t *testing.T) {
	want := errors.New("synthetic reader failure")
	if _, err := readDecompressedRequestBody(customBoundaryErrorReader{err: want}); !errors.Is(err, want) {
		t.Fatalf("decoded reader error lost: %v", err)
	}
	req := customBoundaryRequest(nil, "gzip")
	req.Body = io.NopCloser(customBoundaryErrorReader{err: want})
	got, err := ReadRequestBodyWithPrealloc(req)
	if !errors.Is(err, want) || got != nil {
		t.Fatalf("raw reader error lost: bytes=%d err=%v", len(got), err)
	}
	customBoundaryAssertUnchanged(t, req, nil, "gzip")
}

func TestCustomCompressedBodyIdentityAndPrereadRemainUnchanged(t *testing.T) {
	body := []byte(`{"model":"fixture"}`)
	for _, encoding := range []string{"", "identity"} {
		req := customBoundaryRequest(body, encoding)
		got, err := ReadRequestBodyWithPrealloc(req)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("identity body changed: %q %v", got, err)
		}
		customBoundaryAssertUnchanged(t, req, body, encoding)
	}
	req := customBoundaryRequest(body, "")
	req.Body = NewPrereadBody(body)
	got, err := ReadRequestBodyWithPrealloc(req)
	if err != nil || len(got) == 0 || &got[0] != &body[0] {
		t.Fatalf("preread zero-copy contract changed: %v", err)
	}
}
