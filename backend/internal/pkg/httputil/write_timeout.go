package httputil

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// WithWriteTimeout bounds individual writes and flushes without extending an
// explicit deadline. Between operations it restores the explicit deadline, or
// clears the operation deadline to permit arbitrarily long thinking intervals.
// Wrapping below Gin also covers all its ResponseWriter decorators and keeps
// HTTP/2 deadlines scoped to the individual stream.
func WithWriteTimeout(next http.Handler, timeout time.Duration) http.Handler {
	if timeout <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		writer := &writeTimeoutWriter{ResponseWriter: w, timeout: timeout, cancel: cancel}
		next.ServeHTTP(writer, r.WithContext(ctx))
	})
}

type writeTimeoutWriter struct {
	http.ResponseWriter
	timeout          time.Duration
	externalDeadline time.Time
	cancel           context.CancelFunc
	err              error
}

func (w *writeTimeoutWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *writeTimeoutWriter) SetWriteDeadline(deadline time.Time) error {
	err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
	if err == nil {
		w.externalDeadline = deadline
	}
	return err
}

func (w *writeTimeoutWriter) start() error {
	if w.err != nil {
		return w.err
	}
	deadline := time.Now().Add(w.timeout)
	if !w.externalDeadline.IsZero() && w.externalDeadline.Before(deadline) {
		deadline = w.externalDeadline
	}
	err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.fail(err)
	}
	return w.err
}

func (w *writeTimeoutWriter) finish() {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(w.externalDeadline)
}

func (w *writeTimeoutWriter) fail(err error) {
	if err != nil {
		w.err = err
		w.cancel()
	}
}

func (w *writeTimeoutWriter) Write(p []byte) (int, error) {
	if err := w.start(); err != nil {
		return 0, err
	}
	defer w.finish()
	n, err := w.ResponseWriter.Write(p)
	w.fail(err)
	return n, err
}

func (w *writeTimeoutWriter) WriteString(s string) (int, error) {
	if err := w.start(); err != nil {
		return 0, err
	}
	defer w.finish()
	n, err := io.WriteString(w.ResponseWriter, s)
	w.fail(err)
	return n, err
}

func (w *writeTimeoutWriter) WriteHeader(status int) {
	if w.start() != nil {
		return
	}
	defer w.finish()
	w.ResponseWriter.WriteHeader(status)
}

func (w *writeTimeoutWriter) FlushError() error {
	if err := w.start(); err != nil {
		return err
	}
	defer w.finish()
	err := http.NewResponseController(w.ResponseWriter).Flush()
	w.fail(err)
	return err
}

func (w *writeTimeoutWriter) Flush() { _ = w.FlushError() }

func (w *writeTimeoutWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *writeTimeoutWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (w *writeTimeoutWriter) CloseNotify() <-chan bool {
	if notifier, ok := w.ResponseWriter.(http.CloseNotifier); ok { //nolint:staticcheck // Gin still exposes CloseNotify; preserve legacy writer compatibility.
		return notifier.CloseNotify()
	}
	return make(chan bool)
}
