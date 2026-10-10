package service

import (
	"context"
	"time"
)

// Independent of account IDs: deleting/reimporting an account must not reset Host history.
type CookieEmailCooldownRepository interface {
	CookieEmailCooldowns(context.Context, string) (map[string]time.Time, error)
	SaveCookieEmailCooldown(context.Context, string, string, time.Time) error
}

func CookieAccountEmail(a *Account) string { return cookieMonitorEmail(a) }

func (s *SettingService) cookieEmailAccount(ctx context.Context, a *Account) (*Account, error) {
	if a == nil || s == nil {
		return a, nil
	}
	email := CookieAccountEmail(a)
	if snapshot := s.cookieAdminSnapshot(ctx); snapshot != nil {
		if email == "" {
			return a, nil
		}
		if snapshot.cooldownErr != nil {
			return nil, snapshot.cooldownErr
		}
		return mergeCookieEmailCooldowns(a, snapshot.cooldowns[email]), nil
	}
	repo, ok := s.settingRepo.(CookieEmailCooldownRepository)
	if !ok || email == "" {
		return a, nil
	}
	values, err := repo.CookieEmailCooldowns(ctx, email)
	if err != nil {
		return nil, err
	}
	return mergeCookieEmailCooldowns(a, values), nil
}

func mergeCookieEmailCooldowns(a *Account, values map[string]time.Time) *Account {
	copy := *a
	copy.Extra = make(map[string]any, len(a.Extra)+1)
	for k, v := range a.Extra {
		copy.Extra[k] = v
	}
	merged := openAICodexCookieHostCooldowns(a)
	if merged == nil {
		merged = map[string]time.Time{}
	}
	for host, until := range values {
		if until.After(merged[host]) {
			merged[host] = until
		}
	}
	cooldowns := map[string]string{}
	for host, until := range merged {
		cooldowns[host] = until.Format(time.RFC3339Nano)
	}
	copy.Extra[openAICodexCookieCooldownsExtraKey] = cooldowns
	return &copy
}
