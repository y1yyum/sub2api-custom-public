// Package httpresponse preserves transport errors through response-writer wrappers.
package httpresponse

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Flush observes FlushError where supported. Gin's ResponseWriter carrier exposes
// both void Flush and Unwrap; unwrap that known carrier only. Other wrappers must
// explicitly implement FlushError to preserve their own buffering/lifecycle work.
// In particular, never bypass an arbitrary HTTP wrapper merely for having Unwrap.
func Flush(w http.ResponseWriter) error {
	for w != nil {
		if f, ok := w.(interface{ FlushError() error }); ok {
			return f.FlushError()
		}
		if carrier, ok := w.(gin.ResponseWriter); ok {
			if u, ok := carrier.(interface{ Unwrap() http.ResponseWriter }); ok {
				carrier.WriteHeaderNow()
				w = u.Unwrap()
				continue
			}
		}
		return http.NewResponseController(w).Flush()
	}
	return http.ErrNotSupported
}
