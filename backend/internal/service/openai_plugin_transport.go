package service

import (
	"net/http"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	request = WithAccountTrafficRequest(request, account)
	return accountTrafficController(s.httpUpstream).DoHTTP(request, func(controlled *http.Request) (*http.Response, error) {
		return s.doOpenAIUpstreamWithoutTraffic(controlled, proxyURL, account)
	})
}
func (s *OpenAIGatewayService) doOpenAIUpstreamWithoutTraffic(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	profile, err := resolveMode1TLSProfile(account)
	if err != nil {
		return nil, err
	}
	if profile != nil && (s.cfg == nil || s.cfg.Gateway.TLSFingerprint.Enabled) && (s.pluginManager == nil || !s.pluginManager.ShouldRouteOpenAIOAuth(account)) {
		return s.httpUpstream.DoWithTLS(WithAccountTrafficRequest(request, account), proxyURL, account.ID, account.Mode1EffectiveConcurrency(), profile)
	}
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return captureIntelligentResponse(request, response), err
		}
	}
	return s.httpUpstream.Do(WithAccountTrafficRequest(request, account), proxyURL, account.ID, account.Mode1EffectiveConcurrency())
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	request = WithAccountTrafficRequest(request, account)
	return accountTrafficController(s.httpUpstream).DoHTTP(request, func(controlled *http.Request) (*http.Response, error) {
		return s.doOpenAIAccountTestUpstreamWithoutTraffic(controlled, proxyURL, account, useTLSFallback)
	})
}
func (s *AccountTestService) doOpenAIAccountTestUpstreamWithoutTraffic(request *http.Request, proxyURL string, account *Account, useTLSFallback bool) (*http.Response, error) {
	profile, err := resolveMode1TLSProfile(account)
	if err != nil {
		return nil, err
	}
	if profile != nil && (s.cfg == nil || s.cfg.Gateway.TLSFingerprint.Enabled) && (s.pluginManager == nil || !s.pluginManager.ShouldRouteOpenAIOAuth(account)) {
		return s.httpUpstream.DoWithTLS(WithAccountTrafficRequest(request, account), proxyURL, account.ID, account.Mode1EffectiveConcurrency(), profile)
	}
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return captureIntelligentResponse(request, response), err
		}
	}
	if useTLSFallback && !isMode1ProtectionEnabled(account) {
		return s.httpUpstream.DoWithTLS(
			WithAccountTrafficRequest(request, account),
			proxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.httpUpstream.Do(WithAccountTrafficRequest(request, account), proxyURL, account.ID, account.Mode1EffectiveConcurrency())
}
