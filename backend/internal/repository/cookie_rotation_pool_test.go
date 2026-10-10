package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCookiePoolUpdateLocksAndCommits(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO settings").WithArgs(cookieRotationPoolKey).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings WHERE key=\\$1 FOR UPDATE").WithArgs(cookieRotationPoolKey).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{}`))
	mock.ExpectExec("UPDATE settings SET value").WithArgs(cookieRotationPoolKey, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, r.UpdateCookieRotationPool(context.Background(), func(snapshot *service.CookieRotationPoolSnapshot) error {
		snapshot.Groups = map[int64]*service.CookieRotationPoolGroup{2: {TargetCapacity: 105}}
		return nil
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCookieRotationLockAcquiredAndReleasedByRollback(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock\\(hashtextextended\\(\\$1,0\\)\\)").WithArgs("cookie-host-rotation:user@example.com").WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_xact_lock"}).AddRow(true))
	release, acquired, err := r.TryCookieRotationLock(context.Background(), "user@example.com")
	require.NoError(t, err)
	require.True(t, acquired)
	mock.ExpectRollback()
	release()
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCookieRotationLockBusyRollsBackImmediately(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock\\(hashtextextended\\(\\$1,0\\)\\)").WithArgs("cookie-host-rotation:user@example.com").WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_xact_lock"}).AddRow(false))
	mock.ExpectRollback()
	release, acquired, err := r.TryCookieRotationLock(context.Background(), "user@example.com")
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, release)
	// The busy path rolls back before returning, so no network request waits on it.
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCookieRotationLockQueryFailureRollsBack(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()
	release, acquired, err := r.TryCookieRotationLock(context.Background(), "user@example.com")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, acquired)
	require.Nil(t, release)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCookieEmailCooldownUsesIndependentMailboxRow(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	now := time.Now().UTC().Truncate(time.Second)
	mock.ExpectExec("INSERT INTO cookie_email_host_cooldowns.*ON CONFLICT.*GREATEST").WithArgs("user@example.com", "one.example", now).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, r.SaveCookieEmailCooldown(context.Background(), "user@example.com", "one.example", now))
	mock.ExpectQuery("SELECT host,cooldown_until FROM cookie_email_host_cooldowns WHERE email=\\$1").WithArgs("user@example.com").WillReturnRows(sqlmock.NewRows([]string{"host", "cooldown_until"}).AddRow("one.example", now))
	values, err := r.CookieEmailCooldowns(context.Background(), "user@example.com")
	require.NoError(t, err)
	require.Equal(t, now, values["one.example"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCookieEmailCooldownBatch(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	now := time.Now().UTC().Truncate(time.Second)
	mock.ExpectQuery("SELECT email,host,cooldown_until.*email=ANY").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"email", "host", "cooldown_until"}).AddRow("a@example.com", "one.example", now).AddRow("b@example.com", "two.example", now))
	values, err := r.CookieEmailCooldownsBatch(context.Background(), []string{"a@example.com", "b@example.com", "empty@example.com"})
	require.NoError(t, err)
	require.Equal(t, now, values["a@example.com"]["one.example"])
	require.Empty(t, values["empty@example.com"])
	require.Empty(t, values["a@example.com"]["two.example"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCookieHistoryNewAccountIncludesOldMailboxRecords(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	r := &settingRepository{client: client}
	mock.ExpectQuery("SELECT count.*account_email=NULLIF.*cookie_account_email").WithArgs(int64(999), "", "").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("WITH selected AS.*account_email=NULLIF.*cookie_account_email").WithArgs(int64(999), "", 20, 0, "").WillReturnRows(sqlmock.NewRows([]string{"payload", "account_email"}).AddRow(`{"id":"old-binding","account_id":1,"host":"one.example"}`, "user@example.com"))
	items, total, err := r.CookieValidationPage(context.Background(), 999, "", 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	require.EqualValues(t, 1, items[0].AccountID, "historical account ID remains auditable")
	require.Equal(t, "user@example.com", items[0].AccountEmail)
	require.NoError(t, mock.ExpectationsWereMet())
}
