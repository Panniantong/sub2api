package service

import (
	"context"
	"sync"
	"time"
)

// Display snapshots live for one admin request only. They must never be used
// for validation, binding writes or final gateway admission.
type cookieAdminSnapshotKey struct{}
type cookieAdminSnapshot struct {
	owner       *SettingService
	settings    *OpenAICookieSettings
	settingsErr error
	libraryOnce sync.Once
	library     []OpenAICodexCookieLibraryEntry
	libraryErr  error
	cooldowns   map[string]map[string]time.Time
	cooldownErr error
}

type CookieEmailCooldownBatchRepository interface {
	CookieEmailCooldownsBatch(context.Context, []string) (map[string]map[string]time.Time, error)
}

func (s *SettingService) cookieAdminSnapshot(ctx context.Context) *cookieAdminSnapshot {
	v, _ := ctx.Value(cookieAdminSnapshotKey{}).(*cookieAdminSnapshot)
	if v != nil && v.owner == s {
		return v
	}
	return nil
}

func (s *SettingService) cookieAdminSettings(ctx context.Context) (*OpenAICookieSettings, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !cookieSettingsMu.TryLock() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
				if cookieSettingsMu.TryLock() {
					defer cookieSettingsMu.Unlock()
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					return s.getOpenAICookieSettingsLocked(ctx)
				}
			}
		}
	}
	defer cookieSettingsMu.Unlock()
	return s.getOpenAICookieSettingsLocked(ctx)
}

// WithCookieAdminSnapshot batches mailbox reads and shares settings, pool and
// library reads across all rows, including failures (no per-row retry storm).
func (s *SettingService) WithCookieAdminSnapshot(ctx context.Context, accounts []Account) context.Context {
	if s == nil || s.cookieAdminSnapshot(ctx) != nil {
		return ctx
	}
	emails := []string{}
	seen := map[string]bool{}
	eligible := false
	for i := range accounts {
		if !isOpenAICodexTicketAccount(&accounts[i]) {
			continue
		}
		eligible = true
		if email := CookieAccountEmail(&accounts[i]); email != "" && !seen[email] {
			seen[email] = true
			emails = append(emails, email)
		}
	}
	if !eligible {
		return ctx
	}
	v := &cookieAdminSnapshot{owner: s, cooldowns: map[string]map[string]time.Time{}}
	v.settings, v.settingsErr = s.cookieAdminSettings(ctx)
	if v.settingsErr == nil && len(emails) > 0 {
		if repo, ok := s.settingRepo.(CookieEmailCooldownBatchRepository); ok {
			v.cooldowns, v.cooldownErr = repo.CookieEmailCooldownsBatch(ctx, emails)
		} else if repo, ok := s.settingRepo.(CookieEmailCooldownRepository); ok {
			for _, email := range emails {
				v.cooldowns[email], v.cooldownErr = repo.CookieEmailCooldowns(ctx, email)
				if v.cooldownErr != nil {
					break
				}
			}
		}
	}
	ctx = context.WithValue(ctx, cookieAdminSnapshotKey{}, v)
	if v.settings != nil {
		s.attachCookiePoolReaders(ctx, v.settings)
		v.settings.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return s.GetOpenAICodexCookieLibrary(ctx) }
	}
	return ctx
}
