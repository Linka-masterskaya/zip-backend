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

// GetUserID extracts authenticated user ID from context.
func GetUserID(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(ctxKeyUserID{}).(string)
	return userID, ok && userID != ""
}

// PutUserID returns a context with authenticated user ID.
func PutUserID(ctx context.Context, userID string) context.Context {
	if userID == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyUserID{}, userID)
}
