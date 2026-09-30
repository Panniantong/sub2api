package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Cookie scheduling is a transient admission decision. It never writes Status,
// Schedulable or a cooldown, so collectors/rotation can recover the binding and
// manually disabled accounts remain disabled after recovery.
func openAICookieSchedulingStatus(account *Account, settings *OpenAICookieSettings, now time.Time) (bool, string) {
	if cookieHostMonitorOwns(account, settings) {
		return true, "cookie_host_monitoring"
	}
	// Host rotation groups define the scope. This protection is independent from
	// the persistent account schedulable switch and from the optional UI toggle.
	if !isOpenAICodexTicketAccount(account) || settings == nil || !isOpenAICookieRotationGroupAccount(account, settings) {
		return false, ""
	}
	if normalizeOpenAICookieHost(account.GetExtraString(openAICodexCookieHostExtraKey)) == "" {
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
	if reason != "" {
		settings, err := s.settingService.GetOpenAICookieSettings(ctx)
		if err == nil {
			target := cookieDegradedTarget(account, settings, time.Now())
			if group, ok := ctx.Value(cookieSchedulingGroupKey{}).(int64); ok && target > 0 && group == target {
				return ""
			}
		}
	}
	return reason
}

type cookieSchedulingGroupKey struct{}

// Rotation changes eligibility without changing persistent group membership.
// Read these small, explicitly selected pools from the database so stale
// scheduler buckets cannot hide recovered accounts or delay degradation.
func (s *OpenAIGatewayService) cookieGroupNeedsLiveAccounts(ctx context.Context, groupID *int64, platform string) (bool, error) {
	if platform != PlatformOpenAI || s.settingService == nil {
		return false, nil
	}
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return false, err
	}
	if groupID == nil {
		return false, nil
	}
	if settings.DegradedGroupID > 0 && *groupID == settings.DegradedGroupID {
		return true, nil
	}
	for _, id := range settings.RotationGroupIDs {
		if id == *groupID {
			return true, nil
		}
	}
	return false, nil
}

func withCookieSchedulingGroup(ctx context.Context, groupID *int64) context.Context {
	return context.WithValue(ctx, cookieSchedulingGroupKey{}, derefGroupID(groupID))
}

func cookieDegradedTarget(account *Account, settings *OpenAICookieSettings, now time.Time) int64 {
	if settings == nil || settings.DegradedGroupID <= 0 {
		return 0
	}
	_, reason := openAICookieSchedulingStatus(account, settings, now)
	switch reason {
	case "cookie_host_unbound":
		if settings.rotationCandidates == nil {
			return 0 // Unknown library state is not proof of exhaustion.
		}
		entries, err := settings.rotationCandidates()
		if err != nil {
			return 0
		}
		for _, entry := range entries {
			if openAICookieRotationCandidate(account, entry, now) {
				return 0
			}
		}
		return settings.DegradedGroupID
	case "cookie_host_binding_invalid", "cookie_host_binding_expired":
		return settings.DegradedGroupID
	}
	return 0
}

// Shared by rotation, admission and the account-list count. Network failures
// do not remove a candidate; only actual expiry or account cooldown does.
func openAICookieRotationCandidate(account *Account, entry OpenAICodexCookieLibraryEntry, now time.Time) bool {
	host := normalizeOpenAICookieHost(entry.Host)
	return account != nil && host != "" && strings.TrimSpace(entry.Cookie) != "" &&
		host != normalizeOpenAICookieHost(account.GetExtraString(openAICodexCookieHostExtraKey)) &&
		entry.ExpiresAt.After(now) && !openAICodexCookieHostCooldownUntil(account, host).After(now)
}

type cookieCandidateSnapshot struct {
	entries []OpenAICodexCookieLibraryEntry
	until   time.Time
}

const cookieSchedulingCandidateCacheTTL = 250 * time.Millisecond

// Avoid reading the whole library for every account on each scheduling pass.
// Writes invalidate this snapshot; expiry/cooldown are still checked live.
func (s *SettingService) cookieSchedulingCandidates(ctx context.Context) ([]OpenAICodexCookieLibraryEntry, error) {
	s.cookieCandidatesMu.Lock()
	defer s.cookieCandidatesMu.Unlock()
	if cached := s.cookieCandidatesCache; cached != nil && time.Now().Before(cached.until) {
		return cached.entries, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAICodexCookieLibrary)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	var entries []OpenAICodexCookieLibraryEntry
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return nil, err
		}
	}
	entries = normalizeOpenAICodexCookieLibrary(entries, time.Now())
	s.cookieCandidatesCache = &cookieCandidateSnapshot{entries: entries, until: time.Now().Add(cookieSchedulingCandidateCacheTTL)}
	return entries, nil
}

func (s *OpenAIGatewayService) cookieDegradedGroup(ctx context.Context, account *Account) int64 {
	if s == nil || s.settingService == nil {
		return 0
	}
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return 0
	}
	return cookieDegradedTarget(account, settings, time.Now())
}

func (s *OpenAIGatewayService) cookieDegradationConfigured(ctx context.Context) bool {
	if s == nil || s.settingService == nil {
		return false
	}
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	return err == nil && settings.DegradedGroupID > 0
}

// Only request-local copies suppress expired Host transport; neither group
// membership nor the binding used by the rotation worker is persisted here.
func (s *OpenAIGatewayService) cookieDegradedRequestAccount(ctx context.Context, account *Account) *Account {
	if account == nil {
		return nil
	}
	if target := s.cookieDegradedGroup(ctx, account); target > 0 {
		group, _ := ctx.Value(cookieSchedulingGroupKey{}).(int64)
		if group == target {
			copy := *account
			copy.Extra = make(map[string]any, len(account.Extra))
			for key, value := range account.Extra {
				copy.Extra[key] = value
			}
			copy.openaiCookieDegraded = true
			return &copy
		}
	}
	return account
}
