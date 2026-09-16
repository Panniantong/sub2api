package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// maxSecurityPolicyTranscriptLookupKeys 与 cyber 一致：限定单次 Redis 查询量，
// 保留最新的 transcript 前缀（续聊最可能命中）。
const maxSecurityPolicyTranscriptLookupKeys = 256

// SecurityPolicySessionScopeKey 会话封禁的 Redis HASH scope。
// 按分组+API Key 隔离：换 Key 即新身份，管理员可按 scope 整体解封，无需扫描。
func SecurityPolicySessionScopeKey(groupID, apiKeyID int64) string {
	return "secpol:scope:" + strconv.FormatInt(groupID, 10) + ":" + strconv.FormatInt(apiKeyID, 10)
}

func hashSecurityPolicySessionField(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "k:" + hex.EncodeToString(sum[:16])
}

// SecurityPolicyExplicitSessionKey 显式会话标识：通用头
// （session_id/x-session-id/conversation_id）优先；OpenAI 系协议额外复用
// 既有 cyber 显式 key（含 responses 转录回退语义）。
func SecurityPolicyExplicitSessionKey(protocol string, apiKeyID int64, c *gin.Context, body []byte) string {
	if c != nil && c.Request != nil {
		for _, header := range []string{"session_id", "x-session-id", "conversation_id"} {
			if id := strings.TrimSpace(c.Request.Header.Get(header)); id != "" {
				return hashSecurityPolicySessionField("secpol-explicit:v1|api_key=" + strconv.FormatInt(apiKeyID, 10) + "|" + id)
			}
		}
	}
	switch protocol {
	case ContentModerationProtocolOpenAIResponses,
		ContentModerationProtocolOpenAIChat:
		if key := CyberSessionExplicitBlockKey(apiKeyID, c, body); key != "" {
			return hashSecurityPolicySessionField(key)
		}
	}
	return ""
}

// SecurityPolicyTranscriptKeys 会话转录 lineage key：同一对话的后续轮次必然
// 包含已封禁前缀，从而被拦截；新会话（新 conversation/新 transcript）自然放行。
// 为避免“相同首句”误伤不同会话，仅当转录含模型已生成内容（即续聊）时才出 key。
func SecurityPolicyTranscriptKeys(protocol string, apiKeyID int64, body []byte) []string {
	switch protocol {
	case ContentModerationProtocolOpenAIResponses,
		ContentModerationProtocolOpenAIChat:
		return CyberSessionTranscriptBlockKeys(apiKeyID, body)
	default:
		return securityPolicyGenericTranscriptKeys(protocol, apiKeyID, body)
	}
}

// SecurityPolicySessionLookupKeys 本次请求全部待查 key：显式优先，其次转录。
func SecurityPolicySessionLookupKeys(protocol string, apiKeyID int64, c *gin.Context, body []byte) []string {
	keys := make([]string, 0, 8)
	if explicit := SecurityPolicyExplicitSessionKey(protocol, apiKeyID, c, body); explicit != "" {
		keys = append(keys, explicit)
	}
	transcript := SecurityPolicyTranscriptKeys(protocol, apiKeyID, body)
	seen := make(map[string]struct{}, len(transcript)+1)
	for _, key := range keys {
		seen[key] = struct{}{}
	}
	for _, key := range transcript {
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
		if len(keys) >= maxSecurityPolicyTranscriptLookupKeys+1 {
			break
		}
	}
	return keys
}

func securityPolicyGenericTranscriptKeys(protocol string, apiKeyID int64, body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	var root gjson.Result
	switch protocol {
	case ContentModerationProtocolAnthropicMessages:
		root = gjson.GetBytes(body, "messages")
	case ContentModerationProtocolGemini:
		root = gjson.GetBytes(body, "contents")
	default:
		return nil
	}
	if !root.IsArray() {
		return nil
	}
	h := sha256.New()
	_, _ = h.Write([]byte("secpol-transcript:v1|api_key="))
	_, _ = h.Write([]byte(strconv.FormatInt(apiKeyID, 10)))
	keys := make([]string, 0, 8)
	nextSlot := 0
	rotated := false
	lastKey := ""
	hasModelGenerated := false
	root.ForEach(func(_, item gjson.Result) bool {
		role, text := securityPolicyTranscriptItemText(protocol, item)
		if strings.TrimSpace(text) == "" {
			return true
		}
		if securityPolicyTranscriptItemIsModelGenerated(protocol, role) {
			hasModelGenerated = true
		}
		canonical, _ := json.Marshal(map[string]string{"role": role, "text": text})
		_, _ = h.Write([]byte("|item="))
		_, _ = h.Write(canonical)
		lastKey = hex.EncodeToString(h.Sum(nil))
		if len(keys) < maxSecurityPolicyTranscriptLookupKeys {
			keys = append(keys, lastKey)
		} else {
			keys[nextSlot] = lastKey
			nextSlot = (nextSlot + 1) % maxSecurityPolicyTranscriptLookupKeys
			rotated = true
		}
		return true
	})
	if !hasModelGenerated {
		// 首轮（无模型历史）：不输出 lineage key，避免相同首句误伤其他会话。
		return nil
	}
	if rotated {
		ordered := make([]string, 0, len(keys))
		ordered = append(ordered, keys[nextSlot:]...)
		ordered = append(ordered, keys[:nextSlot]...)
		return ordered
	}
	return keys
}

// securityPolicyTranscriptItemText 提取单条消息的角色与纯文本。
func securityPolicyTranscriptItemText(protocol string, item gjson.Result) (string, string) {
	switch protocol {
	case ContentModerationProtocolAnthropicMessages:
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		content := item.Get("content")
		if content.Type == gjson.String {
			return role, content.String()
		}
		var sb strings.Builder
		content.ForEach(func(_, block gjson.Result) bool {
			if text := block.Get("text"); text.Type == gjson.String && strings.TrimSpace(text.String()) != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(text.String())
			}
			return true
		})
		return role, sb.String()
	default: // gemini contents[].parts[].text
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		var sb strings.Builder
		item.Get("parts").ForEach(func(_, part gjson.Result) bool {
			if text := part.Get("text"); text.Type == gjson.String && strings.TrimSpace(text.String()) != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(text.String())
			}
			return true
		})
		return role, sb.String()
	}
}

func securityPolicyTranscriptItemIsModelGenerated(protocol, role string) bool {
	switch role {
	case "assistant", "model":
		return true
	}
	return false
}

// SecurityPolicySessionStoreKeyForAdmin 供管理端按分组+Key 解封时复用同一 scope。
func SecurityPolicySessionStoreKeyForAdmin(groupID, apiKeyID int64) string {
	return SecurityPolicySessionScopeKey(groupID, apiKeyID)
}
