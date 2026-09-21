package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// DynamicProxyHandler 动态代理打票(uDeal 模式)管理端点。
// 全部挂在 /api/v1/admin/dynamic-proxy 下。
type DynamicProxyHandler struct {
	openAIGateway *service.OpenAIGatewayService
}

// NewDynamicProxyHandler 构造动态代理管理 handler。
func NewDynamicProxyHandler(openAIGateway *service.OpenAIGatewayService) *DynamicProxyHandler {
	return &DynamicProxyHandler{openAIGateway: openAIGateway}
}

// Status 返回配置 + 全部绑定快照。
// GET /api/v1/admin/dynamic-proxy/status
func (h *DynamicProxyHandler) Status(c *gin.Context) {
	view, err := h.openAIGateway.DynamicProxyStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

// UpdateConfigRequest 写动态代理配置。
type UpdateConfigRequest struct {
	Enabled   bool   `json:"enabled"`
	IplistURL string `json:"iplist_url"`
	User      string `json:"user"`
	Pass      string `json:"pass"`
}

// UpdateConfig 写配置(总开关/iplist/认证)。pass 空=保留原值。
// PUT /api/v1/admin/dynamic-proxy/config
func (h *DynamicProxyHandler) UpdateConfig(c *gin.Context) {
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "invalid body: "+err.Error())
		return
	}
	if err := h.openAIGateway.SetDynamicProxyConfig(c.Request.Context(), req.Enabled, req.IplistURL, req.User, req.Pass); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	view, err := h.openAIGateway.DynamicProxyStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

// Rebind 手动给某账号重绑新端口。
// POST /api/v1/admin/dynamic-proxy/accounts/:id/rebind
func (h *DynamicProxyHandler) Rebind(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, 400, "invalid account id")
		return
	}
	binding, err := h.openAIGateway.RebindDynamicProxy(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, binding)
}

// Unbind 解绑某账号(账号回到静态/无代理)。
// DELETE /api/v1/admin/dynamic-proxy/accounts/:id/binding
func (h *DynamicProxyHandler) Unbind(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, 400, "invalid account id")
		return
	}
	if err := h.openAIGateway.UnbindDynamicProxyAccount(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"unbound": id})
}
