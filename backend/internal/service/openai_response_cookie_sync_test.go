package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func responseSyncTestCookie(host string, iat, exp int64) string {
	payload, _ := json.Marshal(map[string]any{"host": host, "iat": iat, "exp": exp})
	return "__oailb=e30." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestResponseCookieFreshnessAndHostDedup(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name                           string
		oldIAT, newIAT, oldEXP, newEXP int64
		updated                        bool
	}{
		{"newer issuance", 100, 200, 3600, 1800, true},
		{"older issuance received later", 200, 100, 1800, 3600, false},
		{"same issuance", 100, 100, 1800, 3600, false},
		{"expiry fallback newer", 0, 0, 1800, 3600, true},
		{"expiry fallback older", 0, 0, 3600, 1800, false},
		{"same token", 100, 100, 3600, 3600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{}}}
			old := OpenAICodexCookieLibraryEntry{Cookie: responseSyncTestCookie("HOST.example.", tc.oldIAT, now.Unix()+tc.oldEXP), CapturedAt: now}
			require.NoError(t, s.UpsertOpenAICodexCookie(context.Background(), old))
			incoming := OpenAICodexCookieLibraryEntry{Cookie: responseSyncTestCookie("host.example", tc.newIAT, now.Unix()+tc.newEXP), CapturedAt: now.Add(time.Minute)}
			updated, err := s.UpsertOpenAICodexCookieIfNewer(context.Background(), incoming)
			require.NoError(t, err)
			require.Equal(t, tc.updated, updated)
			entries, err := s.GetOpenAICodexCookieLibrary(context.Background())
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, "host.example", entries[0].Host)
			want := old.Cookie
			if tc.updated {
				want = incoming.Cookie
			}
			require.Equal(t, want, entries[0].Cookie)
		})
	}
}

func TestResponseCookieConcurrentNewestWins(t *testing.T) {
	s := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{}}}
	now := time.Now()
	var wg sync.WaitGroup
	for i := 1; i <= 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.UpsertOpenAICodexCookieIfNewer(context.Background(), OpenAICodexCookieLibraryEntry{
				Cookie: responseSyncTestCookie(fmt.Sprintf("host%d.example", i%2), int64(i), now.Add(time.Hour).Unix()), CapturedAt: now,
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	entries, err := s.GetOpenAICodexCookieLibrary(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.EqualValues(t, 40, entries[0].Payload["iat"])
	require.EqualValues(t, 39, entries[1].Payload["iat"])
}

func TestResponseCookieSyncSettingsLogsAndFiltering(t *testing.T) {
	for _, tc := range []struct {
		name, config, cookie, stage string
		status                      int
		saved                       bool
	}{
		{"enabled independently", `{"response_cookie_sync_enabled":true}`, "valid", "updated", 200, true},
		{"disabled", `{}`, "valid", "", 200, false},
		{"error response", `{"response_cookie_sync_enabled":true}`, "valid", "ignored_status", 500, false},
		{"invalid cookie", `{"response_cookie_sync_enabled":true}`, "other=value", "ignored_invalid", 200, false},
		{"deleted cookie", `{"response_cookie_sync_enabled":true}`, "valid", "ignored_invalid", 200, false},
		{"expired token", `{"response_cookie_sync_enabled":true}`, "expired", "ignored_invalid", 200, false},
		{"host whitelist", `{"response_cookie_sync_enabled":true,"host_whitelist":["other.example"]}`, "valid", "ignored_host", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: tc.config}}}
			gateway := &OpenAIGatewayService{settingService: s}
			cookie := tc.cookie
			if cookie == "valid" {
				cookie = responseSyncTestCookie("host.example", 100, time.Now().Add(time.Hour).Unix())
			}
			if cookie == "expired" {
				cookie = responseSyncTestCookie("host.example", 100, time.Now().Add(-time.Hour).Unix())
			}
			if tc.name == "deleted cookie" {
				cookie += "; Max-Age=0"
			}
			gateway.processOpenAIResponseCookie(responseCookieEvent{accountID: 7, accountName: "test", statusCode: tc.status, receivedAt: time.Now(), cookies: []string{cookie}})
			entries, err := s.GetOpenAICodexCookieLibrary(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.saved, len(entries) == 1)
			logs, err := s.GetOpenAICookieLogs(context.Background())
			require.NoError(t, err)
			if tc.stage == "" {
				require.Empty(t, logs)
				return
			}
			require.Len(t, logs, 1)
			require.Equal(t, tc.stage, logs[0].Stage)
			require.Equal(t, "response_sync", logs[0].Kind)
			require.Equal(t, "response_sync", logs[0].Task)
			require.EqualValues(t, 7, logs[0].AccountID)
			if tc.saved {
				gateway.processOpenAIResponseCookie(responseCookieEvent{statusCode: 200, receivedAt: time.Now(), cookies: []string{cookie}})
				logs, err = s.GetOpenAICookieLogs(context.Background())
				require.NoError(t, err)
				require.Equal(t, "ignored_older", logs[0].Stage)
			}
		})
	}
}

