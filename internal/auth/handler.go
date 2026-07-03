package auth

import (
	"net/http"
)

type Handler struct {
	svc Service
}

// NewAuthHandler создает обработчик HTTP-запросов для аутентификации.
func NewAuthHandler(svc Service) *Handler {
	return &Handler{
		svc: svc,
	}
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}

func (h *Handler) VerifyResend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}

func (h *Handler) EmailConfirm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}
