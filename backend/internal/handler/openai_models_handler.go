package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *GatewayHandler) pinnedOpenAIModels(c *gin.Context, group *service.Group) {
	if c.Request.Context().Err() != nil {
		return
	}
	if h.openAIGatewayService == nil {
		writeOpenAIModelsError(c, http.StatusInternalServerError, "api_error", "OpenAI model discovery is not configured")
		return
	}
	etag := modelsIfNoneMatch(c)
	if c.Param("model") != "" {
		etag = "" // A collection ETag cannot validate a single-model representation.
	}
	response, account, err := h.openAIGatewayService.FetchPinnedOpenAIModelsList(
		c.Request.Context(), group, h.maxAccountSwitches, etag,
	)
	if c.Request.Context().Err() != nil {
		return
	}
	if err != nil {
		if errors.Is(err, service.ErrNoPinnedCodexModelsAccounts) {
			writeOpenAIModelsError(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI model discovery accounts")
			return
		}
		writeOpenAIModelsError(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
		return
	}
	setOpsSelectedAccount(c, account.ID, account.Platform)
	writeOpenAIModelsResponse(c, response)
}

func writeOpenAIModelsError(c *gin.Context, status int, errorType, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": errorType, "message": message}})
}

func writeOpenAIModelsResponse(c *gin.Context, manifest *service.OpenAIModelsResponse) {
	if denied := deniedModelsForRequest(c); len(denied) > 0 && len(manifest.Body) > 0 {
		body, err := removeDeniedCatalogEntries(manifest.Body, denied)
		if err != nil {
			writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue")
			return
		}
		// 过滤后的目录因用户而异，不能沿用分组共享的 ETag 做条件缓存。
		filtered := *manifest
		filtered.Body = body
		filtered.ETag = ""
		filtered.NotModified = false
		manifest = &filtered
	}
	if c.Param("model") != "" {
		writeRetrievedModel(c, manifest.Body)
		return
	}
	if manifest.ETag != "" {
		c.Header("ETag", manifest.ETag)
	}
	if manifest.NotModified {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return
	}
	c.Data(http.StatusOK, "application/json", manifest.Body)
}

// Both discovery endpoints consume the same final catalogue, after group/platform
// selection and allowlist filtering. Preserve every field on the selected entry.
func writeModelsListResponse(c *gin.Context, models any) {
	response := gin.H{"object": "list", "data": models}
	denied := deniedModelsForRequest(c)
	if c.Param("model") == "" && len(denied) == 0 {
		c.JSON(http.StatusOK, response)
		return
	}
	body, err := json.Marshal(response)
	if err != nil {
		writeOpenAIModelsError(c, http.StatusInternalServerError, "api_error", "Failed to encode model catalogue")
		return
	}
	if len(denied) > 0 {
		if body, err = removeDeniedCatalogEntries(body, denied); err != nil {
			writeOpenAIModelsError(c, http.StatusInternalServerError, "api_error", "Failed to encode model catalogue")
			return
		}
	}
	if c.Param("model") == "" {
		c.Data(http.StatusOK, "application/json; charset=utf-8", body)
		return
	}
	writeRetrievedModel(c, body)
}

// deniedModelsForRequest 返回当前请求 API Key 所属用户在分组内被禁用的模型。
func deniedModelsForRequest(c *gin.Context) []string {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		return nil
	}
	return apiKey.DeniedModelsInGroup()
}

// modelsIfNoneMatch 返回模型目录请求的 If-None-Match；用户有禁用模型时目录要按用户过滤，
// 不能用分组共享的 ETag 命中 304，因此忽略该请求头。
func modelsIfNoneMatch(c *gin.Context) string {
	if len(deniedModelsForRequest(c)) > 0 {
		return ""
	}
	return c.GetHeader("If-None-Match")
}

// removeDeniedCatalogEntries 从模型目录（OpenAI 列表的 data[].id、Codex 清单的 models[].slug）
// 去掉被禁用的条目，保留条目与信封的其余字段。
func removeDeniedCatalogEntries(body []byte, denied []string) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	for _, list := range []struct{ field, idField string }{{"data", "id"}, {"models", "slug"}} {
		raw, ok := envelope[list.field]
		if !ok {
			continue
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			continue
		}
		kept := make([]json.RawMessage, 0, len(entries))
		for _, entry := range entries {
			var fields map[string]json.RawMessage
			var id string
			if json.Unmarshal(entry, &fields) == nil && json.Unmarshal(fields[list.idField], &id) == nil &&
				service.UserGroupDeniesModel(denied, id) {
				continue
			}
			kept = append(kept, entry)
		}
		encoded, err := json.Marshal(kept)
		if err != nil {
			return nil, err
		}
		envelope[list.field] = encoded
	}
	return json.Marshal(envelope)
}

func writeRetrievedModel(c *gin.Context, body []byte) {
	var catalog struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue")
		return
	}
	modelID := c.Param("model")
	for _, raw := range catalog.Data {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(raw, &model); err != nil {
			writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue entry")
			return
		}
		var id string
		if err := json.Unmarshal(model["id"], &id); err != nil {
			writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue ID")
			return
		}
		if id == modelID {
			c.Data(http.StatusOK, "application/json", raw)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
		"type": "invalid_request_error", "code": "model_not_found", "param": "model",
		"message": fmt.Sprintf("Model %q does not exist or is not available for this group", modelID),
	}})
}
