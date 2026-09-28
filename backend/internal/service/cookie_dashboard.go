package service

import (
	"context"
	"time"
)

type CookieDashboard struct {
	Harvest            map[string]int `json:"harvest"`
	RotationAccounts   int            `json:"rotation_accounts"`
	RotationRunning    int            `json:"rotation_running"`
	DegradedAccounts   int            `json:"degraded_accounts"`
	HarvestEnabled     bool           `json:"harvest_enabled"`
	RotationEnabled    bool           `json:"rotation_enabled"`
	HarvestConcurrency int            `json:"harvest_concurrency"`
	UpdatedAt          time.Time      `json:"updated_at"`
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
	if settings.DegradedGroupID > 0 {
		entries, err := s.settingService.cookieSchedulingCandidates(ctx)
		if err != nil {
			return nil, err
		}
		settings.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return entries, nil }
	}
	result := &CookieDashboard{
		Harvest: s.CookieHarvestRunning(), HarvestEnabled: settings.Enabled,
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
