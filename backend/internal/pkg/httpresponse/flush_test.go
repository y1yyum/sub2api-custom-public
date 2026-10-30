package httpresponse

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type errorWriter struct {
	*httptest.ResponseRecorder
	err   error
	calls int
}

func (w *errorWriter) FlushError() error { w.calls++; return w.err }

type bufferedWrapper struct {
	http.ResponseWriter
	flushed bool
}

func (w *bufferedWrapper) Flush()                      { w.flushed = true }
func (w *bufferedWrapper) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestFlushPreservesGinTransportError(t *testing.T) {
	want := errors.New("fixture flush failure")
	w := &errorWriter{ResponseRecorder: httptest.NewRecorder(), err: want}
	c, _ := gin.CreateTestContext(w)
	require.False(t, c.Writer.Written(), "constructing the chain must not commit headers")
	_, err := c.Writer.Write([]byte("prior output"))
	require.NoError(t, err)
	require.ErrorIs(t, Flush(c.Writer), want)
	require.Equal(t, 1, w.calls)
	require.Equal(t, "prior output", w.Body.String())
}
func TestFlushRetainsLegacyFlusher(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	require.NoError(t, Flush(c.Writer))
	require.True(t, w.Flushed)
}
func TestFlushDoesNotBypassOpaqueBufferingWrapper(t *testing.T) {
	transport := &errorWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("must not bypass buffer")}
	w := &bufferedWrapper{ResponseWriter: transport}
	require.NoError(t, Flush(w))
	require.True(t, w.flushed)
	require.Zero(t, transport.calls)
}
