package service

import (
	"context"
	"strings"
	"time"
)

// Only inference uses this short-lived index. Administrative and acquisition
// reads still consult storage. Every local library write publishes a new index;
// the TTL bounds visibility of changes made by another process to one second.
const openAIRequestCookieCacheTTL = time.Second

type openAIRequestCookie struct {
	cookie    string
	expiresAt time.Time
}

type openAIRequestCookieSnapshot struct {
	byHost map[string]openAIRequestCookie
	until  time.Time
}

func (s *SettingService) storeOpenAIRequestCookies(entries []OpenAICodexCookieLibraryEntry) {
	snapshot := &openAIRequestCookieSnapshot{
		byHost: make(map[string]openAIRequestCookie, len(entries)),
		until:  time.Now().Add(openAIRequestCookieCacheTTL),
	}
	for _, entry := range entries {
		snapshot.byHost[entry.Host] = openAIRequestCookie{entry.Cookie, entry.ExpiresAt}
	}
	s.requestCookieCache.Store(snapshot)
}

func (s *SettingService) lookupOpenAIRequestCookie(ctx context.Context, host string) (string, error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if s == nil || s.settingRepo == nil || host == "" {
		return "", nil
	}
	snapshot := s.requestCookieCache.Load()
	if snapshot == nil || !time.Now().Before(snapshot.until) {
		// Coalesce concurrent cold reads; cache hits never take this lock or
		// wait for background acquisition writes.
		s.requestCookieMu.Lock()
		defer s.requestCookieMu.Unlock()
		snapshot = s.requestCookieCache.Load()
		if snapshot == nil || !time.Now().Before(snapshot.until) {
			entries, err := s.GetOpenAICodexCookieLibrary(ctx)
			if err != nil {
				return "", err
			}
			s.storeOpenAIRequestCookies(entries)
			snapshot = s.requestCookieCache.Load()
		}
	}
	entry := snapshot.byHost[host]
	if !entry.expiresAt.IsZero() && !time.Now().Before(entry.expiresAt) {
		return "", nil
	}
	return entry.cookie, nil
}
