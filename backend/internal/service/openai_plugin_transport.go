package service

import (
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if request != nil {
		s.applyOpenAIAccountBoundState(account, request.Header)
		s.logOpenAIAccountBoundState(request, account)
	}
	var response *http.Response
	var err error
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			if response != nil && response.Request == nil {
				response.Request = request
			}
			return response, err
		}
	}
	if s.shouldUseOpenAICodexRelayTransparent(account) {
		if relayRequest, relayProxy, ok := s.openAICodexRelayTransparentRequest(request); ok {
			response, err = s.httpUpstream.Do(relayRequest, relayProxy, account.ID, account.Concurrency)
			if response != nil && response.Request == nil {
				response.Request = relayRequest
			}
			return response, err
		}
	}
	response, err = s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
	if response != nil && response.Request == nil {
		response.Request = request
	}
	return response, err
}

func (s *OpenAIGatewayService) shouldUseOpenAICodexRelayTransparent(account *Account) bool {
	if s == nil || account == nil || openAICodexCookieHostFromAccount(account) == "" {
		return false
	}
	cfg := s.codexTicketConfig()
	return cfg.RelayEnabled && strings.EqualFold(strings.TrimSpace(cfg.RelayMode), "transparent") && strings.TrimSpace(cfg.RelayURL) != ""
}

func (s *OpenAIGatewayService) openAICodexRelayTransparentRequest(request *http.Request) (*http.Request, string, bool) {
	if request == nil || request.URL == nil {
		return nil, "", false
	}
	cfg := s.codexTicketConfig()
	base, err := url.Parse(strings.TrimSpace(cfg.RelayURL))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || strings.TrimSpace(cfg.RelayKey) == "" {
		return nil, "", false
	}
	clone := request.Clone(request.Context())
	clone.URL = base.ResolveReference(&url.URL{Path: path.Join(base.Path, request.URL.Path), RawQuery: request.URL.RawQuery})
	clone.Host = ""
	clone.RequestURI = ""
	clone.Header.Set("X-Relay-Key", strings.TrimSpace(cfg.RelayKey))
	clone.Header.Del("X-Relay-Mint")
	clone.Header.Del("X-Edge-IP")
	return clone, "", true
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if request != nil {
		s.applyOpenAIAccountBoundRequestState(account, request.Header)
		if s.openaiGatewayService != nil {
			s.openaiGatewayService.logOpenAIAccountBoundState(request, account)
		}
	}
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		return s.httpUpstream.DoWithTLS(
			request,
			proxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

// logOpenAIAccountBoundState logs only presence, lengths, counts and equality
// checks. Credential values never leave the request itself.
func (s *OpenAIGatewayService) logOpenAIAccountBoundState(request *http.Request, account *Account) {
	if request == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return
	}
	headers := request.Header
	boundTicket := ""
	if ticket := s.lookupOpenAICodexTicket(account); ticket != nil {
		boundTicket = ticket.State
	}
	boundCookie := openAICodexCookieFromAccount(account)
	outboundCookies := parseOpenAICodexCookieHeader(headers.Get("Cookie"))
	boundCookies := parseOpenAICodexCookieHeader(boundCookie)
	cookieMatches := true
	for key, value := range boundCookies {
		if outboundCookies[key] != value {
			cookieMatches = false
			break
		}
	}
	boundSessionID := account.GetOpenAISessionID()
	path := ""
	if request.URL != nil {
		path = request.URL.Path
	}
	transport := "http"
	if s.pluginManager != nil && s.pluginManager.ShouldRouteOpenAIOAuth(account) {
		transport = "plugin"
	}
	slog.Info("openai_codex_outbound_binding",
		"account_id", account.ID,
		"path", path,
		"transport", transport,
		"ticket_bound", boundTicket != "",
		"ticket_length", len(strings.TrimSpace(headers.Get(openAICodexTurnStateHeader))),
		"ticket_matches", boundTicket == "" || strings.TrimSpace(headers.Get(openAICodexTurnStateHeader)) == boundTicket,
		"cookie_bound", boundCookie != "",
		"cookie_count", len(outboundCookies),
		"cookie_matches", cookieMatches,
		"session_bound", boundSessionID != "",
		"session_present", strings.TrimSpace(headers.Get("session_id")) != "",
		"session_matches", boundSessionID == "" || (strings.TrimSpace(headers.Get("session_id")) == boundSessionID && strings.TrimSpace(headers.Get("session-id")) == boundSessionID),
	)
}
