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
	if cookieOverlapContinuousService(account, settings, now) {
		if cookieOverlapQuotaPaused(account, now) {
			return true, "cookie_validation_paused"
		}
		// Pool membership controls background rotation capacity, not service
		// admission for native accounts in the overlapping destination group.
		return cookieHostBindingStatus(account, settings, now)
	}
	if isOpenAICodexTicketAccount(account) && cookiePoolPolicy(account, settings) != nil {
		if _, reason := cookiePoolMember(account, settings); reason != "" {
			return true, reason
		}
	}
	return cookieHostBindingStatus(account, settings, now)
}

// Temporary degradation never grants this exception: the destination must be
// an actual account group and an explicitly configured rotation source.
func cookieOverlapContinuousService(account *Account, settings *OpenAICookieSettings, now time.Time) bool {
	if account == nil || settings == nil || !isOpenAICodexTicketAccount(account) || !account.IsSchedulable() || settings.DegradedGroupID <= 0 || cookieHostMonitorOwns(account, settings) || !cookieAccountInGroup(account, settings.DegradedGroupID) {
		return false
	}
	found := false
	for _, id := range settings.RotationGroupIDs {
		if id == settings.DegradedGroupID {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	if p := cookiePoolPolicy(account, settings); p != nil {
		if p.GroupID != settings.DegradedGroupID || !p.Enabled || !settings.Enabled || !settings.CookieRotationEnabled {
			return false
		}
	}
	return true
}

func cookieOverlapQuotaPaused(account *Account, now time.Time) bool {
	until, _ := time.Parse(time.RFC3339Nano, account.GetExtraString("codex_cookie_validation_resume_at"))
	return until.After(now)
}

func cookieHostBindingStatus(account *Account, settings *OpenAICookieSettings, now time.Time) (bool, string) {
	if cookieHostMonitorOwns(account, settings) {
		return true, "cookie_host_monitoring"
	}
	// Host rotation groups define the scope. This protection is independent from
	// the persistent account schedulable switch and from the optional UI toggle.
	if !isOpenAICodexTicketAccount(account) || settings == nil || !isOpenAICookieRotationGroupAccount(account, settings) {
		return false, ""
	}
	if host := normalizeOpenAICookieHost(account.GetExtraString(openAICodexCookieHostExtraKey)); host != "" && openAICodexCookieHostCooldownUntil(account, host).After(now) {
		return true, "cookie_host_cooldown"
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
	if (cookiePoolPolicy(account, settings) != nil || cookieOverlapContinuousService(account, settings, now)) && settings.rotationCandidates != nil {
		entries, err := settings.rotationCandidates()
		if err != nil {
			return true, "cookie_host_library_unavailable"
		}
		host := normalizeOpenAICookieHost(account.GetExtraString(openAICodexCookieHostExtraKey))
		for _, entry := range entries {
			if normalizeOpenAICookieHost(entry.Host) == host && entry.Cookie != "" && entry.ExpiresAt.After(now) && cookieHostInScope(host, settings.HostWhitelist) {
				return true, ""
			}
		}
		return true, "cookie_host_cookie_unavailable"
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
		return true, "cookie_settings_unavailable"
	}
	hydrated, hydrateErr := s.cookieEmailAccount(ctx, account)
	if hydrateErr != nil {
		return true, "cookie_identity_unavailable"
	}
	return openAICookieSchedulingStatus(hydrated, settings, time.Now())
}

func (s *OpenAIGatewayService) openAICookieSchedulingBlockReason(ctx context.Context, account *Account) string {
	if s == nil || s.settingService == nil {
		return ""
	}
	if !isOpenAICodexTicketAccount(account) {
		return ""
	}
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return "cookie_settings_unavailable"
	}
	if p := cookiePoolPolicy(account, settings); p != nil {
		if group, ok := ctx.Value(cookieSchedulingGroupKey{}).(int64); ok && group != p.GroupID && group != settings.DegradedGroupID {
			return "cookie_pool_other_group"
		}
	}
	account, err = s.settingService.cookieEmailAccount(ctx, account)
	if err != nil {
		return "cookie_identity_unavailable"
	}
	_, reason := openAICookieSchedulingStatus(account, settings, time.Now())
	if reason != "" {
		target := cookieDegradedTarget(account, settings, time.Now())
		if group, ok := ctx.Value(cookieSchedulingGroupKey{}).(int64); ok && target > 0 && group == target {
			return ""
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
	if account == nil || settings == nil || settings.DegradedGroupID <= 0 || !account.IsSchedulable() || cookieHostMonitorOwns(account, settings) {
		return 0
	}
	if settings.emailAccount != nil {
		hydrated, err := settings.emailAccount(account)
		if err != nil {
			return 0
		}
		account = hydrated
	}
	if cookieOverlapContinuousService(account, settings, now) {
		if cookieOverlapQuotaPaused(account, now) {
			return 0
		}
		_, reason := cookieHostBindingStatus(account, settings, now)
		switch reason {
		case "cookie_host_unbound", "cookie_host_binding_invalid", "cookie_host_binding_expired", "cookie_host_cooldown", "cookie_host_cookie_unavailable":
			// An empty (but readable) library is safe here: native accounts
			// already belong to the destination and need no exhaustion proof.
			// Read failures still fail closed. Reuse the scheduling snapshot;
			// final dispatch supplies its authoritative live library snapshot.
			if settings.rotationCandidates != nil {
				if _, err := settings.rotationCandidates(); err == nil {
					return settings.DegradedGroupID
				}
			}
		}
		return 0
	}
	keepDegraded := false
	if p := cookiePoolPolicy(account, settings); p != nil {
		m, reason := cookiePoolMember(account, settings)
		keepDegraded = cookieLowHostEnabled(*p) && m.KeepDegraded
		if keepDegraded {
			// A quota pause applies to both service modes. The saved marker is
			// only continuity for an already exhausted mailbox, not proof that
			// an unavailable Cookie library is safe to use.
			until, _ := time.Parse(time.RFC3339Nano, account.GetExtraString("codex_cookie_validation_resume_at"))
			if until.After(now) {
				return 0
			}
			_, known := cookieHostExhaustionStatus(account, settings, now)
			keepDegraded = known
		}
		if m.State != "active" && m.State != "preparing" && reason != "cookie_pool_exhausted" && !keepDegraded {
			return 0
		}
	}
	_, reason := cookieHostBindingStatus(account, settings, now)
	if reason == "cookie_host_unbound" || reason == "cookie_host_binding_invalid" || reason == "cookie_host_binding_expired" || reason == "cookie_host_cooldown" {
		if keepDegraded || cookieHostsExhausted(account, settings, now) {
			return settings.DegradedGroupID
		}
	}
	return 0
}

// Empty/unknown libraries and untried Hosts are not evidence that Hosts were used up.
// Every current library Host must have a recorded cooldown for this identity.
func cookieHostsExhausted(account *Account, settings *OpenAICookieSettings, now time.Time) bool {
	exhausted, _ := cookieHostExhaustionStatus(account, settings, now)
	return exhausted
}

// known is false when the library or mailbox cannot establish either exhaustion
// or recovery. An unavailable read must not erase a persisted exhaustion state.
func cookieHostExhaustionStatus(account *Account, settings *OpenAICookieSettings, now time.Time) (exhausted, known bool) {
	if settings == nil || settings.rotationCandidates == nil {
		return false, false
	}
	if settings.emailAccount != nil {
		hydrated, err := settings.emailAccount(account)
		if err != nil {
			return false, false
		}
		account = hydrated
	}
	entries, err := settings.rotationCandidates()
	if err != nil || len(entries) == 0 {
		return false, false
	}
	found := false
	for _, entry := range entries {
		if !cookieHostInScope(entry.Host, settings.HostWhitelist) || entry.Cookie == "" || !entry.ExpiresAt.After(now) {
			continue
		}
		found = true
		if !openAICodexCookieHostCooldownUntil(account, entry.Host).After(now) {
			return false, true
		}
	}
	return found, found
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
