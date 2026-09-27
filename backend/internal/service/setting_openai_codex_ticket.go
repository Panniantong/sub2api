package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const openAICodexTicketSettingsCacheTTL = 5 * time.Second

type cachedOpenAICodexTicketSettings struct {
	settings  OpenAICodexTicketSettings
	expiresAt int64
}

func normalizeOpenAICodexTicketSettings(settings OpenAICodexTicketSettings) OpenAICodexTicketSettings {
	settings.Model = strings.TrimSpace(settings.Model)
	if settings.Model == "" {
		settings.Model = openAICodexTicketHarvestModel
	}
	if len(settings.Model) > 128 {
		settings.Model = settings.Model[:128]
	}
	if settings.TTLSeconds <= 0 {
		settings.TTLSeconds = 3600
	}
	if settings.RefreshBeforeSeconds <= 0 {
		settings.RefreshBeforeSeconds = 600
	}
	if settings.RetryIntervalSeconds <= 0 {
		settings.RetryIntervalSeconds = 5
	}
	if settings.CookieWSConnections <= 0 {
		settings.CookieWSConnections = 10
	}
	if settings.CookieWSConnectionTTLSeconds <= 0 {
		settings.CookieWSConnectionTTLSeconds = 3600
	}
	if settings.CookieWSHostCooldownSeconds <= 0 {
		settings.CookieWSHostCooldownSeconds = 14400
	}
	if settings.RelayTimeoutSeconds <= 0 {
		settings.RelayTimeoutSeconds = 75
	}
	settings.RelayURL = strings.TrimSpace(settings.RelayURL)
	settings.RelayKey = strings.TrimSpace(settings.RelayKey)
	settings.RelayMode = strings.ToLower(strings.TrimSpace(settings.RelayMode))
	if settings.RelayMode == "" {
		settings.RelayMode = "mint"
	}
	if settings.RelayMode != "mint" && settings.RelayMode != "transparent" {
		settings.RelayMode = "mint"
	}
	if settings.RefreshBeforeSeconds >= settings.TTLSeconds {
		settings.RefreshBeforeSeconds = settings.TTLSeconds - 1
		if settings.RefreshBeforeSeconds < 1 {
			settings.RefreshBeforeSeconds = 1
		}
	}
	settings.HarvestProxyURL = strings.TrimSpace(settings.HarvestProxyURL)
	settings.CookieHostWhitelist = normalizeOpenAICodexCookieHostWhitelist(settings.CookieHostWhitelist)
	settings.CookieHarvestProxyURLs = normalizeOpenAICodexProxyURLs(settings.CookieHarvestProxyURLs)
	return settings
}

func normalizeOpenAICodexCookieHostWhitelist(hosts []string) []string {
	seen := make(map[string]struct{}, len(hosts))
	result := make([]string, 0, len(hosts))
	for _, raw := range hosts {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ';' }) {
			host := strings.ToLower(strings.TrimSpace(part))
			host = strings.TrimSuffix(host, ".")
			if host == "" || strings.ContainsAny(host, "/\\:@ ") || len(host) > 253 {
				continue
			}
			if _, ok := seen[host]; ok {
				continue
			}
			seen[host] = struct{}{}
			result = append(result, host)
		}
	}
	return result
}

func normalizeOpenAICodexProxyURLs(rawURLs []string) []string {
	seen := make(map[string]struct{}, len(rawURLs))
	result := make([]string, 0, len(rawURLs))
	for _, raw := range rawURLs {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' }) {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}
			parsed, err := url.Parse(value)
			if err != nil || parsed.Host == "" {
				continue
			}
			scheme := strings.ToLower(parsed.Scheme)
			if scheme != "http" && scheme != "https" && scheme != "socks5" && scheme != "socks5h" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func defaultOpenAICodexTicketSettings(cfg *config.Config) OpenAICodexTicketSettings {
	value := config.OpenAICodexTicketConfig{}
	if cfg != nil {
		value = cfg.Gateway.OpenAICodexTicket
	}
	value = OpenAICodexTicketConfigDefaults(value)
	return normalizeOpenAICodexTicketSettings(OpenAICodexTicketSettings{
		Enabled:                      value.Enabled,
		Model:                        firstOpenAICodexTicketModel(value.Models),
		TTLSeconds:                   value.TTLSeconds,
		RefreshBeforeSeconds:         value.RefreshBeforeSeconds,
		RetryIntervalSeconds:         value.HarvestProbeIntervalSeconds,
		HarvestProxyURL:              value.HarvestProxyURL,
		CookieHostWhitelist:          value.CookieHostWhitelist,
		CookieHarvestProxyURLs:       value.CookieHarvestProxyURLs,
		OverrideTurnState:            value.OverrideTurnState,
		CookieWSConnections:          value.CookieWSConnections,
		CookieWSConnectionTTLSeconds: value.CookieWSConnectionTTLSeconds,
		CookieWSHostCooldownSeconds:  value.CookieWSHostCooldownSeconds,
		RelayEnabled:                 value.RelayEnabled,
		RelayURL:                     value.RelayURL,
		RelayKey:                     value.RelayKey,
		RelayMode:                    value.RelayMode,
		RelayTimeoutSeconds:          value.RelayTimeoutSeconds,
		RelayAllowMint:               value.RelayAllowMint,
	})
}

func (s *SettingService) GetOpenAICodexTicketSettings(ctx context.Context) (*OpenAICodexTicketSettings, error) {
	defaults := defaultOpenAICodexTicketSettings(nil)
	if s != nil {
		defaults = defaultOpenAICodexTicketSettings(s.cfg)
	}
	if s == nil || s.settingRepo == nil {
		return &defaults, nil
	}
	if cached, ok := s.openAICodexTicketCache.Load().(*cachedOpenAICodexTicketSettings); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		value := cached.settings
		return &value, nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAICodexTicketSettings)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, fmt.Errorf("get OpenAI Codex ticket settings: %w", err)
	}
	settings := defaults
	if strings.TrimSpace(value) != "" {
		if err := json.Unmarshal([]byte(value), &settings); err != nil {
			settings = defaults
		}
	}
	settings = normalizeOpenAICodexTicketSettings(settings)
	s.openAICodexTicketCache.Store(&cachedOpenAICodexTicketSettings{
		settings:  settings,
		expiresAt: time.Now().Add(openAICodexTicketSettingsCacheTTL).UnixNano(),
	})
	return &settings, nil
}

