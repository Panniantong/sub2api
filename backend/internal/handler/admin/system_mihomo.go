package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *SystemHandler) GetMihomo(c *gin.Context) { response.Success(c, h.kernel.Status()) }
func (h *SystemHandler) ManageMihomo(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	var req struct {
		Action        string                `json:"action"`
		Subscriptions []string              `json:"subscriptions"`
		Append        bool                  `json:"append"`
		CountryFilter *mihomo.CountryFilter `json:"country_filter"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.Error(c, http.StatusBadRequest, "Invalid kernel request")
		return
	}
	if err := h.kernel.Submit(req.Action, req.Subscriptions, req.Append, req.CountryFilter); err != nil {
		response.Error(c, http.StatusConflict, err.Error())
		return
	}
	response.Success(c, h.kernel.Status())
}
