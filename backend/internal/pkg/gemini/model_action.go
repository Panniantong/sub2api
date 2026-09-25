package gemini

import "strings"

// Gemini 原生接口 URL `{model}:{action}` 里的动作名。
const (
	ActionGenerateContent       = "generateContent"
	ActionStreamGenerateContent = "streamGenerateContent"
	ActionCountTokens           = "countTokens"
)

// ParseModelAction 解析 Gemini 原生 URL 的 `{model}:{action}`（兼容 `{model}/{action}`），
// 入参是去掉开头 "/" 的路由参数。网关 handler 与分组准入中间件共用这一份解析，
// 保证两边对同一请求看到的是同一个动作。
func ParseModelAction(rest string) (model, action string, ok bool) {
	rest = strings.TrimSpace(rest)
	// Standard: {model}:{action}
	if i := strings.Index(rest, ":"); i > 0 && i < len(rest)-1 {
		return rest[:i], rest[i+1:], true
	}
	// Fallback: {model}/{action}
	if i := strings.Index(rest, "/"); i > 0 && i < len(rest)-1 {
		return rest[:i], rest[i+1:], true
	}
	return "", "", false
}
