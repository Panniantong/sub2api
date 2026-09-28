package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookieDashboardCountsLiveTasksAndDegradation(t *testing.T) {
	a := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
	b := cookieGuardTestAccount("healthy.example", time.Now().Add(time.Hour))
	b.ID = a.ID + 1
	b.Extra[openAICodexCookieRotationStatusExtraKey] = "running" // stale persisted marker
	c := cookieGuardTestAccount("", time.Time{})
	c.ID = b.ID + 1
	c.GroupIDs = []int64{4}
	s := &OpenAIGatewayService{settingService: cookieDegradedTestSettings(), accountRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{a, b, c}}}}
	s.cookieHarvestRuntime.accounts = map[int64]bool{a.ID: true}
	s.cookieHarvestRuntime.jobs = map[int64]string{a.ID: "explore"}
	s.openaiCookieRotationRunning.Store(a.ID, true)
	result, err := s.CookieDashboard(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Harvest["total"])
	require.Equal(t, 1, result.Harvest["explore"])
	require.Equal(t, 2, result.RotationAccounts)
	require.Equal(t, 1, result.RotationRunning, "stale persisted running state must not count")
	require.Equal(t, 1, result.DegradedAccounts)
	s.openaiCookieRotationRunning.Delete(a.ID)
	result, err = s.CookieDashboard(context.Background())
	require.NoError(t, err)
	require.Zero(t, result.RotationRunning)
}

func TestCookieDashboardCountsOnlyHealthyBindings(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	settings := &OpenAICookieSettings{HostMonitor: &CookieHostMonitorConfig{Enabled: true, AccountID: 9}}
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	svc := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: string(raw)}}}
	_, err = svc.SetOpenAICodexCookieLibrary(ctx, []OpenAICodexCookieLibraryEntry{
		{Host: "healthy.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)},
		{Host: "expired.example", Cookie: "cookie", ExpiresAt: now.Add(-time.Minute)},
	})
	require.NoError(t, err)
	accounts := make([]Account, 9)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("healthy.example", now.Add(time.Hour))
		accounts[i].ID = int64(i + 1)
	}
	accounts[1].Extra["codex_cookie_host_binding_expires_at"] = now.Add(-time.Minute).Format(time.RFC3339Nano)
	accounts[2].Extra[openAICodexCookieHostExtraKey] = "missing.example"
	accounts[3].Extra[openAICodexCookieHostExtraKey] = "expired.example"
	accounts[4].Schedulable = false
	accounts[5].Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"healthy.example": now.Add(time.Minute).Format(time.RFC3339Nano)}
	accounts[6].Extra[openAICodexCookieHostExtraKey] = ""
	accounts[7].RateLimitResetAt = ptrTime(now.Add(time.Minute))
	accounts = append(accounts, accounts[0]) // Repeated group membership must not double count.
	gateway := &OpenAIGatewayService{settingService: svc, accountRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}}
	result, err := gateway.CookieDashboard(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.HealthyBoundAccounts, "healthy bindings must count even without a degraded group configured")

	// A failed library read must not publish a misleading healthy count of zero.
	svc.settingRepo.(*cookieTestRepo).values[SettingKeyOpenAICodexCookieLibrary] = "invalid json"
	svc.cookieCandidatesCache = nil
	_, err = gateway.CookieDashboard(ctx)
	require.Error(t, err)
}
