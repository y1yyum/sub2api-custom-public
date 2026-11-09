package custombridge

import "context"

// Request 是受信内部调用的约束，不从公开请求头解析。
type Request struct {
	ReservationID string
	AttemptID     string
	AccountID     int64
}

type requestKey struct{}
type coreRequestIDKey struct{}

func WithRequest(ctx context.Context, request Request) context.Context {
	return context.WithValue(ctx, requestKey{}, request)
}

func RequestFromContext(ctx context.Context) (Request, bool) {
	request, ok := ctx.Value(requestKey{}).(Request)
	return request, ok
}

func WithCoreRequestID(ctx context.Context, coreRequestID string) context.Context {
	return context.WithValue(ctx, coreRequestIDKey{}, coreRequestID)
}

func CoreRequestIDFromContext(ctx context.Context) (string, bool) {
	coreRequestID, ok := ctx.Value(coreRequestIDKey{}).(string)
	return coreRequestID, ok
}