type responseSyncBlockingRepo struct {
	SettingRepository
	mu      sync.Mutex
	values  map[string]string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *responseSyncBlockingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make(map[string]string)
	for _, key := range keys {
		values[key] = r.values[key]
	}
	return values, nil
}

func (r *responseSyncBlockingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if key == cookieSettingsKey {
		r.once.Do(func() { close(r.entered) })
		select {
		case <-r.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *responseSyncBlockingRepo) Set(ctx context.Context, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func TestResponseCookieQueueNeverWaitsForStorageAndCopiesHeaders(t *testing.T) {
	repo := &responseSyncBlockingRepo{values: map[string]string{cookieSettingsKey: `{"response_cookie_sync_enabled":true}`}, entered: make(chan struct{}), release: make(chan struct{})}
	s := &SettingService{settingRepo: repo}
	gateway := &OpenAIGatewayService{settingService: s}
	cookie := responseSyncTestCookie("host.example", 100, time.Now().Add(time.Hour).Unix())
	response := &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": {cookie}}}
	account := &Account{ID: 1, Name: "before", Platform: PlatformOpenAI}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(repo.release) }) }
	t.Cleanup(func() {
		release()
		require.Eventually(t, func() bool { return !gateway.responseCookieSync.running.Load() }, 10*time.Second, time.Millisecond)
	})
	gateway.enqueueOpenAIResponseCookie(account, response)
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	// Mutating the response/account after submission cannot change the snapshot.
	response.Header["Set-Cookie"][0] = "downstream=private"
	account.Name = "after"
	done := make(chan struct{})
	go func() {
		for i := 0; i < responseCookieQueueSize+10; i++ {
			gateway.enqueueOpenAIResponseCookie(account, response)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("queue blocked the request")
	}
	require.Greater(t, gateway.responseCookieSync.dropped.Load(), uint64(0))
	release()
	require.Eventually(t, func() bool { return !gateway.responseCookieSync.running.Load() }, 10*time.Second, time.Millisecond)
	entries, err := s.GetOpenAICodexCookieLibrary(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, cookie, entries[0].Cookie)
	logs, err := s.GetOpenAICookieLogs(context.Background())
	require.NoError(t, err)
	foundUpdate, foundDrop := false, false
	for _, item := range logs {
		if item.Stage == "updated" {
			foundUpdate = true
			require.Equal(t, "before", item.AccountName)
		}
		if item.Stage == "queue_full" {
			foundDrop = true
		}
	}
	require.True(t, foundUpdate)
	require.True(t, foundDrop)
}

func TestResponseCookieConfigurationRoundTrip(t *testing.T) {
	s := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{}}}
	settings, err := s.GetOpenAICookieSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.ResponseCookieSyncEnabled)
	settings.ResponseCookieSyncEnabled = true
	require.NoError(t, s.SetOpenAICookieSettings(context.Background(), settings))
	loaded, err := (&SettingService{settingRepo: s.settingRepo}).GetOpenAICookieSettings(context.Background())
	require.NoError(t, err)
	require.True(t, loaded.ResponseCookieSyncEnabled)
}

func TestResponseCookieDeletionAndMaxAgePrecedence(t *testing.T) {
	cookie := responseSyncTestCookie("host.example", 100, time.Now().Add(time.Hour).Unix())
	for _, tc := range []struct {
		name    string
		headers []string
		want    string
	}{
		{"later deletion", []string{cookie, "__oailb=; Max-Age=0"}, ""},
		{"later replacement", []string{"__oailb=; Max-Age=0", cookie}, cookie},
		{"max age overrides past expiry", []string{cookie + "; Max-Age=3600; Expires=Thu, 01 Jan 1970 00:00:00 GMT"}, cookie},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAICodexResponseCookieHeader(&http.Response{Header: http.Header{"Set-Cookie": tc.headers}}))
		})
	}
}

func TestResponseCookieSettingsDoNotUseForwardingLock(t *testing.T) {
	s := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: `{"response_cookie_sync_enabled":true}`}}}
	gateway := &OpenAIGatewayService{settingService: s}
	cookieSettingsMu.Lock()
	done := make(chan OpenAICookieSettings, 1)
	go func() { settings, _ := gateway.responseCookieSettings(context.Background()); done <- settings }()
	select {
	case settings := <-done:
		cookieSettingsMu.Unlock()
		require.True(t, settings.ResponseCookieSyncEnabled)
	case <-time.After(time.Second):
		cookieSettingsMu.Unlock()
		<-done
		t.Fatal("background synchronization shares the forwarding settings lock")
	}
}
