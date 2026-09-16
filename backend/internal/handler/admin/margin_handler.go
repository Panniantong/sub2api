package admin

import (
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// MarginHandler 毛利看板与熔断：收入取用户实扣，成本按目录价估算。
type MarginHandler struct {
	service *service.MarginService
}

func NewMarginHandler(svc *service.MarginService) *MarginHandler {
	return &MarginHandler{service: svc}
}

func (h *MarginHandler) requireService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, errors.New("margin service unavailable"))
		return false
	}
	return true
}

// Summary 毛利汇总：?hours=24&group_id=。
// GET /api/v1/admin/margins
func (h *MarginHandler) Summary(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	hours := 24
	if raw := c.Query("hours"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= 24*90 {
			hours = v
		}
	}
	var groupID *int64
	if raw := c.Query("group_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			groupID = &v
		}
	}
	rows, err := h.service.Summary(c.Request.Context(), hours, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if rows == nil {
		rows = []service.MarginRow{}
	}
	response.Success(c, gin.H{"items": rows, "count": len(rows), "hours": hours})
}

// GetSettings 读取熔断配置。
// GET /api/v1/admin/margins/fuse-settings
func (h *MarginHandler) GetSettings(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	response.Success(c, h.service.GetSettings(c.Request.Context()))
}

// UpdateSettings 更新熔断配置。
// PUT /api/v1/admin/margins/fuse-settings
func (h *MarginHandler) UpdateSettings(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	var req service.MarginFuseSettings
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

// Events 近期熔断/恢复事件。
// GET /api/v1/admin/margins/events
func (h *MarginHandler) Events(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	events := h.service.Events()
	if events == nil {
		events = []service.MarginFuseEvent{}
	}
	response.Success(c, gin.H{"items": events, "count": len(events)})
}

// Unfuse 手动恢复渠道并清除熔断跟踪。
// POST /api/v1/admin/margins/channels/:id/unfuse
func (h *MarginHandler) Unfuse(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid channel ID")
		return
	}
	if err := h.service.Unfuse(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Channel recovered successfully"})
}
