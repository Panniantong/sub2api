package admin

import (
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type UserCleanupHandler struct{ service *service.UserCleanupService }

func NewUserCleanupHandler(svc *service.UserCleanupService) *UserCleanupHandler {
	return &UserCleanupHandler{service: svc}
}

// Defense in depth: authentication middleware protects routes, and these handlers
// reject ordinary users even when accidentally mounted outside the admin group.
func userCleanupActorID(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return 0, false
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || (role != "admin" && role != "super_admin") {
		response.Forbidden(c, "Administrator access required")
		return 0, false
	}
	return subject.UserID, true
}

func (h *UserCleanupHandler) Summary(c *gin.Context) {
	id, ok := userCleanupActorID(c)
	if !ok {
		return
	}
	out, err := h.service.Summary(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *UserCleanupHandler) Preview(c *gin.Context) {
	id, ok := userCleanupActorID(c)
	if !ok {
		return
	}
	out, err := h.service.Preview(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *UserCleanupHandler) Execute(c *gin.Context) {
	id, ok := userCleanupActorID(c)
	if !ok {
		return
	}
	var req struct {
		PreviewID     string `json:"preview_id" binding:"required,uuid"`
		Confirm       bool   `json:"confirm"`
		ExpectedCount int    `json:"expected_count" binding:"required,min=1,max=500"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid cleanup confirmation")
		return
	}
	if !req.Confirm {
		response.ErrorFrom(c, service.ErrUserCleanupConfirmation)
		return
	}
	out, err := h.service.Execute(c.Request.Context(), id, req.PreviewID, req.Confirm, req.ExpectedCount, userCleanupAudit(c))
	if err != nil {
		if errors.Is(err, service.ErrUserCleanupBusy) {
			c.Header("Retry-After", "3")
		}
		response.ErrorFrom(c, err)
		return
	}
	if out.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	middleware.SetAuditAction(c, "admin.users.cleanup.request")
	middleware.SetAuditExtra(c, map[string]any{"requested_count": req.ExpectedCount, "matched_count": out.DeletedCount, "result": "completed"})
	response.Success(c, out)
}

func (h *UserCleanupHandler) GetGuard(c *gin.Context) {
	actorID, ok := userCleanupActorID(c)
	if !ok {
		return
	}
	id, ok := cleanupTargetID(c)
	if !ok {
		return
	}
	out, err := h.service.GetGuard(c.Request.Context(), actorID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *UserCleanupHandler) UpdateGuard(c *gin.Context) {
	actorID, ok := userCleanupActorID(c)
	if !ok {
		return
	}
	id, ok := cleanupTargetID(c)
	if !ok {
		return
	}
	var req struct {
		IsProtected *bool `json:"is_protected" binding:"required"`
		IsSystem    *bool `json:"is_system"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "is_protected is required")
		return
	}
	out, err := h.service.UpdateGuard(c.Request.Context(), actorID, id, *req.IsProtected, req.IsSystem, userCleanupAudit(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func cleanupTargetID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return 0, false
	}
	return id, true
}
func userCleanupAudit(c *gin.Context) service.UserCleanupAuditContext {
	return service.UserCleanupAuditContext{AuthMethod: c.GetString("auth_method"), ClientIP: c.ClientIP(), RequestID: c.GetHeader("X-Request-ID"), Method: c.Request.Method, Path: c.Request.URL.Path}
}
