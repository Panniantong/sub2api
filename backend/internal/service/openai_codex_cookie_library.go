package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// OpenAICodexCookieLibraryEntry is a host-keyed, administrator-visible cookie.
// Host is the deduplication key; a newer harvest replaces the previous entry.
type OpenAICodexCookieLibraryEntry struct {
	Host       string         `json:"host"`
	Cookie     string         `json:"cookie"`
	Payload    map[string]any `json:"payload,omitempty"`
	CapturedAt time.Time      `json:"captured_at"`
	ExpiresAt  time.Time      `json:"expires_at"`
}

var openAICodexCookieLibraryMu sync.Mutex

func normalizeOpenAICodexCookieLibrary(entries []OpenAICodexCookieLibraryEntry, now time.Time) []OpenAICodexCookieLibraryEntry {
	byHost := make(map[string]OpenAICodexCookieLibraryEntry, len(entries))
	for _, entry := range entries {
		entry.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(entry.Host), "."))
		entry.Cookie = strings.TrimSpace(entry.Cookie)
		if entry.Host == "" || entry.Cookie == "" {
			continue
		}
		if entry.ExpiresAt.IsZero() {
			if exp, ok := entry.Payload["exp"].(float64); ok && exp > 0 {
				entry.ExpiresAt = time.Unix(int64(exp), 0)
			}
		}
		if !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(now) {
			continue
		}
		if old, exists := byHost[entry.Host]; !exists || openAICodexCookieIsNewer(entry, old) {
			byHost[entry.Host] = entry
		}
	}
	result := make([]OpenAICodexCookieLibraryEntry, 0, len(byHost))
	for _, entry := range byHost {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Host < result[j].Host })
	return result
}

func decodeOpenAICodexCookieLibrary(raw string) []OpenAICodexCookieLibraryEntry {
	var entries []OpenAICodexCookieLibraryEntry
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &entries) != nil {
		return nil
	}
	return entries
}

func (s *SettingService) GetOpenAICodexCookieLibrary(ctx context.Context) ([]OpenAICodexCookieLibraryEntry, error) {
	if s == nil || s.settingRepo == nil {
		return []OpenAICodexCookieLibraryEntry{}, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAICodexCookieLibrary)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	entries := normalizeOpenAICodexCookieLibrary(decodeOpenAICodexCookieLibrary(raw), time.Now())
	return entries, nil
}

func (s *SettingService) SetOpenAICodexCookieLibrary(ctx context.Context, entries []OpenAICodexCookieLibraryEntry) ([]OpenAICodexCookieLibraryEntry, error) {
	openAICodexCookieLibraryMu.Lock()
	defer openAICodexCookieLibraryMu.Unlock()
	return s.setOpenAICodexCookieLibraryLocked(ctx, entries)
}

func (s *SettingService) setOpenAICodexCookieLibraryLocked(ctx context.Context, entries []OpenAICodexCookieLibraryEntry) ([]OpenAICodexCookieLibraryEntry, error) {
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("setting repository is unavailable")
	}
	entries = normalizeOpenAICodexCookieLibrary(entries, time.Now())
	b, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	// Serialize snapshot refresh with persistence so a slow reader cannot
	// publish the old library after a successful acquisition or deletion.
	s.requestCookieMu.Lock()
	defer s.requestCookieMu.Unlock()
	if err := s.settingRepo.Set(ctx, SettingKeyOpenAICodexCookieLibrary, string(b)); err != nil {
		return nil, err
	}
	s.storeOpenAIRequestCookies(entries)
	s.cookieCandidatesMu.Lock()
	s.cookieCandidatesCache = nil
	s.cookieCandidatesMu.Unlock()
	return entries, nil
}

func (s *SettingService) UpsertOpenAICodexCookie(ctx context.Context, entry OpenAICodexCookieLibraryEntry) error {
	_, err := s.UpsertOpenAICodexCookieIfNewer(ctx, entry)
	return err
}

// All acquisition sources share the same lock and freshness rule so a delayed
// harvest cannot overwrite a newer response cookie.
func (s *SettingService) UpsertOpenAICodexCookieIfNewer(ctx context.Context, entry OpenAICodexCookieLibraryEntry) (bool, error) {
	openAICodexCookieLibraryMu.Lock()
	defer openAICodexCookieLibraryMu.Unlock()
	entries, err := s.GetOpenAICodexCookieLibrary(ctx)
	if err != nil {
		return false, err
	}
	entry.Host, entry.Payload = openAICodexCookieJWTInfo(entry.Cookie)
	entry.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(entry.Host), "."))
	if entry.Host == "" {
		return false, fmt.Errorf("cookie payload host is required")
	}
	if entry.CapturedAt.IsZero() {
		entry.CapturedAt = time.Now()
	}
	if entry.ExpiresAt.IsZero() {
		if exp, ok := entry.Payload["exp"].(float64); ok && exp > 0 {
			entry.ExpiresAt = time.Unix(int64(exp), 0)
		}
	}
	if !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(time.Now()) {
		return false, nil
	}
	replaced := false
	for i := range entries {
		if entries[i].Host == entry.Host {
			if !openAICodexCookieIsNewer(entry, entries[i]) {
				return false, nil
			}
			entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, entry)
	}
	_, err = s.setOpenAICodexCookieLibraryLocked(ctx, entries)
	return err == nil, err
}

func openAICodexCookieIsNewer(incoming, existing OpenAICodexCookieLibraryEntry) bool {
	newToken := parseOpenAICodexCookieHeader(incoming.Cookie)["__oailb"]
	oldToken := parseOpenAICodexCookieHeader(existing.Cookie)["__oailb"]
	if incoming.Cookie == existing.Cookie || (newToken != "" && newToken == oldToken) {
		return false
	}
	// Read timestamps from the token itself, not administrator-supplied payload.
	_, newPayload := openAICodexCookieJWTInfo(incoming.Cookie)
	_, oldPayload := openAICodexCookieJWTInfo(existing.Cookie)
	for _, key := range []string{"iat", "exp"} {
		n, nok := newPayload[key].(float64)
		o, ook := oldPayload[key].(float64)
		if nok && ook && n > 0 && o > 0 {
			return n > o
		}
	}
	// Legacy entries may lack token timestamps. Capture time is the final
	// fallback and must be taken on receipt, never when the worker runs.
	return incoming.CapturedAt.After(existing.CapturedAt)
}

func (s *SettingService) LookupOpenAICodexCookie(ctx context.Context, host string) (*OpenAICodexCookieLibraryEntry, error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return nil, nil
	}
	entries, err := s.GetOpenAICodexCookieLibrary(ctx)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Host == host && (entry.ExpiresAt.IsZero() || entry.ExpiresAt.After(time.Now())) {
			copy := entry
			return &copy, nil
		}
	}
	return nil, nil
}
