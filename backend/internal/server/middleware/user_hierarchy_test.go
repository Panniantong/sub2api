package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type hierarchyTestUsers map[int64]*service.User

func (s hierarchyTestUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	if u := s[id]; u != nil {
		return u, nil
	}
	return nil, service.ErrUserNotFound
}

func TestUserHierarchyGuardsEveryMutationAndBatch(t *testing.T) {
	for _, tt := range []struct {
		name, role, path, body string
		want                   int
	}{
		{"admin_balance_super", service.RoleAdmin, "/api/v1/admin/users/2/balance", `{"balance":9}`, 403},
		{"admin_totp_super", service.RoleAdmin, "/api/v1/admin/users/2/totp", `{}`, 403},
		{"admin_batch_super", service.RoleAdmin, "/api/v1/admin/users/batch", `{"user_ids":[3,2]}`, 403},
		{"admin_batch_all", service.RoleAdmin, "/api/v1/admin/users/batch", `{"all":true}`, 403},
		{"admin_promote_self", service.RoleAdmin, "/api/v1/admin/users/1", `{"role":"super_admin"}`, 403},
		{"admin_create_admin", service.RoleAdmin, "/api/v1/admin/users", `{"role":"admin"}`, 403},
		{"admin_update_user", service.RoleAdmin, "/api/v1/admin/users/3", `{"notes":"updated"}`, 200},
		{"super_update_admin", service.RoleSuperAdmin, "/api/v1/admin/users/1", `{"notes":"updated"}`, 200},
		{"user_forbidden", service.RoleUser, "/api/v1/admin/users/3", `{}`, 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(string(ContextKeyUserRole), tt.role) })
			r.Use(userHierarchyGuard(hierarchyTestUsers{1: {ID: 1, Role: service.RoleAdmin}, 2: {ID: 2, Role: service.RoleSuperAdmin}, 3: {ID: 3, Role: service.RoleUser}}))
			ok := func(c *gin.Context) { c.Status(http.StatusOK) }
			r.POST("/api/v1/admin/users", ok)
			r.POST("/api/v1/admin/users/batch", ok)
			r.POST("/api/v1/admin/users/:id", ok)
			r.POST("/api/v1/admin/users/:id/balance", ok)
			r.POST("/api/v1/admin/users/:id/totp", ok)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, tt.want, w.Code, w.Body.String())
		})
	}
}

func TestUserHierarchyPreventsGlobalKeyAndRestoreEscalation(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/settings/admin-api-key"},
		{"POST", "/api/v1/admin/settings/admin-api-key/regenerate"},
		{"PUT", "/api/v1/admin/settings"},
		{"POST", "/api/v1/admin/backups/8/restore"},
		{"GET", "/api/v1/admin/backups/8/download-url"},
		{"POST", "/api/v1/admin/data-management/backups"},
		{"PUT", "/api/v1/admin/plugins/8"},
	} {
		for _, role := range []string{service.RoleAdmin, service.RoleSuperAdmin} {
			t.Run(role+route.path, func(t *testing.T) {
				r := gin.New()
				r.Use(func(c *gin.Context) { c.Set(string(ContextKeyUserRole), role) })
				r.Use(userHierarchyGuard(nil))
				r.Handle(route.method, route.path, func(c *gin.Context) { c.Status(200) })
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
				want := 403
				if role == service.RoleSuperAdmin {
					want = 200
				}
				require.Equal(t, want, w.Code)
			})
		}
	}
}
