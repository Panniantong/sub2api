package service

import (
	"context"
	"log/slog"
	"time"
)

const openAICookieDegradedSinceExtraKey = "codex_cookie_degraded_since"

// The persisted timestamp belongs to one continuous degradation episode.
// A known expired binding also supplies an accurate start for older accounts.
func cookieDegradedSince(account *Account, now time.Time) *time.Time {
	if account == nil {
		return nil
	}
	var since *time.Time
	if started, err := time.Parse(time.RFC3339Nano, account.GetExtraString(openAICookieDegradedSinceExtraKey)); err == nil && !started.After(now) {
		since = &started
	}
	if openAICodexCookieHostFromAccount(account) != "" {
		if expired, err := time.Parse(time.RFC3339Nano, account.GetExtraString("codex_cookie_host_binding_expires_at")); err == nil && !expired.After(now) {
			// A newer binding deadline indicates recovery since the saved episode.
			if since == nil || expired.After(*since) {
				return &expired
			}
		}
	}
	return since
}

// Called under the account's rotation lock, using its freshly loaded state.
// This is display metadata only; it never changes scheduling or group scope.
func (s *OpenAIGatewayService) syncCookieDegradedSince(ctx context.Context, account *Account, settings *OpenAICookieSettings, now time.Time) {
	if account == nil || settings == nil {
		return
	}
	copy := *settings
	_, reason := openAICookieSchedulingStatus(account, settings, now)
	if reason == "cookie_host_unbound" && settings.DegradedGroupID > 0 {
		if settings.rotationCandidates == nil {
			return
		}
		entries, err := settings.rotationCandidates()
		if err != nil {
			return // A storage failure must not reset an ongoing episode.
		}
		copy.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return entries, nil }
	}
	var value any
	if cookieDegradedTarget(account, &copy, now) > 0 {
		started := cookieDegradedSince(account, now)
		if started == nil {
			started = &now
		}
		value = started.Format(time.RFC3339Nano)
	}
	old := account.GetExtraString(openAICookieDegradedSinceExtraKey)
	if (value == nil && old == "") || value == old {
		return
	}
	if s.accountRepo != nil {
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{openAICookieDegradedSinceExtraKey: value}); err != nil {
			slog.Warn("cookie degradation time persist failed", "account_id", account.ID, "error", err)
			return
		}
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	if value == nil {
		delete(account.Extra, openAICookieDegradedSinceExtraKey)
	} else {
		account.Extra[openAICookieDegradedSinceExtraKey] = value
	}
}
