package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookieLivePoolsFollowDatabaseAcrossExpiryAndRecovery(t *testing.T) {
	ctx := context.Background()
	stale := cookieGuardTestAccount("old.example", time.Now().Add(time.Hour))
	fresh := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
	repo := &schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{fresh}}}
	svc := &OpenAIGatewayService{settingService: cookieDegradedTestSettings(), accountRepo: repo,
		schedulerSnapshot: &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{snapshotAccounts: []*Account{&stale}, accountsByID: map[int64]*Account{fresh.ID: &stale}}}}
	source, target := int64(2), int64(3)
	accounts, err := svc.listSchedulableAccounts(ctx, &source, PlatformOpenAI)
	require.NoError(t, err)
	require.Empty(t, accounts)
	accounts, err = svc.listSchedulableAccounts(ctx, &target, PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.True(t, accounts[0].openaiCookieDegraded)
	repo.accounts[0] = cookieGuardTestAccount("new.example", time.Now().Add(time.Minute))
	accounts, err = svc.listSchedulableAccounts(ctx, &source, PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "new.example", openAICodexCookieHostFromAccount(&accounts[0]))
	accounts, err = svc.listSchedulableAccounts(ctx, &target, PlatformOpenAI)
	require.NoError(t, err)
	require.Empty(t, accounts)
	account, err := svc.getSchedulableAccount(withCookieSchedulingGroup(ctx, &source), fresh.ID)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, "new.example", openAICodexCookieHostFromAccount(account))
}

func TestCookieValidationUsageLimitPausesUntilReset(t *testing.T) {
	now := time.Now()
	until, limited := cookieValidationUsageResume(429, `{"error":{"type":"usage_limit_reached","resets_in_seconds":3600}}`, now)
	require.True(t, limited)
	require.Equal(t, now.Add(time.Hour), until)
	a := cookieGuardTestAccount("old.example", now.Add(-time.Minute))
	a.Extra["codex_cookie_validation_resume_at"] = until.Format(time.RFC3339Nano)
	require.False(t, openAICookieRotationDue(&a, now))
	require.True(t, openAICookieRotationDue(&a, until.Add(time.Second)))
	_, limited = cookieValidationUsageResume(429, `{"error":{"type":"rate_limit_exceeded"}}`, now)
	require.False(t, limited)
	_, limited = cookieValidationUsageResume(400, `{"error":{"type":"usage_limit_reached"}}`, now)
	require.False(t, limited)
}
