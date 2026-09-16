package admin

import (
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// TicketHandler 工单：用户端（JWT 上下文取用户）与管理端共用同一 handler。
type TicketHandler struct {
	service *service.TicketService
}

func NewTicketHandler(svc *service.TicketService) *TicketHandler {
	return &TicketHandler{service: svc}
}

func (h *TicketHandler) requireService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, errors.New("ticket service unavailable"))
		return false
	}
	return true
}

func ticketUserID(c *gin.Context) (int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return 0, false
	}
	return subject.UserID, true
}

func ticketID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid ticket ID")
		return 0, false
	}
	return id, true
}

func ticketErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrTicketNotFound):
		response.NotFound(c, "Ticket not found")
	case errors.Is(err, service.ErrTicketClosed):
		response.BadRequest(c, "Ticket is closed")
	default:
		response.ErrorFrom(c, err)
	}
}

// ListMine 用户工单列表。
// GET /tickets
func (h *TicketHandler) ListMine(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	items, err := h.service.ListMine(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if items == nil {
		items = []service.SupportTicket{}
	}
	response.Success(c, gin.H{"items": items, "count": len(items)})
}

type ticketCreateRequest struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Create 用户建单。
// POST /tickets
func (h *TicketHandler) Create(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	var req ticketCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	t, err := h.service.Create(c.Request.Context(), userID, req.Subject, req.Body)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, t)
}

// GetMine 用户查看自己的工单。
// GET /tickets/:id
func (h *TicketHandler) GetMine(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	t, err := h.service.GetMine(c.Request.Context(), userID, id)
	if err != nil {
		ticketErr(c, err)
		return
	}
	response.Success(c, t)
}

type ticketReplyRequest struct {
	Body string `json:"body"`
}

// ReplyMine 用户追问。
// POST /tickets/:id/replies
func (h *TicketHandler) ReplyMine(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	var req ticketReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	r, err := h.service.ReplyMine(c.Request.Context(), userID, id, req.Body)
	if err != nil {
		ticketErr(c, err)
		return
	}
	response.Success(c, r)
}

// CloseMine 用户关闭。
// POST /tickets/:id/close
func (h *TicketHandler) CloseMine(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	if err := h.service.CloseMine(c.Request.Context(), userID, id); err != nil {
		ticketErr(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Ticket closed successfully"})
}

// ListAll 管理端列表：?status=&search=&limit=&offset=。
// GET /api/v1/admin/tickets
func (h *TicketHandler) ListAll(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= 500 {
			limit = v
		}
	}
	offset := 0
	if raw := c.Query("offset"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			offset = v
		}
	}
	items, err := h.service.ListAll(c.Request.Context(), c.Query("status"), c.Query("search"), limit, offset)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if items == nil {
		items = []service.SupportTicket{}
	}
	response.Success(c, gin.H{"items": items, "count": len(items)})
}

// GetAny 管理端查看。
// GET /api/v1/admin/tickets/:id
func (h *TicketHandler) GetAny(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	t, err := h.service.GetAny(c.Request.Context(), id)
	if err != nil {
		ticketErr(c, err)
		return
	}
	response.Success(c, t)
}

// ReplyAny 管理员回复（状态置 answered）。
// POST /api/v1/admin/tickets/:id/replies
func (h *TicketHandler) ReplyAny(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	var req ticketReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	r, err := h.service.ReplyAny(c.Request.Context(), subject.UserID, id, req.Body)
	if err != nil {
		ticketErr(c, err)
		return
	}
	response.Success(c, r)
}

// CloseAny 管理员关闭。
// POST /api/v1/admin/tickets/:id/close
func (h *TicketHandler) CloseAny(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	if err := h.service.CloseAny(c.Request.Context(), id); err != nil {
		ticketErr(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Ticket closed successfully"})
}

// Stats 未关闭工单数（管理端角标）。
// GET /api/v1/admin/tickets/stats
func (h *TicketHandler) Stats(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	n, err := h.service.OpenCount(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"open": n})
}
