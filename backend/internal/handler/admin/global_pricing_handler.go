package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GlobalPricingHandler 管理全站模型定价覆盖：命中模型的请求在全部分组/
// 账号强制使用此处价格，未命中的模型走原定价链路。
type GlobalPricingHandler struct {
	service *service.GlobalModelPricingService
}

func NewGlobalPricingHandler(svc *service.GlobalModelPricingService) *GlobalPricingHandler {
	return &GlobalPricingHandler{service: svc}
}

func (h *GlobalPricingHandler) requireService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, errors.New("global pricing service unavailable"))
		return false
	}
	return true
}

func globalPricingNotFound(c *gin.Context, err error) bool {
	if errors.Is(err, service.ErrGlobalModelPricingNotFound) {
		response.NotFound(c, "Global pricing entry not found")
		return true
	}
	return false
}

func globalPricingConflict(c *gin.Context, err error) bool {
	if errors.Is(err, service.ErrGlobalModelPricingDuplicate) {
		response.Error(c, http.StatusConflict, "This model already has a global pricing entry")
		return true
	}
	return false
}

// List 返回全部全站定价（含禁用）。
// GET /api/v1/admin/global-pricing
func (h *GlobalPricingHandler) List(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	items, err := h.service.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "count": len(items)})
}

type globalPricingUpsertRequest struct {
	ModelPattern      string              `json:"model_pattern"`
	BillingMode       service.BillingMode `json:"billing_mode"`
	InputPrice        *float64            `json:"input_price"`
	OutputPrice       *float64            `json:"output_price"`
	CacheWritePrice   *float64            `json:"cache_write_price"`
	CacheWrite1hPrice *float64            `json:"cache_write_1h_price"`
	CacheReadPrice    *float64            `json:"cache_read_price"`
	PerRequestPrice   *float64            `json:"per_request_price"`
	Enabled           *bool               `json:"enabled"`
}

func (r globalPricingUpsertRequest) toInput(defaultEnabled bool) service.GlobalModelPriceInput {
	enabled := defaultEnabled
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return service.GlobalModelPriceInput{
		ModelPattern:      r.ModelPattern,
		BillingMode:       r.BillingMode,
		InputPrice:        r.InputPrice,
		OutputPrice:       r.OutputPrice,
		CacheWritePrice:   r.CacheWritePrice,
		CacheWrite1hPrice: r.CacheWrite1hPrice,
		CacheReadPrice:    r.CacheReadPrice,
		PerRequestPrice:   r.PerRequestPrice,
		Enabled:           enabled,
	}
}

// Create 新建全站定价（默认启用）。
// POST /api/v1/admin/global-pricing
func (h *GlobalPricingHandler) Create(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	var req globalPricingUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.service.Create(c.Request.Context(), req.toInput(true))
	if err != nil {
		if globalPricingConflict(c, err) {
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

// Update 更新全站定价（enabled 缺省保持原值）。
// PUT /api/v1/admin/global-pricing/:id
func (h *GlobalPricingHandler) Update(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid pricing ID")
		return
	}
	var req globalPricingUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	input := req.toInput(true)
	if req.Enabled == nil {
		existing, err := h.service.Get(c.Request.Context(), id)
		if err != nil {
			if globalPricingNotFound(c, err) {
				return
			}
			response.ErrorFrom(c, err)
			return
		}
		input.Enabled = existing.Enabled
	}
	item, err := h.service.Update(c.Request.Context(), id, input)
	if err != nil {
		if globalPricingNotFound(c, err) {
			return
		}
		if globalPricingConflict(c, err) {
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

// Delete 删除全站定价（对应模型恢复原定价）。
// DELETE /api/v1/admin/global-pricing/:id
func (h *GlobalPricingHandler) Delete(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid pricing ID")
		return
	}
	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		if globalPricingNotFound(c, err) {
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Global pricing deleted successfully"})
}

type globalPricingEnableRequest struct {
	Enabled *bool `json:"enabled"`
}

// SetEnabled 启停全站定价（停用后对应模型恢复原定价）。
// POST /api/v1/admin/global-pricing/:id/enable
func (h *GlobalPricingHandler) SetEnabled(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid pricing ID")
		return
	}
	var req globalPricingEnableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Enabled == nil {
		response.BadRequest(c, "Invalid request: enabled is required")
		return
	}
	item, err := h.service.SetEnabled(c.Request.Context(), id, *req.Enabled)
	if err != nil {
		if globalPricingNotFound(c, err) {
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}
