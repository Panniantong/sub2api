package service

import (
	"context"
	"time"
)

type CookieDashboard struct {
	Harvest              map[string]int `json:"harvest"`
	RotationAccounts     int            `json:"rotation_accounts"`
	RotationRunning      int            `json:"rotation_running"`
	DegradedAccounts     int            `json:"degraded_accounts"`
	HealthyBoundAccounts int            `json:"healthy_bound_accounts"`
	HarvestEnabled       bool           `json:"harvest_enabled"`
	RotationEnabled      bool           `json:"rotation_enabled"`
	HarvestConcurrency   int            `json:"harvest_concurrency"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

func (s *OpenAIGatewayService) CookieDashboard(ctx context.Context) (*CookieDashboard, error) {
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	// Use one consistent library snapshot for the whole dashboard. Failure is
	// reported instead of turning unavailable data into a misleading zero.
	entries, err := s.settingService.cookieSchedulingCandidates(ctx)
	if err != nil {
		return nil, err
	}
	settings.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return entries, nil }
	byHost := make(map[string]OpenAICodexCookieLibraryEntry, len(entries))
	for _, entry := range entries {
		byHost[normalizeOpenAICookieHost(entry.Host)] = entry
	}
	result := &CookieDashboard{
		Harvest: s.CookieHarvestRunning(), HarvestEnabled: settings.Enabled && !settings.LocalHarvestDisabled && (len(settings.AccountIDs) > 0 || settings.AccountID > 0 || len(settings.GroupIDs) > 0),
		RotationEnabled:    settings.Enabled && settings.CookieRotationEnabled,
		HarvestConcurrency: settings.CookieHarvestConcurrency, UpdatedAt: time.Now(),
	}
	seen := make(map[int64]bool, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		if seen[a.ID] || !isOpenAICodexTicketAccount(a) {
			continue
		}
		seen[a.ID] = true
		if cookieDashboardHealthyBinding(a, settings, byHost, result.UpdatedAt) {
			result.HealthyBoundAccounts++
		}
		if isOpenAICookieRotationAccount(a, settings) {
			result.RotationAccounts++
		}
		// Match the account-list virtual degradation marker, including disabled
		// accounts. This counter does not imply that all of them are schedulable.
		if cookieDegradedTarget(a, settings, result.UpdatedAt) > 0 {
			result.DegradedAccounts++
		}
	}
	// In-flight work can still finish after an administrator disables rotation.
	s.openaiCookieRotationRunning.Range(func(_, _ any) bool {
		result.RotationRunning++
		return true
	})
	return result, nil
}

func cookieDashboardHealthyBinding(a *Account, settings *OpenAICookieSettings, entries map[string]OpenAICodexCookieLibraryEntry, now time.Time) bool {
	if !a.IsSchedulable() || cookieHostMonitorOwns(a, settings) {
		return false
	}
	host := normalizeOpenAICookieHost(openAICodexCookieHostFromAccount(a))
	entry, ok := entries[host]
	if !ok || entry.Cookie == "" || !entry.ExpiresAt.After(now) || openAICodexCookieHostCooldownUntil(a, host).After(now) {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_host_binding_expires_at"))
	return err == nil && expires.After(now)
}
