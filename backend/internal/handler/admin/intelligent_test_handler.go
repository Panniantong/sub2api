package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"time"
)

type IntelligentTestHandler struct {
	svc *service.IntelligentTestService
}

func NewIntelligentTestHandler(svc *service.IntelligentTestService) *IntelligentTestHandler {
	return &IntelligentTestHandler{svc: svc}
}
func intelligentAdminActor(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return 0, false
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || (role != "admin" && role != "super_admin") {
		response.Forbidden(c, "administrator access required")
		return 0, false
	}
	return subject.UserID, true
}
func ParseIntelligentTestFilter(c *gin.Context) (service.IntelligentTestFilter, bool) {
	f := service.IntelligentTestFilter{Page: 1, PageSize: 24, Search: c.Query("search"), Platform: c.Query("platform"), AccountType: c.Query("account_type"), AccountStatus: c.Query("account_status"), TestType: c.Query("test_type"), Status: c.Query("status")}
	if f.AccountType == "" {
		f.AccountType = c.Query("type")
	}
	if len(f.Search) > 200 {
		response.BadRequest(c, "search is too long")
		return f, false
	}
	for key, dst := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
		if raw := c.Query(key); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > 1000000 {
				response.BadRequest(c, "invalid "+key)
				return f, false
			}
			*dst = v
		}
	}
	for key, dst := range map[string]*int64{"account_id": &f.AccountID, "group_id": &f.GroupID} {
		if raw := c.Query(key); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v < 1 {
				response.BadRequest(c, "invalid "+key)
				return f, false
			}
			*dst = v
		}
	}
	if raw := c.Query("anti_degradation"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "invalid anti_degradation")
			return f, false
		}
		f.AntiDegradation = &v
	}
	if raw := c.Query("only_abnormal"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "invalid only_abnormal")
			return f, false
		}
		f.OnlyAbnormal = v
	}
	for key, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if raw := c.Query(key); raw != "" {
			v, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				response.BadRequest(c, "invalid "+key+"; use RFC3339")
				return f, false
			}
			*dst = &v
		}
	}
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		response.BadRequest(c, "from must be before to")
		return f, false
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
	return f, true
}
func IntelligentTestParamID(c *gin.Context, param string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(param), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "invalid identifier")
		return 0, false
	}
	return id, true
}
func (h *IntelligentTestHandler) Accounts(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	f, ok := ParseIntelligentTestFilter(c)
	if !ok {
		return
	}
	out, err := h.svc.Accounts(c.Request.Context(), actor, f)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *IntelligentTestHandler) Records(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	f, ok := ParseIntelligentTestFilter(c)
	if !ok {
		return
	}
	out, err := h.svc.Records(c.Request.Context(), actor, f)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *IntelligentTestHandler) Get(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	id, ok := IntelligentTestParamID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Get(c.Request.Context(), actor, id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *IntelligentTestHandler) Image(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	id, ok := IntelligentTestParamID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Get(c.Request.Context(), actor, id)
	if response.ErrorFrom(c, err) {
		return
	}
	WriteIntelligentTestImage(c, out.ResultImage)
}
func (h *IntelligentTestHandler) Run(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var req service.IntelligentTestEnqueue
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid test request")
		return
	}
	out, err := h.svc.Enqueue(c.Request.Context(), actor, req)
	if !response.ErrorFrom(c, err) {
		response.Accepted(c, out)
	}
}
func (h *IntelligentTestHandler) Settings(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	out, err := h.svc.Settings(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"items": out})
	}
}
func (h *IntelligentTestHandler) UpdateSetting(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	var req service.IntelligentTestSetting
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid test settings")
		return
	}
	req.TestType = c.Param("test_type")
	if err := h.svc.UpdateSetting(c.Request.Context(), actor, &req); response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"updated": true})
}

func (h *IntelligentTestHandler) Cancel(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	id, ok := IntelligentTestParamID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Cancel(c.Request.Context(), actor, id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *IntelligentTestHandler) Reevaluate(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	id, ok := IntelligentTestParamID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Reevaluate(c.Request.Context(), actor, id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *IntelligentTestHandler) PreviewEvaluation(c *gin.Context) {
	actor, ok := intelligentAdminActor(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var request struct {
		Output string                        `json:"output"`
		Config service.IntelligentTestConfig `json:"config"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "无效的规则试判请求")
		return
	}
	out, err := h.svc.PreviewEvaluation(c.Request.Context(), actor, request.Output, request.Config)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func WriteIntelligentTestImage(c *gin.Context, raw string) {
	if raw == "" {
		response.NotFound(c, "test image not found")
		return
	}
	safe, err := service.SanitizeIntelligentTestSVG(raw)
	if err != nil {
		response.NotFound(c, "test image unavailable")
		return
	}
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'none'; sandbox")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "no-store, private")
	c.Header("Content-Disposition", "inline; filename=test-result.svg")
	c.Data(http.StatusOK, "image/svg+xml; charset=utf-8", []byte(safe))
}
