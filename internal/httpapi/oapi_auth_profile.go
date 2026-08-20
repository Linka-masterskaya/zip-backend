package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/oapiserver"
	"github.com/Linka-masterskaya/zip-backend/internal/profile"
)

// authProfileOAPIServer binds generated auth/profile server stubs to the
// existing handlers. Runtime route registration remains in auth.go/profile.go
// because those routes have endpoint-specific authentication and rate limits.
// The compile-time assertion below makes an OpenAPI/codegen signature change
// fail the build until the application handlers are synchronized.
type authProfileOAPIServer struct {
	auth           *auth.Handler
	profile        *profile.Handler
	changePassword *profile.ChangePasswordHandler
}

var _ oapiserver.ServerInterface = (*authProfileOAPIServer)(nil)

func serveOAPI(h middleware.AppHandler, w http.ResponseWriter, r *http.Request) {
	middleware.ErrorMiddleware(h).ServeHTTP(w, r)
}

func (s *authProfileOAPIServer) Register(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.auth.Register, w, r)
}

func (s *authProfileOAPIServer) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.auth.VerifyEmail, w, r)
}

func (s *authProfileOAPIServer) ResendVerifyEmail(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.auth.ResendEmail, w, r)
}

func (s *authProfileOAPIServer) Login(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.auth.Login, w, r)
}

func (s *authProfileOAPIServer) Refresh(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.auth.Refresh, w, r)
}

func (s *authProfileOAPIServer) Logout(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.auth.Logout, w, r)
}

func (s *authProfileOAPIServer) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.auth.ForgotPassword, w, r)
}

func (s *authProfileOAPIServer) ResetPassword(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.auth.ResetPassword, w, r)
}

func (s *authProfileOAPIServer) GetProfile(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.profile.GetProfile, w, r)
}

func (s *authProfileOAPIServer) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.profile.UpdateProfile, w, r)
}

func (s *authProfileOAPIServer) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.profile.DeleteProfile, w, r)
}

func (s *authProfileOAPIServer) ReplaceAvatar(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.profile.UploadAvatar, w, r)
}

func (s *authProfileOAPIServer) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.profile.DeleteAvatar, w, r)
}

func (s *authProfileOAPIServer) ChangePassword(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.changePassword.ChangePassword, w, r)
}

func (s *authProfileOAPIServer) RequestEmailChange(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.profile.RequestEmailChange, w, r)
}

func (s *authProfileOAPIServer) ConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	serveOAPI(s.profile.ConfirmEmailChange, w, r)
}
