package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cookieDashboardDemandCache struct {
	schedulerTestConcurrencyCache
	fail bool
}

func (*cookieDashboardDemandCache) TouchCookieGroupDemand(context.Context, int64, string) error {
	return nil
}
func (*cookieDashboardDemandCache) ReleaseCookieGroupDemand(context.Context, int64, string) error {
	return nil
}
func (c *cookieDashboardDemandCache) ReadCookieGroupDemand(_ context.Context, group int64, _ int) (CookieGroupDemand, error) {
	if c.fail {
		return CookieGroupDemand{}, errors.New("offline")
	}
	if group == 3 {
		return CookieGroupDemand{Current: 21, Peak: 22}, nil
	}
	return CookieGroupDemand{Current: 2, Peak: 3}, nil
}

func TestCookieDashboardPoolCapacityAndDemandIsolation(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	p.DemandMode = "dynamic"
	g := &CookieRotationPoolGroup{TargetCapacity: 5, UpdatedAt: now, Members: map[int64]CookieRotationPoolMember{
		1: {State: "active"}, 2: {State: "preparing"}, 3: {State: "exhausted"}, 4: {State: "active"}, 99: {State: "active"},
	}}
	cfg := poolTestConfig(p, g)
	cfg.HostWhitelist = []string{"one.example"}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &cookiePoolRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: string(raw), SettingKeyOpenAICodexCookieLibrary: `[{"host":"one.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"},{"host":"two.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"}]`}}, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
	accounts := make([]Account, 5)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("one.example", now.Add(time.Hour))
		accounts[i].ID, accounts[i].Concurrency = int64(i+1), 35
	}
	accounts[2].Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"one.example": now.Add(time.Hour).Format(time.RFC3339Nano)}
	accounts[3].Extra[openAICodexCookieHostExtraKey] = "two.example"
	cache := &cookieDashboardDemandCache{}
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: repo}, accountRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}, concurrencyService: NewConcurrencyService(cache)}
	dashboard, err := svc.CookieDashboard(context.Background())
	require.NoError(t, err)
	require.Len(t, dashboard.PoolGroups, 1)
	row := dashboard.PoolGroups[0]
	require.Equal(t, 2, *row.CurrentRequests)
	require.Equal(t, 3, *row.WindowDemand)
	require.Equal(t, 21, *dashboard.DegradedCurrentRequests)
	require.Equal(t, 5, row.TargetCapacity)
	require.Equal(t, 35, row.ReadyCapacity, "preparing, deleted and disallowed Host accounts must not count as ready")
	require.Equal(t, 1, row.Active)
	require.Equal(t, 1, row.Preparing)
	require.Equal(t, 1, row.Exhausted)
	require.Equal(t, 1, row.Degraded)
	require.Equal(t, 1, row.Unavailable)
	require.Equal(t, 1, row.Standby)
	svc.openaiCookieRotationRunning.Store(int64(2), cookiePoolValidationLive{Host: "one.example", Stage: "validation_started", UpdatedAt: now})
	detail, err := svc.CookieDashboard(context.Background(), 2)
	require.NoError(t, err)
	require.Len(t, detail.PoolGroups[0].Accounts, 5, "deleted snapshot members excluded")
	for _, a := range detail.PoolGroups[0].Accounts {
		if a.ID == 2 {
			require.Equal(t, "preparing", a.State)
			require.Equal(t, "one.example", a.ValidationHost)
			require.Equal(t, "validation_started", a.ValidationStage)
			require.NotNil(t, a.PreparingDeadline)
		}
		if a.ID == 3 {
			require.Zero(t, a.AvailableHosts)
			require.Equal(t, "cooldown", a.Hosts[0].State)
			require.Equal(t, "out_of_scope", a.Hosts[1].State)
		}
	}
	encoded, err := json.Marshal(detail.PoolGroups[0].Accounts)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"cookie":`)
	require.NotContains(t, string(encoded), `"credentials":`)
	without, err := svc.CookieDashboard(context.Background())
	require.NoError(t, err)
	require.Empty(t, without.PoolGroups[0].Accounts)
	cache.fail = true
	dashboard, err = svc.CookieDashboard(context.Background())
	require.NoError(t, err)
	require.Nil(t, dashboard.PoolGroups[0].CurrentRequests)
	require.Nil(t, dashboard.DegradedCurrentRequests)
}

func TestCookieDashboardWaitingReasonsUseLongestSafetyDeadline(t *testing.T) {
	now := time.Now()
	a := cookieGuardTestAccount("", time.Time{})
	p := poolTestPolicy()
	p.ShortageRetrySeconds = 10
	row := &CookiePoolDashboardGroup{Policy: p, Enabled: true, shortage: true}
	m := CookieRotationPoolMember{State: "waiting", RetryAt: now.Add(time.Minute)}
	reason, until := cookiePoolAccountWait(&a, m, row, now)
	require.Equal(t, "准备失败后等待重试", reason)
	require.Equal(t, now.Add(10*time.Second), *until)
	a.RateLimitResetAt = ptrTime(now.Add(24 * time.Hour))
	reason, until = cookiePoolAccountWait(&a, m, row, now)
	require.Equal(t, "额度 / 限流暂停", reason)
	require.Equal(t, *a.RateLimitResetAt, *until)
}

func TestCookieDashboardCountsLiveTasksAndDegradation(t *testing.T) {
	a := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
	exhaustCookieTestCandidates(&a)
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
