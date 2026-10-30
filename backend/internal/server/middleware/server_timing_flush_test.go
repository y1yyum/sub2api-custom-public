package middleware

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type timingErrorFlusher struct {
	*httptest.ResponseRecorder
	err   error
	calls int
}

func (w *timingErrorFlusher) FlushError() error { w.calls++; return w.err }
func TestServerTimingFlushErrorPreservesTransportAndFinalize(t *testing.T) {
	want := errors.New("fixture flush failure")
	underlying := &timingErrorFlusher{ResponseRecorder: httptest.NewRecorder(), err: want}
	c, _ := gin.CreateTestContext(underlying)
	w := &serverTimingResponseWriter{ResponseWriter: c.Writer, context: c}
	require.ErrorIs(t, w.FlushError(), want)
	require.Equal(t, 1, underlying.calls)
	// finalize uses once; reaching it in FlushError must consume that guard.
	called := false
	w.once.Do(func() { called = true })
	require.False(t, called)
}
