package admin

import (
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AccountHealthHandler 管理账号健康分：快照列表、阈值配置、手动隔离/恢复。
type AccountHealthHandler struct {
	service *service.AccountHealthService
}

func NewAccountHealthHandler(svc *service.AccountHealthService) *AccountHealthHandler {
	return &AccountHealthHandler{service: svc}
}

func (h *AccountHealthHandler) requireService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, errors.New("account health service unavailable"))
		return false
	}
	return true
}

// Snapshot 返回最近一次评估快照。
// GET /api/v1/admin/account-health
func (h *AccountHealthHandler) Snapshot(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	items := h.service.Snapshot()
	if items == nil {
		items = []service.AccountHealthSnapshot{}
	}
	response.Success(c, gin.H{"items": items, "count": len(items)})
}

// GetSettings 读取阈值配置。
// GET /api/v1/admin/account-health/settings
func (h *AccountHealthHandler) GetSettings(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	response.Success(c, h.service.GetSettings(c.Request.Context()))
}

// UpdateSettings 更新阈值配置。
// PUT /api/v1/admin/account-health/settings
func (h *AccountHealthHandler) UpdateSettings(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	var req service.AccountHealthSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	out, err := h.service.UpdateSettings(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

// Isolate 手动隔离账号 24h。
// POST /api/v1/admin/account-health/:id/isolate
func (h *AccountHealthHandler) Isolate(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := h.service.Isolate(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Account isolated successfully"})
}

// Resume 手动恢复账号。
// POST /api/v1/admin/account-health/:id/resume
func (h *AccountHealthHandler) Resume(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := h.service.Resume(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Account resumed successfully"})
}
