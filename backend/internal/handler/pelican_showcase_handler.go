package handler

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// PelicanShowcaseHandler serves the user-facing Pelican gallery (read-only) and the
// admin action that removes a snapshot from it.
type PelicanShowcaseHandler struct {
	showcase *service.PelicanShowcaseService
}

func NewPelicanShowcaseHandler(showcase *service.PelicanShowcaseService) *PelicanShowcaseHandler {
	return &PelicanShowcaseHandler{showcase: showcase}
}

// List GET /api/v1/pelican-showcase
// A disabled gallery answers enabled=false with no groups rather than an error.
func (h *PelicanShowcaseHandler) List(c *gin.Context) {
	view, err := h.showcase.View(c.Request.Context(), time.Now())
	if err != nil {
		response.InternalError(c, "Failed to load pelican showcase")
		return
	}
	response.Success(c, view)
}

// GetItem GET /api/v1/pelican-showcase/items/:id
// Returns the raw model output; the frontend renders it in a sandboxed iframe.
func (h *PelicanShowcaseHandler) GetItem(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid item id")
		return
	}
	item, err := h.showcase.Item(c.Request.Context(), id, time.Now())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

// DeleteItem DELETE /api/v1/admin/pelican-showcase/items/:id
func (h *PelicanShowcaseHandler) DeleteItem(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid item id")
		return
	}
	if err := h.showcase.Remove(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
