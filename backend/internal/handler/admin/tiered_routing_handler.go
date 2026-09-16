package admin

import (
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// TieredRoutingHandler 管理分级路由规则：VIP 用户优先 premium 池账号。
type TieredRoutingHandler struct {
	service *service.TieredRoutingService
}

func NewTieredRoutingHandler(svc *service.TieredRoutingService) *TieredRoutingHandler {
	return &TieredRoutingHandler{service: svc}
}

func (h *TieredRoutingHandler) requireService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, errors.New("tiered routing service unavailable"))
		return false
	}
	return true
}

// GetSettings 读取分级路由规则。
// GET /api/v1/admin/tiered-routing/settings
func (h *TieredRoutingHandler) GetSettings(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	response.Success(c, h.service.GetSettings(c.Request.Context()))
}

// UpdateSettings 更新分级路由规则。
// PUT /api/v1/admin/tiered-routing/settings
func (h *TieredRoutingHandler) UpdateSettings(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	var req service.TieredRoutingSettings
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
