// Package auth contains authentication handlers and services.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"golang.org/x/oauth2"
)

type Handler struct {
	service     *Service
	cache       *cache.Client
	oauthCfg    *oauth2.Config
	frontendURL string
}

func NewHandler(service *Service, cache *cache.Client, oauthCfg *oauth2.Config, frontendURL string) *Handler {
	return &Handler{
		service:     service,
		cache:       cache,
		oauthCfg:    oauthCfg,
		frontendURL: frontendURL,
	}
}

// GET /auth/yandex
func (h *Handler) YandexLogin(w http.ResponseWriter, r *http.Request) {
	// 1. Генерируем state
	// 2. Сохраняем в Redis с TTL 5 минут
	// 3. Формируем URL для редиректа в Яндекс
	// 4. http.Redirect(w, r, url, http.StatusTemporaryRedirect)
	ctx := r.Context()
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "Failed to generate state", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(stateBytes)
	key := fmt.Sprintf("auth:yandex:state:%s", state)
	if err := h.cache.SetString(ctx, key, state, 5*time.Minute); err != nil {
		http.Error(w, "Failed to save state", http.StatusInternalServerError)
		return
	}
	url := h.oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// GET /auth/yandex/callback
func (h *Handler) YandexCallback(w http.ResponseWriter, r *http.Request) {
	// 1. Получаем code и state из query params
	// 2. Проверяем state в Redis
	// 3. Обмениваем code на access_token
	// 4. Запрашиваем профиль пользователя (email, name, yandex_id)
	// 5. Вызываем service.UpsertUser(...)
	// 6. Генерируем JWT
	// 7. Редиректим на фронтенд с JWT
	ctx := r.Context()
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing code or state", http.StatusBadRequest)
		return
	}
	key := fmt.Sprintf("auth:yandex:state:%s", state)
	savedState, err := h.cache.GetString(ctx, key)
	if err != nil {
		http.Error(w, "Invalid or expired state", http.StatusForbidden)
		return
	}
	if savedState != state {
		http.Error(w, "State mismatch", http.StatusForbidden)
		return
	}
	_ = h.cache.Del(ctx, key)
	token, err := h.oauthCfg.Exchange(ctx, code)
	if err != nil {
		http.Error(w, "Failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	client := h.oauthCfg.Client(ctx, token)
	resp, err := client.Get("https://login.yandex.ru/info?format=json")
	if err != nil {
		http.Error(w, "Failed to fetch user info: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Yandex API error: "+resp.Status, http.StatusInternalServerError)
		return
	}

	var yandexUser struct {
		ID        string `json:"id"`
		Email     string `json:"default_email"`
		Name      string `json:"display_name"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&yandexUser); err != nil {
		http.Error(w, "Failed to parse user info: "+err.Error(), http.StatusInternalServerError)
		return
	}

	name := yandexUser.Name
	if name == "" {
		name = yandexUser.FirstName + " " + yandexUser.LastName
		name = strings.TrimSpace(name)
	}
	if name == "" {
		name = yandexUser.Email
	}
	user, userAuth, err := h.service.UpsertUser(ctx, yandexUser.Email, name, yandexUser.ID)
	if err != nil {
		http.Error(w, "Failed to upsert user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	tokenString, err := h.service.GenerateJWT(user, userAuth)
	if err != nil {
		http.Error(w, "Failed to generate JWT: "+err.Error(), http.StatusInternalServerError)
		return
	}

	frontendURL := h.frontendURL
	redirectURL := fmt.Sprintf("%s/auth/callback?token=%s", frontendURL, tokenString)
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}
