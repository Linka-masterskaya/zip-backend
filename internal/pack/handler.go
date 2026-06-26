package pack

import (
	"encoding/json"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

// Handler обрабатывает входящие HTTP-запросы для выполнения операций над паками.
type Handler struct {
	service *Service
}

// NewHandler создает новый экземпляр контроллера Handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

type createPackRequest struct {
	Name string `json:"name"`
}

// CreatePack парсит тело запроса, валидирует данные и инициирует создание пака.
func (h *Handler) CreatePack(w http.ResponseWriter, r *http.Request) error {
	var req createPackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest
	}

	if req.Name == "" {
		return apperr.ErrBadRequest
	}

	res, err := h.service.Create(r.Context(), req.Name)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	return json.NewEncoder(w).Encode(res)
}

// GetPack извлекает идентификатор из пути запроса и запрашивает данные пака.
func (h *Handler) GetPack(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if id == "" {
		id = r.URL.Query().Get("id")
	}

	if id == "" {
		return apperr.ErrBadRequest
	}

	res, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(res)
}

// ListPacks запрашивает список паков у сервисного слоя и отдает клиенту в формате JSON.
func (h *Handler) ListPacks(w http.ResponseWriter, r *http.Request) error {
	res, err := h.service.List(r.Context())
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(res)
}
