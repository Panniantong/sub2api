package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) GetCookieHostMonitor(c *gin.Context) {
	settings, err := h.settingService.GetOpenAICookieSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	rows, err := h.settingService.CookieHostMonitorSamples(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	config := settings.HostMonitor
	if config == nil {
		config = &service.CookieHostMonitorConfig{Model: "gpt-6-astra", InitialWaitSeconds: 21600, IntervalSeconds: 300}
	}
	firstNo, lastNo, recovered := service.CookieHostMonitorRecovery(rows, config.ExperimentID)
	next := service.CookieHostMonitorNext(*config, rows)
	page, size := response.ParsePagination(c)
	if size > 100 {
		size = 100
	}
	start := (page - 1) * size
	if start > len(rows) {
		start = len(rows)
	}
	end := start + size
	if end > len(rows) {
		end = len(rows)
	}
	response.Success(c, gin.H{"config": config, "running": service.CookieHostMonitorRunning(), "items": rows[start:end], "total": len(rows), "page": page, "page_size": size, "next_at": next, "first_no": firstNo, "last_no": lastNo, "recovered_at": recovered})
}

func (h *AccountHandler) GetCookieHarvestRuntime(c *gin.Context) {
	response.Success(c, h.openaiGatewayService.CookieHarvestRunning())
}

func (h *AccountHandler) GetCookieDashboard(c *gin.Context) {
	result, err := h.openaiGatewayService.CookieDashboard(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) UpdateCookieHostMonitor(c *gin.Context) {
	var config service.CookieHostMonitorConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		response.BadRequest(c, "监控配置无效")
		return
	}
	if err := h.openaiGatewayService.SetCookieHostMonitor(c.Request.Context(), config); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"saved": true})
}

func (h *AccountHandler) RunCookieHostMonitor(c *gin.Context) {
	if err := h.openaiGatewayService.StartCookieHostMonitorProbe(c.Request.Context(), true); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"accepted": true})
}
