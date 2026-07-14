package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
)

func TestRequireRole(t *testing.T) {
	noop := AppHandler(func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})

	tests := []struct {
		name       string
		allowed    []UserRole
		ctxRole    string
		setRole    bool
		wantStatus int
		wantCode   string
	}{
		{
			name:       "defectologist_allowed",
			allowed:    []UserRole{RoleDefectologist},
			ctxRole:    "defectologist",
			setRole:    true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "head_defectologist_allowed",
			allowed:    []UserRole{RoleHeadDefectologist},
			ctxRole:    "head_defectologist",
			setRole:    true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "defectologist_denied_when_only_head_allowed",
			allowed:    []UserRole{RoleHeadDefectologist},
			ctxRole:    "defectologist",
			setRole:    true,
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
		},
		{
			name:       "head_defectologist_denied_when_only_defectologist_allowed",
			allowed:    []UserRole{RoleDefectologist},
			ctxRole:    "head_defectologist",
			setRole:    true,
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
		},
		{
			name:       "both_roles_allowed_defectologist",
			allowed:    []UserRole{RoleDefectologist, RoleHeadDefectologist},
			ctxRole:    "defectologist",
			setRole:    true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "both_roles_allowed_head_defectologist",
			allowed:    []UserRole{RoleDefectologist, RoleHeadDefectologist},
			ctxRole:    "head_defectologist",
			setRole:    true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "unknown_role_denied",
			allowed:    []UserRole{RoleDefectologist, RoleHeadDefectologist},
			ctxRole:    "admin",
			setRole:    true,
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
		},
		{
			name:       "no_role_in_context",
			allowed:    []UserRole{RoleDefectologist},
			setRole:    false,
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := ErrorMiddleware(RequireRole(tt.allowed...)(noop))

			ctx := context.Background()
			if tt.setRole {
				ctx = authctx.SetRoleToCtx(ctx, tt.ctxRole)
			}

			req := httptest.NewRequestWithContext(ctx, "GET", "/test", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantCode != "" {
				var resp JSONErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Error.Code != tt.wantCode {
					t.Errorf("code = %s, want %s", resp.Error.Code, tt.wantCode)
				}
			}
		})
	}
}
