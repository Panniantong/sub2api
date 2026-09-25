package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/gemini"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// streamOnlyJSONRoutes 是请求体里用 `stream` 选择流式的对话生成入口（路由模板）。
// Responses 子路径（如 /responses/compact）与 count_tokens、模型列表、Embeddings、
// 图片视频等接口本来就没有流式可选，不在其列；Responses WebSocket 本身就是流式。
var streamOnlyJSONRoutes = map[string]bool{
	"/v1/messages":                 true,
	"/antigravity/v1/messages":     true,
	"/v1/chat/completions":         true,
	"/chat/completions":            true,
	"/v1/responses":                true,
	"/responses":                   true,
	"/backend-api/codex/responses": true,
}

// streamOnlyGeminiRoutes 是 Gemini 原生入口：流式与否由 URL 里的动作决定。
var streamOnlyGeminiRoutes = map[string]bool{
	"/v1beta/models/*modelAction":             true,
	"/antigravity/v1beta/models/*modelAction": true,
}

const (
	streamRequiredMessage       = `This group only accepts streaming requests; set "stream": true`
	streamRequiredGeminiMessage = "This group only accepts streaming requests; use :streamGenerateContent"
)

// GroupStreamOnly 是分组「仅允许流式请求」的准入中间件。
//
// 挂载位置与 GroupModelAllowlist 相同：apiKeyAuth 之后、compositeTarget 之前，
// 在调度与转发之前按客户端原始请求判断。
//
// 行为：
//   - 快速路径：分组未开启时直接放行，不读请求体；只检查 POST。
//   - JSON 入口：请求体必须是合法 JSON，且顶层所有键名大小写不敏感等于 "stream" 的值都是 true、
//     至少出现一次。下游 gjson 取第一个、encoding/json 取最后一个且不分大小写，任何一处缺省、
//     false 或非布尔都可能被某个解析器当成非流式，因此一律拒绝。
//   - Gemini 原生入口：动作为 generateContent 时拒绝；streamGenerateContent、countTokens 放行，
//     其余动作由 handler 返回不支持。
//   - 拒绝：按入口协议格式返回 400，标记运维业务限流原因 local_policy_denied 与 ingress
//     拒绝原因 stream_required。
func GroupStreamOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.Group == nil || !apiKey.Group.StreamOnly ||
			c.Request == nil || c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		route := c.FullPath()
		switch {
		case streamOnlyJSONRoutes[route]:
			body, ok := readAdmissionRequestBody(c)
			if !ok {
				// 请求体读取失败（如超限 413）已写出响应。
				return
			}
			if !jsonBodyRequestsStream(body) {
				rejectNonStreamRequest(c, route)
				return
			}
		case streamOnlyGeminiRoutes[route]:
			_, action, parsed := gemini.ParseModelAction(strings.TrimPrefix(c.Param("modelAction"), "/"))
			if parsed && action == gemini.ActionGenerateContent {
				rejectNonStreamRequest(c, route)
				return
			}
		}
		c.Next()
	}
}

// jsonBodyRequestsStream 报告请求体是否明确要求流式（规则见 GroupStreamOnly）。
func jsonBodyRequestsStream(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return false
	}
	found, streaming := false, true
	root.ForEach(func(key, value gjson.Result) bool {
		if !strings.EqualFold(key.String(), "stream") {
			return true
		}
		found = true
		if value.Type != gjson.True {
			streaming = false
			return false
		}
		return true
	})
	return found && streaming
}

// rejectNonStreamRequest 按入口协议返回 400：Gemini 用 Google 格式，Messages 用 Anthropic 格式，
// 其余（Chat Completions / Responses）用 OpenAI 格式。
func rejectNonStreamRequest(c *gin.Context, route string) {
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	MarkIngressRejected(c, IngressRejectStreamRequired)
	switch {
	case streamOnlyGeminiRoutes[route]:
		GoogleErrorWriter(c, http.StatusBadRequest, streamRequiredGeminiMessage)
	case strings.HasSuffix(route, "/messages"):
		c.JSON(http.StatusBadRequest, gin.H{
			"type":  "error",
			"error": gin.H{"type": "invalid_request_error", "message": streamRequiredMessage},
		})
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": streamRequiredMessage,
				"type":    "invalid_request_error",
				"param":   "stream",
				"code":    "stream_required",
			},
		})
	}
	c.Abort()
}
