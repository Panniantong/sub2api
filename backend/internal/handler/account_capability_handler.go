package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountCapabilityHandler struct {
	svc *service.IntelligentTestService
}

func NewAccountCapabilityHandler(svc *service.IntelligentTestService) *AccountCapabilityHandler {
	return &AccountCapabilityHandler{svc: svc}
}
func capabilityUser(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID < 1 {
		response.Unauthorized(c, "authentication required")
		return 0, false
	}
	c.Header("Cache-Control", "private, no-store")
	return subject.UserID, true
}
func (h *AccountCapabilityHandler) List(c *gin.Context) {
	user, ok := capabilityUser(c)
	if !ok {
		return
	}
	f, ok := admin.ParseIntelligentTestFilter(c)
	if !ok {
		return
	}
	items, total, err := h.svc.Capabilities(c.Request.Context(), user, f)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"items": items, "total": total, "page": f.Page, "page_size": f.PageSize})
	}
}
func (h *AccountCapabilityHandler) History(c *gin.Context) {
	user, ok := capabilityUser(c)
	if !ok {
		return
	}
	id, ok := admin.IntelligentTestParamID(c, "account_id")
	if !ok {
		return
	}
	f, ok := admin.ParseIntelligentTestFilter(c)
	if !ok {
		return
	}
	f.AccountID = id
	out, err := h.svc.PublicRecords(c.Request.Context(), user, f)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *AccountCapabilityHandler) Get(c *gin.Context) {
	user, ok := capabilityUser(c)
	if !ok {
		return
	}
	id, ok := admin.IntelligentTestParamID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.PublicGet(c.Request.Context(), user, id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *AccountCapabilityHandler) Image(c *gin.Context) {
	user, ok := capabilityUser(c)
	if !ok {
		return
	}
	id, ok := admin.IntelligentTestParamID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.PublicGet(c.Request.Context(), user, id)
	if response.ErrorFrom(c, err) {
		return
	}
	admin.WriteIntelligentTestImage(c, out.ResultImage)
}
