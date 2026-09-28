package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetCookieSettings(c *gin.Context) {
	value, err := h.settingService.GetOpenAICookieSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	public := *value
	public.RemoteSyncAdminKey = ""
	response.Success(c, public)
}

func (h *SettingHandler) UpdateCookieSettings(c *gin.Context) {
	var value service.OpenAICookieSettings
	if err := c.ShouldBindJSON(&value); err != nil {
		response.BadRequest(c, "Invalid Cookie configuration")
		return
	}
	if err := h.settingService.SetOpenAICookieSettings(c.Request.Context(), &value); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	value.RemoteSyncAdminKey = ""
	response.Success(c, value)
}

func (h *SettingHandler) GetCookieLogs(c *gin.Context) {
	logs, err := h.settingService.GetOpenAICookieLogs(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if raw := c.Query("limit"); raw != "" {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit < 1 || limit > 100 {
			response.BadRequest(c, "limit must be between 1 and 100")
			return
		}
		if len(logs) > limit {
			logs = logs[:limit]
		}
	}
	response.Success(c, logs)
}

func (h *SettingHandler) GetCookieValidationLogs(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	if pageSize > 100 {
		pageSize = 100
	}
	accountID := int64(0)
	if raw := c.Query("account_id"); raw != "" {
		value, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || value < 1 {
			response.BadRequest(c, "account_id must be a positive integer")
			return
		}
		accountID = value
	}
	logs, total, err := h.settingService.GetOpenAICookieValidationLogsPage(c.Request.Context(), accountID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, logs, total, page, pageSize)
}

func (h *SettingHandler) GetCookieProxyHostMemories(c *gin.Context) {
	memories, err := h.settingService.GetOpenAICookieProxyHostMemories(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, memories)
}

func (h *SettingHandler) ResetCookieProxyHostMemory(c *gin.Context) {
	var req struct {
		Proxy         string `json:"proxy"`
		ProxyUsername string `json:"proxy_username"`
		All           bool   `json:"all"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || (!req.All && req.Proxy == "") {
		response.BadRequest(c, "proxy is required")
		return
	}
	var resetErr error
	if req.All {
		resetErr = h.settingService.ResetAllOpenAICookieProxyHostMemories(c.Request.Context())
	} else {
		resetErr = h.settingService.ResetOpenAICookieProxyHostMemoryForProxy(c.Request.Context(), req.Proxy, req.ProxyUsername)
	}
	if resetErr != nil {
		response.ErrorFrom(c, resetErr)
		return
	}
	response.Success(c, gin.H{"reset": true})
}