func (s *SettingService) SetOpenAICodexTicketSettings(ctx context.Context, settings *OpenAICodexTicketSettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("setting repository is unavailable")
	}
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	settings.HarvestProxyURL = strings.TrimSpace(settings.HarvestProxyURL)
	settings.CookieHostWhitelist = normalizeOpenAICodexCookieHostWhitelist(settings.CookieHostWhitelist)
	settings.CookieHarvestProxyURLs = normalizeOpenAICodexProxyURLs(settings.CookieHarvestProxyURLs)
	settings.Model = strings.TrimSpace(settings.Model)
	if settings.Model == "" {
		return fmt.Errorf("model is required")
	}
	if len(settings.Model) > 128 {
		return fmt.Errorf("model must be at most 128 characters")
	}
	if settings.TTLSeconds < 60 || settings.TTLSeconds > 86400 {
		return fmt.Errorf("ttl_seconds must be between 60 and 86400")
	}
	if settings.RefreshBeforeSeconds < 1 || settings.RefreshBeforeSeconds >= settings.TTLSeconds {
		return fmt.Errorf("refresh_before_seconds must be between 1 and ttl_seconds - 1")
	}
	if settings.RetryIntervalSeconds < 1 || settings.RetryIntervalSeconds > 300 {
		return fmt.Errorf("retry_interval_seconds must be between 1 and 300")
	}
	if settings.CookieWSConnections < 1 || settings.CookieWSConnections > 64 {
		return fmt.Errorf("cookie_ws_connections must be between 1 and 64")
	}
	if settings.CookieWSConnectionTTLSeconds < 60 || settings.CookieWSConnectionTTLSeconds > 86400 {
		return fmt.Errorf("cookie_ws_connection_ttl_seconds must be between 60 and 86400")
	}
	if settings.CookieWSHostCooldownSeconds < 0 || settings.CookieWSHostCooldownSeconds > 604800 {
		return fmt.Errorf("cookie_ws_host_cooldown_seconds must be between 0 and 604800")
	}
	settings.RelayURL = strings.TrimSpace(settings.RelayURL)
	settings.RelayKey = strings.TrimSpace(settings.RelayKey)
	settings.RelayMode = strings.ToLower(strings.TrimSpace(settings.RelayMode))
	if settings.RelayMode == "" {
		settings.RelayMode = "mint"
	}
	if settings.RelayMode != "mint" && settings.RelayMode != "transparent" {
		return fmt.Errorf("relay_mode must be mint or transparent")
	}
	if settings.RelayTimeoutSeconds < 1 || settings.RelayTimeoutSeconds > 180 {
		return fmt.Errorf("relay_timeout_seconds must be between 1 and 180")
	}
	if settings.RelayEnabled && settings.RelayURL == "" {
		return fmt.Errorf("relay_url is required when relay_enabled is true")
	}
	if settings.HarvestProxyURL != "" {
		parsed, err := url.Parse(settings.HarvestProxyURL)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("harvest_proxy_url must be a complete proxy URL")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("harvest_proxy_url supports http, https, socks5, or socks5h")
		}
	}
	for _, proxyURL := range settings.CookieHarvestProxyURLs {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("cookie_harvest_proxy_urls contains an invalid proxy URL")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("cookie_harvest_proxy_urls supports http, https, socks5, or socks5h")
		}
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal OpenAI Codex ticket settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOpenAICodexTicketSettings, string(data)); err != nil {
		return err
	}
	value := *settings
	s.openAICodexTicketCache.Store(&cachedOpenAICodexTicketSettings{
		settings:  value,
		expiresAt: time.Now().Add(openAICodexTicketSettingsCacheTTL).UnixNano(),
	})
	return nil
}
