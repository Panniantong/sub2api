package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type requestCookieRepo struct {
	SettingRepository
	mu          sync.Mutex
	raw         string
	reads       atomic.Int64
	readErr     error
	writeErr    error
	readStarted chan struct{}
	releaseRead chan struct{}
}

func (r *requestCookieRepo) GetValue(ctx context.Context, key string) (string, error) {
	r.reads.Add(1)
	r.mu.Lock()
	raw, err := r.raw, r.readErr
	started, release := r.readStarted, r.releaseRead
	r.readStarted, r.releaseRead = nil, nil
	r.mu.Unlock()
	if started != nil {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return raw, err
}

func (r *requestCookieRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writeErr != nil {
		return r.writeErr
	}
	r.raw = value
	return nil
}

func TestOpenAIRequestCookieCache_ReuseUpdateDeleteAndFailedWrite(t *testing.T) {
	ctx := context.Background()
	repo := &requestCookieRepo{raw: `[{"host":"one.example","cookie":"old"},{"host":"two.example","cookie":"other"}]`}
	s := &SettingService{settingRepo: repo}
	for i := 0; i < 10; i++ {
		value, err := s.lookupOpenAIRequestCookie(ctx, "ONE.EXAMPLE.")
		require.NoError(t, err)
		require.Equal(t, "old", value)
		value, err = s.lookupOpenAIRequestCookie(ctx, "two.example")
		require.NoError(t, err)
		require.Equal(t, "other", value)
	}
	require.EqualValues(t, 1, repo.reads.Load(), "a request must not repeatedly fetch/decode the whole library")
	entries := []OpenAICodexCookieLibraryEntry{{Host: "one.example", Cookie: "new"}}
	_, err := s.SetOpenAICodexCookieLibrary(ctx, entries)
	require.NoError(t, err)
	entries[0].Cookie = "caller mutation"
	value, err := s.lookupOpenAIRequestCookie(ctx, "one.example")
	require.NoError(t, err)
	require.Equal(t, "new", value)
	value, err = s.lookupOpenAIRequestCookie(ctx, "two.example")
	require.NoError(t, err)
	require.Empty(t, value, "deleted Host must disappear immediately")
	repo.writeErr = errors.New("storage failed")
	_, err = s.SetOpenAICodexCookieLibrary(ctx, nil)
	require.Error(t, err)
	value, err = s.lookupOpenAIRequestCookie(ctx, "one.example")
	require.NoError(t, err)
	require.Equal(t, "new", value, "failed persistence must not replace the snapshot")
}

func TestOpenAIRequestCookieCache_ExpiryAndExternalRefresh(t *testing.T) {
	ctx := context.Background()
	repo := &requestCookieRepo{raw: `[{"host":"one.example","cookie":"external"}]`}
	s := &SettingService{settingRepo: repo}
	s.requestCookieCache.Store(&openAIRequestCookieSnapshot{
		until:  time.Now().Add(time.Hour),
		byHost: map[string]openAIRequestCookie{"one.example": {cookie: "expired", expiresAt: time.Now().Add(-time.Second)}},
	})
	value, err := s.lookupOpenAIRequestCookie(ctx, "one.example")
	require.NoError(t, err)
	require.Empty(t, value, "snapshot TTL must never extend Cookie expiration")
	s.requestCookieCache.Store(&openAIRequestCookieSnapshot{until: time.Now().Add(-time.Second)})
	value, err = s.lookupOpenAIRequestCookie(ctx, "one.example")
	require.NoError(t, err)
	require.Equal(t, "external", value)
	s.requestCookieCache.Store(&openAIRequestCookieSnapshot{until: time.Now().Add(-time.Second)})
	repo.readErr = errors.New("storage failed")
	value, err = s.lookupOpenAIRequestCookie(ctx, "one.example")
	require.Error(t, err)
	require.Empty(t, value, "expired snapshot must not hide storage failures")
}

func TestOpenAIRequestCookieCache_ConcurrentColdReadsAndWrite(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	repo := &requestCookieRepo{raw: `[{"host":"one.example","cookie":"old"}]`, readStarted: started, releaseRead: release}
	s := &SettingService{settingRepo: repo}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.lookupOpenAIRequestCookie(ctx, "one.example")
		}()
	}
	<-started
	writeDone := make(chan error, 1)
	go func() {
		_, err := s.SetOpenAICodexCookieLibrary(ctx, []OpenAICodexCookieLibraryEntry{{Host: "one.example", Cookie: "new"}})
		writeDone <- err
	}()
	close(release)
	wg.Wait()
	require.NoError(t, <-writeDone)
	value, err := s.lookupOpenAIRequestCookie(ctx, "one.example")
	require.NoError(t, err)
	require.Equal(t, "new", value, "cold read must not overwrite a newly persisted cookie")
	require.EqualValues(t, 1, repo.reads.Load())
}

func BenchmarkOpenAIRequestCookieLookup(b *testing.B) {
	entries := make([]OpenAICodexCookieLibraryEntry, 100)
	for i := range entries {
		entries[i] = OpenAICodexCookieLibraryEntry{Host: fmt.Sprintf("host-%d.example", i), Cookie: "cookie", ExpiresAt: time.Now().Add(time.Hour)}
	}
	raw, _ := json.Marshal(entries)
	repo := &requestCookieRepo{raw: string(raw)}
	s := &SettingService{settingRepo: repo}
	ctx := context.Background()
	b.Run("whole_library", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = s.LookupOpenAICodexCookie(ctx, "host-50.example")
		}
	})
	b.Run("host_snapshot", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = s.lookupOpenAIRequestCookie(ctx, "host-50.example")
		}
	})
}
