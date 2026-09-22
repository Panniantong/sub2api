package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) SetCodexHarvestService(controls *service.CodexHarvestService) {
	h.codexHarvest = controls
}

func (h *AccountHandler) harvestControlsReady(c *gin.Context) bool {
	if h == nil || h.codexHarvest == nil {
		response.Error(c, http.StatusServiceUnavailable, "Harvest controls unavailable")
		return false
	}
	return true
}

func (h *AccountHandler) GetCodexHarvestControls(c *gin.Context) {
	if !h.harvestControlsReady(c) {
		return
	}
	proxy := ""
	if h.cfg != nil {
		proxy = h.cfg.Gateway.OpenAICodexTicket.HarvestProxyURL
	}
	if h.codexTicketSettings != nil {
		if override := h.codexTicketSettings.GetOpenAICodexTicketHarvestProxyURL(c.Request.Context()); override != "" {
			proxy = override
		}
	}
	response.Success(c, h.codexHarvest.Snapshot(c.Request.Context(), proxy))
}

func (h *AccountHandler) UpdateCodexHarvestControls(c *gin.Context) {
	if !h.harvestControlsReady(c) {
		return
	}
	var body service.CodexHarvestControls
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid harvest settings")
		return
	}
	if err := service.ValidateCodexHarvestControls(body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.codexHarvest.SaveControls(c.Request.Context(), body); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Harvest settings were not saved")
		return
	}
	saved, _, _ := h.codexHarvest.Controls(c.Request.Context())
	response.Success(c, saved)
}

func (h *AccountHandler) GetCodexHarvestNodes(c *gin.Context) {
	if !h.harvestControlsReady(c) {
		return
	}
	offset, e1 := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, e2 := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if e1 != nil || e2 != nil || offset < 0 || limit < 1 || limit > 100 {
		response.BadRequest(c, "Invalid node page")
		return
	}
	page, err := h.codexHarvest.ListNodes(c.Request.Context(), offset, limit)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Node learning records unavailable")
		return
	}
	response.Success(c, page)
}

func (h *AccountHandler) ResetCodexHarvestNodes(c *gin.Context) {
	if !h.harvestControlsReady(c) {
		return
	}
	var body struct {
		RecordID *int64 `json:"record_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.RecordID == nil || *body.RecordID < 0 {
		response.BadRequest(c, "record_id is required; 0 resets all learning records")
		return
	}
	if err := h.codexHarvest.ResetNodes(c.Request.Context(), *body.RecordID); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Node learning reset failed")
		return
	}
	response.Success(c, gin.H{"reset": true})
}
