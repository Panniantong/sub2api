package service

import (
	"context"
	"strings"
	"time"
)

// Cookie scheduling is a transient admission decision. It never writes Status,
// Schedulable or a cooldown, so collectors/rotation can recover the binding and
// manually disabled accounts remain disabled after recovery.
func openAICookieSchedulingStatus(account *Account, settings *OpenAICookieSettings, now time.Time) (bool, string) {
	if !isOpenAICodexTicketAccount(account) || settings == nil || !settings.CookieHostSchedulingGuardEnabled || !isOpenAICookieRotationGroupAccount(account, settings) {
		return false, ""
	}
	if openAICodexCookieHostFromAccount(account) == "" {
		return true, "cookie_host_unbound"
	}
	expires, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(account.GetExtraString("codex_cookie_host_binding_expires_at")))
	if err != nil {
		return true, "cookie_host_binding_invalid"
	}
	if !expires.After(now) {
		return true, "cookie_host_binding_expired"
	}
	// Early rotation does not invalidate the current, still usable binding.
	return true, ""
}

func (s *SettingService) openAICookieSchedulingStatus(ctx context.Context, account *Account) (bool, string) {
	if s == nil || !isOpenAICodexTicketAccount(account) {
		return false, ""
	}
	// Normal scheduling reads the short-lived settings cache, not the Cookie
	// library. Expiry itself is evaluated for every request, without waiting for
	// a background rotation task or cache refresh.
	settings, err := s.GetOpenAICookieSettings(ctx)
	if err != nil {
		// Keep a known protection policy on transient settings read failures.
		if cached, ok := s.openAICookieCache.Load().(*cachedOpenAICookieSettings); ok {
			settings = &cached.value
		}
	}
	return openAICookieSchedulingStatus(account, settings, time.Now())
}

func (s *OpenAIGatewayService) openAICookieSchedulingBlockReason(ctx context.Context, account *Account) string {
	if s == nil || s.settingService == nil {
		return ""
	}
	_, reason := s.settingService.openAICookieSchedulingStatus(ctx, account)
	return reason
}
