package admin

import (
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SecurityPolicyHandler 管理安全策略自定义词与会话解封。
// 分组开关本身走分组管理接口（PUT /admin/groups/:id）。
type SecurityPolicyHandler struct {
	service *service.SecurityPolicyService
}

func NewSecurityPolicyHandler(svc *service.SecurityPolicyService) *SecurityPolicyHandler {
	return &SecurityPolicyHandler{service: svc}
}

func (h *SecurityPolicyHandler) requireService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, errors.New("security policy service unavailable"))
		return false
	}
	return true
}

// ListBuiltin 返回内置 seed 词包（只读，供管理端审计/调优参考）。
func (h *SecurityPolicyHandler) ListBuiltin(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	seeds := h.service.GetBuiltinKeywords()
	out := make([]dto.SecurityPolicyKeywordSeed, 0, len(seeds))
	for _, seed := range seeds {
		out = append(out, dto.SecurityPolicyKeywordSeedFromService(seed))
	}
	response.Success(c, gin.H{"keywords": out, "count": len(out)})
}

// ListKeywords 查询自定义词：?group_id=（缺省全部，0 仅全局）&include_disabled=true。
func (h *SecurityPolicyHandler) ListKeywords(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	var groupID *int64
	if raw := c.Query("group_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		groupID = &id
	}
	includeDisabled := c.Query("include_disabled") == "true"
	words, err := h.service.ListKeywords(c.Request.Context(), groupID, includeDisabled)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"keywords": dto.SecurityPolicyKeywordsFromService(words)})
}

type securityPolicyKeywordCreateRequest struct {
	GroupID  *int64 `json:"group_id"`
	Keyword  string `json:"keyword"`
	Category string `json:"category"`
}

// CreateKeyword 新建自定义词（group_id 缺省/0 为全局词）。
func (h *SecurityPolicyHandler) CreateKeyword(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	var req securityPolicyKeywordCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.GroupID != nil && *req.GroupID <= 0 {
		req.GroupID = nil
	}
	word, err := h.service.CreateKeyword(c.Request.Context(), service.SecurityPolicyKeywordInput{
		GroupID:  req.GroupID,
		Keyword:  req.Keyword,
		Category: req.Category,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.SecurityPolicyKeywordFromService(word))
}

type securityPolicyKeywordUpdateRequest struct {
	Keyword  *string `json:"keyword"`
	Category *string `json:"category"`
	Enabled  *bool   `json:"enabled"`
}

// UpdateKeyword 更新自定义词（词/分类/启用）。
func (h *SecurityPolicyHandler) UpdateKeyword(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid keyword ID")
		return
	}
	var req securityPolicyKeywordUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	word, err := h.service.UpdateKeyword(c.Request.Context(), id, service.SecurityPolicyKeywordUpdate{
		Keyword:  req.Keyword,
		Category: req.Category,
		Enabled:  req.Enabled,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.SecurityPolicyKeywordFromService(word))
}

// DeleteKeyword 软删除自定义词。
func (h *SecurityPolicyHandler) DeleteKeyword(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid keyword ID")
		return
	}
	if err := h.service.DeleteKeyword(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Keyword deleted successfully"})
}

type securityPolicyUnblockRequest struct {
	GroupID  int64 `json:"group_id"`
	APIKeyID int64 `json:"api_key_id"`
}

// UnblockSession 清除指定分组+Key 的全部会话封禁（需新建会话的反向操作）。
func (h *SecurityPolicyHandler) UnblockSession(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	var req securityPolicyUnblockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.GroupID <= 0 || req.APIKeyID <= 0 {
		response.BadRequest(c, "group_id and api_key_id must be positive")
		return
	}
	unblocked, err := h.service.UnblockSessionScope(c.Request.Context(), req.GroupID, req.APIKeyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"unblocked": unblocked})
}
