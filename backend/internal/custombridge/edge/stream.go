package edge

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

func responseHeaderAllowed(name string, connectionFields map[string]bool) bool {
	name = strings.ToLower(name)
	if connectionFields[name] || strings.HasPrefix(name, "x-bridge-") || strings.HasPrefix(name, "x-internal-") || strings.HasPrefix(name, "x-account-") {
		return false
	}
	switch name {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "authorization", "x-api-key", "api-key", "set-cookie", "x-edge-request-id":
		return false
	default:
		return true
	}
}

func copyResponse(ctx context.Context, writer http.ResponseWriter, response *http.Response) error {
	controller := http.NewResponseController(writer)
	deadline, _ := ctx.Deadline()
	if err := controller.SetWriteDeadline(deadline); err != nil {
		return err
	}
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(canceled)
		_ = controller.SetWriteDeadline(time.Now())
	})
	defer func() {
		// 回调须在返回前结束；写期限保留给 net/http 的终结块和 trailer 刷新。
		if !stop() {
			<-canceled
		}
	}()

	connectionFields := make(map[string]bool)
	for _, value := range response.Header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			connectionFields[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	for name, values := range response.Header {
		if responseHeaderAllowed(name, connectionFields) {
			writer.Header()[name] = append([]string(nil), values...)
		}
	}
	for name := range response.Trailer {
		if responseHeaderAllowed(name, connectionFields) && !strings.EqualFold(name, "Content-Length") {
			writer.Header().Add("Trailer", name)
		}
	}
	writer.WriteHeader(response.StatusCode)
	if err := controller.Flush(); err != nil {
		return err
	}
	_, err := io.Copy(flushWriter{writer, controller}, response.Body)
	if err != nil {
		return err
	}
	for name, values := range response.Trailer {
		if responseHeaderAllowed(name, connectionFields) && !strings.EqualFold(name, "Content-Length") {
			writer.Header()[name] = append([]string(nil), values...)
		}
	}
	return nil
}

type flushWriter struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
}

func (w flushWriter) Write(body []byte) (int, error) {
	n, err := w.writer.Write(body)
	if err == nil {
		err = w.controller.Flush()
	}
	return n, err
}
