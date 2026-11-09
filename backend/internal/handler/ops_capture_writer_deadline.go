package handler

import (
	"errors"
	"net/http"
	"time"
)

func (w *opsCaptureWriter) SetWriteDeadline(deadline time.Time) error {
	state, writer := w.beginDelegatedCall()
	if state == nil {
		return errors.New("response writer released")
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	return http.NewResponseController(writer).SetWriteDeadline(deadline)
}
