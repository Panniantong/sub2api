package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountTrafficHandler struct {
	admin   service.AdminService
	traffic *service.AccountTrafficService
}

func NewAccountTrafficHandler(admin service.AdminService, traffic *service.AccountTrafficService) *AccountTrafficHandler {
	return &AccountTrafficHandler{admin: admin, traffic: traffic}
}
func (h *AccountTrafficHandler) Get(c *gin.Context) {
	if _, ok := userCleanupActorID(c); !ok {
		return
	}
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	a, err := h.admin.GetAccount(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	policy, err := service.ParseAccountTrafficPolicy(a.Extra)
	if response.ErrorFrom(c, err) {
		return
	}
	state, err := h.traffic.State(c.Request.Context(), a)
	// Configuration remains editable during a temporary telemetry outage.
	if err != nil {
		response.Success(c, gin.H{"policy": policy, "state": nil, "state_available": false, "hard_limit": a.Mode1EffectiveConcurrency()})
		return
	}
	response.Success(c, gin.H{"policy": policy, "state": state, "state_available": true, "hard_limit": a.Mode1EffectiveConcurrency()})
}
func (h *AccountTrafficHandler) Update(c *gin.Context) {
	if _, ok := userCleanupActorID(c); !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var policy service.AccountTrafficPolicy
	if err := c.ShouldBindJSON(&policy); err != nil {
		response.BadRequest(c, "Invalid traffic policy")
		return
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		response.BadRequest(c, "Invalid traffic policy")
		return
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		response.BadRequest(c, "Invalid traffic policy")
		return
	}
	a, err := h.admin.GetAccount(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	copy := *a
	copy.Extra = map[string]any{}
	for k, v := range a.Extra {
		copy.Extra[k] = v
	}
	copy.Extra[service.AccountTrafficPolicyKey] = value
	if _, err := service.AccountTrafficPlanFor(&copy); response.ErrorFrom(c, err) {
		return
	}
	if err := h.admin.UpdateAccountExtra(c.Request.Context(), id, map[string]any{service.AccountTrafficPolicyKey: value}); response.ErrorFrom(c, err) {
		return
	}
	updated, err := h.admin.GetAccount(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	synced := h.traffic.Sync(c.Request.Context(), updated) == nil
	response.Success(c, gin.H{"policy": policy, "state_available": synced, "hard_limit": updated.Mode1EffectiveConcurrency()})
}
