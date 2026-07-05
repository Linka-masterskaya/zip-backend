package middleware

import "net/http"

// Middleware описывает сигнатуру стандартной функции-мидлвари.
type Middleware func(http.Handler) http.Handler

// Chain последовательно оборачивает базовый http.Handler в переданный список мидлварей.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
