package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestCookieEmailCacheCoalescesAndInvalidates(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	until := time.Now().Add(time.Hour)
	mock.ExpectQuery("SELECT host,cooldown_until").WithArgs("a@example.com").WillDelayFor(30 * time.Millisecond).WillReturnRows(sqlmock.NewRows([]string{"host", "cooldown_until"}).AddRow("one", until))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := r.CookieEmailCooldowns(context.Background(), "a@example.com")
			if e != nil {
				t.Error(e)
				return
			}
			v["caller-mutation"] = until
		}()
	}
	wg.Wait()
	v, err := r.CookieEmailCooldowns(context.Background(), "a@example.com")
	require.NoError(t, err)
	require.NotContains(t, v, "caller-mutation")
	mock.ExpectExec("INSERT INTO cookie_email_host_cooldowns").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, r.SaveCookieEmailCooldown(context.Background(), "a@example.com", "two", until))
	mock.ExpectQuery("SELECT host,cooldown_until").WithArgs("a@example.com").WillReturnRows(sqlmock.NewRows([]string{"host", "cooldown_until"}).AddRow("two", until))
	v, err = r.CookieEmailCooldowns(context.Background(), "a@example.com")
	require.NoError(t, err)
	require.Contains(t, v, "two")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCookieEmailCacheDoesNotCacheErrors(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	mock.ExpectQuery("SELECT host,cooldown_until").WillReturnError(context.DeadlineExceeded)
	_, err := r.CookieEmailCooldowns(context.Background(), "a@example.com")
	require.Error(t, err)
	mock.ExpectQuery("SELECT host,cooldown_until").WillReturnRows(sqlmock.NewRows([]string{"host", "cooldown_until"}))
	_, err = r.CookieEmailCooldowns(context.Background(), "a@example.com")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
