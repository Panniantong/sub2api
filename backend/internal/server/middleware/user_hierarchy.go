package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type hierarchyUserLookup interface {
	GetByID(context.Context, int64) (*service.User, error)
}

// UserHierarchyGuard belongs after AdminAuth on the admin route group. It
// protects all user writes, including balance, TOTP, identities and bulk limits.
func UserHierarchyGuard(userService *service.UserService) gin.HandlerFunc {
	if userService == nil {
		return userHierarchyGuard(nil)
	}
	return userHierarchyGuard(userService)
}

func userHierarchyGuard(users hierarchyUserLookup) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		role, ok := GetUserRoleFromContext(c)
		if subject, present := GetAuthSubjectFromContext(c); present && subject.UserID > 0 {
			c.Request = c.Request.WithContext(service.WithUserManagementActor(c.Request.Context(), subject.UserID))
		}
		if superAdminOperation(path, c.Request.Method) && (!ok || role != service.RoleSuperAdmin) {
			AbortWithError(c, 403, "SUPER_ADMIN_REQUIRED", "只有超级管理员可以管理全局安全设置、备份和插件")
			return
		}
		if !strings.HasPrefix(path, "/api/v1/admin/users") || c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		if !ok || !service.IsAdminRole(role) {
			AbortWithError(c, 403, "FORBIDDEN", "Admin access required")
			return
		}
		if role == service.RoleSuperAdmin {
			c.Next()
			return
		}
		var body struct {
			Role    string  `json:"role"`
			UserIDs []int64 `json:"user_ids"`
			All     bool    `json:"all"`
		}
		if c.Request.Body != nil && c.Request.Body != http.NoBody {
			data, err := io.ReadAll(io.LimitReader(c.Request.Body, 2<<20))
			if err != nil || len(data) >= 2<<20 {
				AbortWithError(c, 400, "INVALID_REQUEST", "User management request is too large")
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(data))
			if len(data) > 0 && json.Unmarshal(data, &body) != nil {
				AbortWithError(c, 400, "INVALID_REQUEST", "Invalid JSON request")
				return
			}
		}
		// All=true includes administrators. The super-admin may use that legacy
		// operation; an ordinary admin selects specific ordinary users instead.
		if body.All || service.IsAdminRole(body.Role) {
			AbortWithError(c, 403, "SUPER_ADMIN_REQUIRED", "只有超级管理员可以修改角色或管理管理员账号")
			return
		}
		ids := body.UserIDs
		if len(ids) > 500 {
			AbortWithError(c, 400, "INVALID_REQUEST", "At most 500 users may be selected")
			return
		}
		if raw := c.Param("id"); raw != "" {
			if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
				ids = append(ids, id)
			}
		}
		for _, id := range ids {
			if users == nil {
				AbortWithError(c, 503, "UNAVAILABLE", "User authorization unavailable")
				return
			}
			u, err := users.GetByID(c.Request.Context(), id)
			if err != nil {
				AbortWithError(c, 403, "FORBIDDEN", "Cannot authorize target user")
				return
			}
			if u.IsAdmin() {
				AbortWithError(c, 403, "SUPER_ADMIN_REQUIRED", "只有超级管理员可以管理管理员账号")
				return
			}
		}
		c.Next()
	}
}

func superAdminOperation(path, method string) bool {
	for _, prefix := range []string{"/api/v1/admin/settings/admin-api-key", "/api/v1/admin/backups", "/api/v1/admin/data-management"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	write := method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
	return write && (strings.HasPrefix(path, "/api/v1/admin/system/") || path == "/api/v1/admin/settings" || strings.HasPrefix(path, "/api/v1/admin/settings/") || path == "/api/v1/admin/plugins" || strings.HasPrefix(path, "/api/v1/admin/plugins/"))
}
