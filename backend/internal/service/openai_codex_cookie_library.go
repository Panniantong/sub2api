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
		byHost[entry.Host] = entry
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
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("setting repository is unavailable")
	}
	entries = normalizeOpenAICodexCookieLibrary(entries, time.Now())
	b, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOpenAICodexCookieLibrary, string(b)); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *SettingService) UpsertOpenAICodexCookie(ctx context.Context, entry OpenAICodexCookieLibraryEntry) error {
	openAICodexCookieLibraryMu.Lock()
	defer openAICodexCookieLibraryMu.Unlock()
	entries, err := s.GetOpenAICodexCookieLibrary(ctx)
	if err != nil {
		return err
	}
	entry.Host, _ = openAICodexCookieJWTInfo(entry.Cookie)
	entry.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(entry.Host), "."))
	if entry.Host == "" {
		return fmt.Errorf("cookie payload host is required")
	}
	if entry.CapturedAt.IsZero() {
		entry.CapturedAt = time.Now()
	}
	if entry.ExpiresAt.IsZero() {
		if exp, ok := entry.Payload["exp"].(float64); ok && exp > 0 {
			entry.ExpiresAt = time.Unix(int64(exp), 0)
		}
	}
	replaced := false
	for i := range entries {
		if entries[i].Host == entry.Host {
			entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, entry)
	}
	_, err = s.SetOpenAICodexCookieLibrary(ctx, entries)
	return err
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
