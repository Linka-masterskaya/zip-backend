package reqctx

import "context"

type ctxKeyRequestID struct{}

type ctxKeyUserID struct{}

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

// GetUserID извлекает ID аутентифицированного пользователя из контекста.
func GetUserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKeyUserID{}).(string)
	return id, ok
}

// PutUserID возвращает новый контекст с установленным ID пользователя.
func PutUserID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyUserID{}, id)
}
