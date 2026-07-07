// Package auth contains authentication handlers and services.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
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

type yandexUserInfo struct {
	ID        string `json:"id"`
	Email     string `json:"default_email"`
	Name      string `json:"display_name"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func (h *Handler) YandexLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "Failed to generate state", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(stateBytes)
	if err := h.cache.SaveOAuthState(ctx, state, 5*time.Minute); err != nil {
		http.Error(w, "Failed to save state", http.StatusInternalServerError)
		return
	}
	url := h.oauthCfg.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (h *Handler) YandexCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing code or state", http.StatusBadRequest)
		return
	}

	if err := h.validateState(ctx, state); err != nil {
		http.Error(w, "Invalid or expired state", http.StatusForbidden)
		return
	}

	token, err := h.exchangeCode(ctx, code)
	if err != nil {
		slog.Error("failed to exchange token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	yandexUser, err := h.fetchUserInfo(ctx, token)
	if err != nil {
		slog.Error("failed to fetch user info", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	name := h.buildDisplayName(yandexUser)
	user, userAuth, err := h.service.UpsertUser(ctx, yandexUser.Email, name, yandexUser.ID)
	if err != nil {
		slog.Error("failed to upsert user", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	tokenString, err := h.service.GenerateJWT(user, userAuth)
	if err != nil {
		slog.Error("failed to generate JWT", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    tokenString,
		HttpOnly: true,
		Secure:   true,
		Path:     "/",
		MaxAge:   86400,
	})
	http.Redirect(w, r, h.frontendURL, http.StatusSeeOther)
}

func (h *Handler) validateState(ctx context.Context, state string) error {
	savedState, err := h.cache.GetOAuthState(ctx, state)
	if err != nil {
		slog.Error("failed to get state from cache",
			slog.Any("error", err),
		)
		return fmt.Errorf("invalid or expired state")
	}
	if savedState != state {
		slog.Warn("state mismatch")
		return fmt.Errorf("state mismatch")
	}
	if err := h.cache.DeleteOAuthState(ctx, state); err != nil {
		slog.Warn("failed to delete oauth state",
			slog.Any("error", err),
		)
	}
	return nil
}

// exchangeCode exchanges the OAuth code for an access token.
func (h *Handler) exchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	return h.oauthCfg.Exchange(ctx, code)
}

// fetchUserInfo fetches user information from Yandex API.
func (h *Handler) fetchUserInfo(ctx context.Context, token *oauth2.Token) (*yandexUserInfo, error) {
	client := h.oauthCfg.Client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://login.yandex.ru/info?format=json", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yandex API error: %s", resp.Status)
	}

	var yandexUser yandexUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&yandexUser); err != nil {
		return nil, fmt.Errorf("failed to parse user info: %w", err)
	}
	return &yandexUser, nil
}

// buildDisplayName builds a display name from Yandex user info.
func (h *Handler) buildDisplayName(user *yandexUserInfo) string {
	if user.Name != "" {
		return user.Name
	}
	if user.FirstName != "" || user.LastName != "" {
		name := strings.TrimSpace(user.FirstName + " " + user.LastName)
		if name != "" {
			return name
		}
	}
	return user.Email
}
