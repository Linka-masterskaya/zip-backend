package reqctx

import "context"

type ctxKeyRequestID struct{}

// GetRequestID извлекает уникальный ID запроса из контекста выполнения.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyRequestID{}).(string); ok {
		return id
	}
	return ""
}

// PutRequestID возвращает новый контекст с установленным request ID.
func PutRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyRequestID{}, id)
}
