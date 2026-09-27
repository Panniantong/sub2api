package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

const (
	IntelligenceTestHTML         = "html"
	IntelligenceTestCandy        = "candy"
	IntelligenceTestHostValidate = "host_validation"
)

var intelligenceHTMLFence = regexp.MustCompile("(?is)```(?:html)?\\s*(.*?)```")

// IntelligenceTestAccount runs one of the administrator's fixed intelligence
// prompts through the same account-bound OpenAI request path used by normal
// traffic. The frontend starts one request per case, so each stream is
// independent and can finish without blocking the other two.
func (s *AccountTestService) IntelligenceTestAccount(c *gin.Context, accountID int64, testCase string, prompt string, modelIDs ...string) error {
	ctx := c.Request.Context()
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return s.sendIntelligenceError(c, testCase, prompt, "Account not found")
	}
	if account.Platform != PlatformOpenAI {
		return s.sendIntelligenceError(c, testCase, prompt, "智力测试仅支持 OpenAI 账号")
	}

	caseName := strings.TrimSpace(testCase)
	if caseName == "" {
		caseName = IntelligenceTestHostValidate
	}
	host := openAICodexCookieHostFromAccount(account)
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	metadata := func(eventType string) TestEvent {
		return TestEvent{Type: eventType, Case: caseName, Prompt: prompt, CookieHost: host, Timestamp: startedAt}
	}
	s.sendEvent(c, metadata("test_start"))

	credentialAccount := account
	if account.IsCredentialShadow() {
		resolved, resolveErr := resolveCredentialAccount(ctx, s.accountRepo, account)
		if resolveErr != nil {
			return s.sendIntelligenceError(c, caseName, prompt, resolveErr.Error())
		}
		credentialAccount = resolved
	}

	isOAuth := credentialAccount.IsOAuth()
	authToken := ""
	if isOAuth {
		if credentialAccount.IsOpenAIAgentIdentity() {
			return s.sendIntelligenceError(c, caseName, prompt, "智力测试暂不支持 Agent Identity 账号")
		}
		authToken = credentialAccount.GetOpenAIAccessToken()
		if authToken == "" {
			return s.sendIntelligenceError(c, caseName, prompt, "账号没有可用的 OpenAI access token")
		}
	} else if credentialAccount.Type == "apikey" {
		authToken = credentialAccount.GetOpenAIProtocolAPIKey()
		if authToken == "" {
			authToken = credentialAccount.GetOpenAIApiKey()
		}
		if authToken == "" {
			return s.sendIntelligenceError(c, caseName, prompt, "账号没有可用的 OpenAI API Key")
		}
	} else {
		return s.sendIntelligenceError(c, caseName, prompt, "当前账号类型不支持智力测试")
	}

	modelID := openai.DefaultTestModel
	if len(modelIDs) > 0 && strings.TrimSpace(modelIDs[0]) != "" {
		modelID = strings.TrimSpace(modelIDs[0])
	}
	if isOAuth {
		modelID = normalizeOpenAIModelForUpstream(credentialAccount, modelID)
	}
	payload := map[string]any{
		"model": modelID,
		"input": []map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": prompt}},
		}},
		"instructions": openai.DefaultInstructions,
		"stream":       true,
	}
	if isOAuth {
		payload["store"] = false
	}
	payloadBytes, _ := json.Marshal(payload)
	apiURL := chatgptCodexAPIURL
	if !isOAuth {
		baseURL := credentialAccount.GetOpenAIBaseURL()
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		normalized, normalizeErr := s.validateUpstreamBaseURL(baseURL)
		if normalizeErr != nil {
			return s.sendIntelligenceError(c, caseName, prompt, normalizeErr.Error())
		}
		apiURL = buildOpenAIResponsesURLForPlatform(credentialAccount.Platform, normalized)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendIntelligenceError(c, caseName, prompt, "Failed to create request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	if isOAuth {
		req.Host = "chatgpt.com"
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		canonical := resolveCodexOutboundIdentity("")
		req.Header.Set("Originator", canonical.originator)
		if customUA := strings.TrimSpace(credentialAccount.GetOpenAIUserAgent()); customUA != "" {
			req.Header.Set("User-Agent", customUA)
		} else {
			req.Header.Set("User-Agent", canonical.userAgent)
		}
		setOpenAIChatGPTAccountHeaders(req.Header, credentialAccount)
		enforceCodexIdentityHeadersWithUA(req.Header, credentialAccount.GetOpenAIUserAgent())
	} else {
		req.Header.Set("Authorization", "Bearer "+authToken)
		applyOpenAICodexProbeHeaders(req.Header)
	}
	if isOAuth {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	credentialAccount.ApplyHeaderOverrides(req.Header)
	s.applyOpenAIAccountBoundRequestState(credentialAccount, req.Header)
	if isOAuth && s.openaiGatewayService != nil {
		if cookie := s.openaiGatewayService.openAICodexCookieForAccount(credentialAccount); cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var resp *http.Response
	if s.tlsFPProfileService != nil {
		resp, err = s.doOpenAIAccountTestUpstream(req, proxyURL, account, true)
	} else if s.httpUpstream != nil {
		resp, err = s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	} else {
		err = fmt.Errorf("HTTP upstream not configured")
	}
	if err != nil {
		return s.sendIntelligenceError(c, caseName, prompt, "Request failed: "+err.Error())
	}
	if resp == nil {
		return s.sendIntelligenceError(c, caseName, prompt, "Empty upstream response")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return s.sendIntelligenceError(c, caseName, prompt, fmt.Sprintf("API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}

	var output strings.Builder
	reader := bufio.NewReader(resp.Body)
	completed := false
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return s.sendIntelligenceError(c, caseName, prompt, "Stream read error: "+readErr.Error())
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if jsonStr != "" && jsonStr != "[DONE]" {
				var data map[string]any
				if json.Unmarshal([]byte(jsonStr), &data) == nil {
					eventType, _ := data["type"].(string)
					if eventType == "response.output_text.delta" {
						if delta, ok := data["delta"].(string); ok && delta != "" {
							output.WriteString(delta)
							event := metadata("content")
							event.Text = delta
							s.sendEvent(c, event)
						}
					}
					if eventType == "response.completed" || eventType == "response.done" {
						completed = true
					}
					if eventType == "response.failed" || eventType == "error" {
						return s.sendIntelligenceError(c, caseName, prompt, "模型响应失败")
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	if caseName == IntelligenceTestHTML {
		if html := extractIntelligenceHTML(output.String()); html != "" {
			event := metadata("html")
			event.Text = html
			s.sendEvent(c, event)
		}
	}
	event := metadata("test_complete")
	event.Success = completed || output.Len() > 0
	s.sendEvent(c, event)
	return nil
}

func (s *AccountTestService) sendIntelligenceError(c *gin.Context, testCase, prompt, message string) error {
	s.sendEvent(c, TestEvent{Type: "error", Case: testCase, Prompt: prompt, Error: message, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)})
	return fmt.Errorf("%s", message)
}

func extractIntelligenceHTML(raw string) string {
	value := strings.TrimSpace(raw)
	if matches := intelligenceHTMLFence.FindStringSubmatch(value); len(matches) == 2 {
		value = strings.TrimSpace(matches[1])
	}
	lower := strings.ToLower(value)
	if idx := strings.Index(lower, "<!doctype html"); idx >= 0 {
		value = value[idx:]
	} else if idx := strings.Index(lower, "<html"); idx >= 0 {
		value = value[idx:]
	} else if idx := strings.Index(lower, "<svg"); idx >= 0 {
		value = value[idx:]
	}
	return strings.TrimSpace(value)
}

// ExtractIntelligenceHTML exposes the normalized HTML extraction used by the
// asynchronous administrator test result store.
func ExtractIntelligenceHTML(raw string) string {
	return extractIntelligenceHTML(raw)
}
