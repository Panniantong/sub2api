package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCookieDispatchStateBypassesCandidateCache(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client, cooldownCache: map[string]cookieCooldownCacheEntry{"a@example.com": {expires: time.Now().Add(time.Minute)}}}
	until := time.Now().Add(time.Hour).UTC()
	raw, err := json.Marshal(map[string]time.Time{"one.example": until})
	require.NoError(t, err)
	mock.ExpectQuery("SELECT.*COALESCE").WithArgs(service.SettingKeyOpenAICodexCookieLibrary, "a@example.com", "one.example").WillReturnRows(sqlmock.NewRows([]string{"library", "cooldowns"}).AddRow(`[{"host":"one.example","cookie":"__oailb=test","captured_at":null,"expires_at":null,"payload":{"exp":1900000000}}]`, string(raw)))
	entries, cooldowns, err := r.CookieDispatchState(context.Background(), "a@example.com", "one.example")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "__oailb=test", entries[0].Cookie)
	require.Equal(t, float64(1900000000), entries[0].Payload["exp"])
	require.True(t, until.Equal(cooldowns["one.example"]))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCookieCandidateBatchCoalescesAndWarmsAllEmails(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	until := time.Now().Add(time.Hour)
	mock.ExpectQuery("SELECT email,host,cooldown_until").WithArgs(`{"a@example.com","b@example.com"}`).WillDelayFor(40 * time.Millisecond).WillReturnRows(sqlmock.NewRows([]string{"email", "host", "cooldown_until"}).AddRow("a@example.com", "one", until))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.PrimeCookieEmailCooldowns(context.Background(), []string{"b@example.com", "a@example.com", "a@example.com"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for _, email := range []string{"a@example.com", "b@example.com"} {
		_, err := r.CookieEmailCooldowns(context.Background(), email)
		require.NoError(t, err, "candidate lookups must hit the warmed batch cache")
	}
	mock.ExpectExec("INSERT INTO cookie_email_host_cooldowns").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, r.SaveCookieEmailCooldown(context.Background(), "b@example.com", "two", until))
	mock.ExpectQuery("SELECT email,host,cooldown_until").WithArgs(`{"b@example.com"}`).WillReturnRows(sqlmock.NewRows([]string{"email", "host", "cooldown_until"}).AddRow("b@example.com", "two", until))
	require.NoError(t, r.PrimeCookieEmailCooldowns(context.Background(), []string{"a@example.com", "b@example.com"}))
	v, err := r.CookieEmailCooldowns(context.Background(), "b@example.com")
	require.NoError(t, err)
	require.Contains(t, v, "two")
	require.NoError(t, mock.ExpectationsWereMet())
}
