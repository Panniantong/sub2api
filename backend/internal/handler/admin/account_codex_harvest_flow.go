package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type setCodexSkipHarvestRequest struct {
	SkipHarvest *bool `json:"skip_harvest" binding:"required"`
}

// GetCodexHarvestFlow returns the live harvest pipeline for the admin flow page.
// GET /api/v1/admin/accounts/codex-harvest-flow
func (h *AccountHandler) GetCodexHarvestFlow(c *gin.Context) {
	if h == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account handler not available")
		return
	}
	ctx := c.Request.Context()
	var accounts []service.Account
	if h.adminService != nil {
		listed, err := h.adminService.ListAccountsForSchedulerScoreFilter(ctx, service.PlatformOpenAI, "", "", "", 0, "")
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		accounts = listed
	}
	response.Success(c, service.BuildCodexHarvestFlow(ctx, h.cfg, h.codexTicketSettings, accounts, h.codexHarvest))
}

// SetCodexSkipHarvest stops background ticket probes for this account.
// The account stays schedulable: own leftover tickets may still be used,
// and missing tickets do not fail-close the account.
// PUT /api/v1/admin/accounts/:id/codex-skip-harvest
func (h *AccountHandler) SetCodexSkipHarvest(c *gin.Context) {
	if h == nil || h.adminService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account handler not available")
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req setCodexSkipHarvestRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.SkipHarvest == nil {
		response.BadRequest(c, "skip_harvest is required")
		return
	}
	if err := h.adminService.UpdateAccountExtra(c.Request.Context(), accountID, map[string]any{
		service.OpenAICodexSkipHarvestExtraKey: *req.SkipHarvest,
	}); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"account_id": accountID, "skip_harvest": *req.SkipHarvest})
}

// ManualCodexHarvest streams a directed single-account harvest over SSE.
// POST /api/v1/admin/accounts/:id/manual-harvest
func (h *AccountHandler) ManualCodexHarvest(c *gin.Context) {
	if h == nil || h.openAIGatewayService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Gateway service not available")
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req service.ManualHarvestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid parameters")
		return
	}
	req.AccountID = accountID
	req, err = service.NormalizeManualHarvestRequest(req)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	ctx, err := service.WithCodexHarvestEdgeIP(c.Request.Context(), c.GetHeader("X-Edge-IP"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		response.Error(c, http.StatusInternalServerError, "Streaming unsupported")
		return
	}

	emit := func(p service.ManualHarvestProgress) {
		if ctx.Err() != nil {
			return
		}
		data, err := json.Marshal(p)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		flusher.Flush()
	}
	if err := h.openAIGatewayService.ExecuteManualHarvest(ctx, req, emit); err != nil && ctx.Err() == nil {
		emit(service.ManualHarvestProgress{Done: true, Result: "error", Level: "ERROR", Message: err.Error()})
	}
}
