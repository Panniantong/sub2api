package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// openAICodexRelayMintResult is deliberately smaller than the FC response. The
// relay is an upstream transport; account/Host persistence remains local.
type openAICodexRelayMintResult struct {
	Ticket     string
	Cookie     string
	Gateway    string
	ExpiresAt  time.Time
	StatusCode int
	Body       string
}

func (s *OpenAIGatewayService) mintOpenAICodexViaRelay(ctx context.Context, account *Account, token, model string, payload []byte, cfg config.OpenAICodexTicketConfig) (openAICodexRelayMintResult, error) {
	var result openAICodexRelayMintResult
	if !cfg.RelayEnabled || !cfg.RelayAllowMint || !strings.EqualFold(strings.TrimSpace(cfg.RelayMode), "mint") {
		return result, fmt.Errorf("relay mint is disabled")
	}
	relayURL := strings.TrimSpace(cfg.RelayURL)
	parsed, err := url.Parse(relayURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return result, fmt.Errorf("invalid relay URL")
	}
	if strings.TrimSpace(cfg.RelayKey) == "" {
		return result, fmt.Errorf("relay key is empty")
	}
	timeout := time.Duration(cfg.RelayTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 75 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, relayURL, bytes.NewReader(payload))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Relay-Key", strings.TrimSpace(cfg.RelayKey))
	req.Header.Set("X-Relay-Mint", "1")
	req.Header.Set("X-Mint-Model", strings.TrimSpace(model))
	req.Header.Set("X-Mint-Transport", "sse")
	if account != nil {
		if err := resolveAndSetOpenAIChatGPTAccountHeaders(requestCtx, s.accountRepo, req.Header, account); err != nil {
			return result, err
		}
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if readErr != nil {
		return result, readErr
	}
	if len(body) > 2<<20 {
		return result, fmt.Errorf("relay response is too large")
	}
	result.StatusCode = resp.StatusCode
	result.Body = string(body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := strings.TrimSpace(resp.Header.Get("X-Relay-Error"))
		if code == "" {
			code = resp.Status
		}
		return result, fmt.Errorf("relay mint failed: %s", code)
	}
	var wire struct {
		TurnState string            `json:"turn_state"`
		Ticket    string            `json:"ticket"`
		Cookie    string            `json:"cookie_header"`
		Cookies   map[string]string `json:"cookies"`
		Gateway   string            `json:"gateway"`
		ExpiresAt string            `json:"expires_at"`
		Tickets   map[string]struct {
			TurnState string `json:"turn_state"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return result, fmt.Errorf("decode relay mint response: %w", err)
	}
	result.Ticket = strings.TrimSpace(wire.TurnState)
	if result.Ticket == "" {
		result.Ticket = strings.TrimSpace(wire.Ticket)
	}
	if result.Ticket == "" {
		for _, ticket := range wire.Tickets {
			if candidate := strings.TrimSpace(ticket.TurnState); candidate != "" {
				result.Ticket = candidate
				break
			}
		}
	}
	result.Cookie = normalizeRelayRouteCookieHeader(wire.Cookie)
	if result.Cookie == "" && len(wire.Cookies) > 0 {
		result.Cookie = relayCookieHeader(wire.Cookies)
	}
	result.Gateway = strings.TrimSpace(wire.Gateway)
	if raw := strings.TrimSpace(wire.ExpiresAt); raw != "" {
		result.ExpiresAt, _ = time.Parse(time.RFC3339Nano, raw)
	}
	if result.Ticket == "" && result.Cookie == "" {
		return result, fmt.Errorf("relay response contains neither ticket nor route cookie")
	}
	return result, nil
}

func relayCookieHeader(values map[string]string) string {
	parts := make([]string, 0, 2)
	for _, name := range []string{"__cflb", "__oailb"} {
		if value := strings.TrimSpace(values[name]); value != "" && !strings.ContainsAny(value, "\r\n;,") {
			parts = append(parts, name+"="+value)
		}
	}
	if len(parts) != 2 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func normalizeRelayRouteCookieHeader(raw string) string {
	values := make(map[string]string, 2)
	for _, part := range strings.Split(raw, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if !ok || (name != "__cflb" && name != "__oailb") || strings.ContainsAny(value, "\r\n;,\x00") {
			continue
		}
		values[name] = strings.TrimSpace(value)
	}
	return relayCookieHeader(values)
}

// acceptOpenAICodexRelayMint applies the same local persistence and validation
// rules as a direct harvest. A relay response is never trusted as a binding
// decision by itself.
func (s *OpenAIGatewayService) acceptOpenAICodexRelayMint(ctx context.Context, account *Account, token, model, sessionID string, cfg config.OpenAICodexTicketConfig, cookieSettings *OpenAICookieSettings, cookieLog *OpenAICookieAcquisitionLog, result openAICodexRelayMintResult) bool {
	now := time.Now()
	responseCookie := strings.TrimSpace(result.Cookie)
	if responseCookie != "" && !openAICodexCookieHostAllowed(responseCookie, cfg.CookieHostWhitelist) {
		return false
	}
	if cookieSettings != nil {
		postResponseCtx, postResponseCancel := newOpenAICookiePostResponseContext(ctx)
		defer postResponseCancel()
		if responseCookie == "" {
			return false
		}
		host, payload := openAICodexCookieJWTInfo(responseCookie)
		exp, ok := payload["exp"].(float64)
		if host == "" || !ok || exp <= float64(now.Unix()) {
			return false
		}
		if cookieLog != nil {
			cookieLog.StatusCode = result.StatusCode
			cookieLog.Cookie = responseCookie
			cookieLog.Host = host
			cookieLog.Payload = payload
			cookieLog.Response = truncateOpenAICodexValidationResponse(result.Body)
			cookieLog.Message = "Relay mint 已返回 Cookie Host"
		}
		entry := OpenAICodexCookieLibraryEntry{Host: host, Cookie: responseCookie, Payload: payload, CapturedAt: now, ExpiresAt: time.Unix(int64(exp), 0)}
		persistCtx, persistCancel := context.WithTimeout(postResponseCtx, openAICookiePersistTimeout)
		err := s.settingService.UpsertOpenAICodexCookie(persistCtx, entry)
		persistCancel()
		if err != nil {
			if cookieLog != nil {
				cookieLog.Message = "Relay Cookie 保存失败: " + err.Error()
			}
			return false
		}
		if cookieLog != nil {
			cookieLog.Success = true
			cookieLog.Message = "Relay Cookie 已保存，等待本地 Host 验证"
		}
		boundHost := openAICodexCookieHostFromAccount(account)
		rotationDue := cookieSettings.CookieRotationEnabled && openAICookieRotationDue(account, now)
		if (cookieSettings.AutoValidateHost || cookieSettings.CookieRotationEnabled) &&
			(boundHost == "" || (rotationDue && boundHost != host)) &&
			!openAICodexCookieHostCooldownUntil(account, host).After(now) {
			_, _ = s.validateBindAndBuildOpenAICookieHost(postResponseCtx, account, cookieSettings, host, responseCookie, "", model, token)
		}
		return true
	}

	ticket := strings.TrimSpace(result.Ticket)
	if !plausibleOpenAICodexTicket(ticket, cfg.TargetLength) {
		return false
	}
	expiresAt := result.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.Add(time.Duration(cfg.TTLSeconds) * time.Second)
	}
	stored := s.storeOpenAICodexTicket(account, ticket, now, cfg)
	if stored == nil || !stored.valid(now, cfg.TargetLength) {
		return false
	}
	s.recordOpenAICodexTicketObservationWithResponse(account, "", ticket, ticket, sessionID, responseCookie, "relay_mint", result.StatusCode, now, expiresAt, result.Body)
	return true
}
