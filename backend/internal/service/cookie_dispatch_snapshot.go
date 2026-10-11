package service

import (
	"context"
	"time"
)

// Only the selected account uses this live read. Candidate scoring never does.
// The repository fetches the selected Host and mailbox cooldowns in one query.
type CookieDispatchStateRepository interface {
	CookieDispatchState(context.Context, string, string) ([]OpenAICodexCookieLibraryEntry, map[string]time.Time, error)
}

type cookieDispatchSnapshotKey struct{}
type cookieDispatchSnapshot struct {
	owner     *SettingService
	settings  *OpenAICookieSettings
	email     string
	entries   []OpenAICodexCookieLibraryEntry
	cooldowns map[string]time.Time
}

func (s *SettingService) dispatchSnapshot(ctx context.Context) *cookieDispatchSnapshot {
	v, _ := ctx.Value(cookieDispatchSnapshotKey{}).(*cookieDispatchSnapshot)
	if v != nil && v.owner == s {
		return v
	}
	return nil
}

func (s *SettingService) withCookieDispatchSnapshot(ctx context.Context, a *Account, settings *OpenAICookieSettings) (context.Context, error) {
	v := &cookieDispatchSnapshot{owner: s, email: CookieAccountEmail(a)}
	copy := *settings
	v.settings = &copy
	host := openAICodexCookieHostFromAccount(a)
	// Degraded routing must prove exhaustion across all candidate Hosts.
	if group, ok := ctx.Value(cookieSchedulingGroupKey{}).(int64); ok && group == settings.DegradedGroupID {
		host = ""
	}
	var err error
	if repo, ok := s.settingRepo.(CookieDispatchStateRepository); ok {
		v.entries, v.cooldowns, err = repo.CookieDispatchState(ctx, v.email, host)
	} else {
		v.entries, err = s.readOpenAICodexCookieLibrary(ctx, true)
		if err == nil && v.email != "" {
			if repo, ok := s.settingRepo.(CookieEmailCooldownBatchRepository); ok {
				var values map[string]map[string]time.Time
				values, err = repo.CookieEmailCooldownsBatch(ctx, []string{v.email})
				v.cooldowns = values[v.email]
			} else if repo, ok := s.settingRepo.(CookieEmailCooldownRepository); ok {
				v.cooldowns, err = repo.CookieEmailCooldowns(ctx, v.email)
			}
		}
	}
	if err != nil {
		return ctx, err
	}
	v.entries = normalizeOpenAICodexCookieLibrary(v.entries, time.Now())
	ctx = context.WithValue(ctx, cookieDispatchSnapshotKey{}, v)
	s.attachCookiePoolReaders(ctx, v.settings)
	v.settings.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return v.entries, nil }
	return ctx, nil
}
