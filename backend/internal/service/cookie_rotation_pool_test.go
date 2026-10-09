package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cookiePoolRepoStub struct {
	*cookieTestRepo
	snapshot *CookieRotationPoolSnapshot
}

func (r *cookiePoolRepoStub) CookieRotationPool(context.Context) (*CookieRotationPoolSnapshot, error) {
	return r.snapshot, nil
}
func (r *cookiePoolRepoStub) UpdateCookieRotationPool(_ context.Context, fn func(*CookieRotationPoolSnapshot) error) error {
	return fn(r.snapshot)
}

func TestCookiePoolStickyAndFinalDispatchCannotUseStandby(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		t.Run(advanced, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			now := time.Now()
			a := cookieGuardTestAccount("one.example", now.Add(time.Hour))
			a.ID = 1
			b := cookieGuardTestAccount("one.example", now.Add(time.Hour))
			b.ID = 2
			g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{1: {State: "active", Since: now}}}
			cfg := poolTestConfig(poolTestPolicy(), g)
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			repo := &cookiePoolRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: string(raw), SettingKeyOpenAICodexCookieLibrary: `[{"host":"one.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"}]`}}, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, settingService: &SettingService{settingRepo: repo}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a, b}}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}), cache: &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:pool": 2}}}
			group := int64(2)
			ctx := withCookieSchedulingGroup(context.Background(), &group)
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &group, "", "pool", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.EqualValues(t, 1, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			_, err = svc.refreshCookieDispatchAccount(ctx, &b)
			require.Error(t, err)
			// A prepared binding is not admission; changing the group does not bypass the pool.
			g.Members[2] = CookieRotationPoolMember{State: "preparing", Since: now}
			_, err = svc.refreshCookieDispatchAccount(ctx, &b)
			require.Error(t, err)
			other := int64(4)
			require.Equal(t, "cookie_pool_other_group", svc.openAICookieSchedulingBlockReason(withCookieSchedulingGroup(context.Background(), &other), &a))
		})
	}
}

func poolTestPolicy() CookieRotationPoolPolicy {
	return CookieRotationPoolPolicy{GroupID: 2, Enabled: true, TargetConcurrency: 70, RedundancyPercent: 50, MinAccounts: 1, MaxAccounts: 10, ReadyTimeoutSeconds: 30, RetrySeconds: 60}
}
func poolTestConfig(p CookieRotationPoolPolicy, g *CookieRotationPoolGroup) *OpenAICookieSettings {
	return &OpenAICookieSettings{Enabled: true, CookieRotationEnabled: true, RotationGroupIDs: []int64{2}, DegradedGroupID: 3, RotationPools: []CookieRotationPoolPolicy{p},
		rotationPool: func() (*CookieRotationPoolSnapshot, error) {
			return &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}, nil
		},
		rotationCandidates: func() ([]OpenAICodexCookieLibraryEntry, error) {
			return []OpenAICodexCookieLibraryEntry{{Host: "one.example", Cookie: "cookie", ExpiresAt: time.Now().Add(time.Hour)}, {Host: "two.example", Cookie: "cookie", ExpiresAt: time.Now().Add(time.Hour)}}, nil
		}}
}
func TestCookiePoolCapacityReplacementAndRecovery(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	g := &CookieRotationPoolGroup{}
	cfg := poolTestConfig(p, g)
	accounts := make([]Account, 10)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("one.example", now.Add(time.Hour))
		accounts[i].ID = int64(i + 1)
		accounts[i].Concurrency = 35
	}
	run := func(at time.Time) { reconcileCookiePool(g, p, accounts, cfg, at, map[string]bool{}) }
	run(now)
	require.Len(t, g.Members, 3)
	require.Equal(t, 105, g.TargetCapacity)
	require.False(t, cookiePoolMayRotate(&accounts[3], cfg))
	_, reason := openAICookieSchedulingStatus(&accounts[3], cfg, now)
	require.Equal(t, "cookie_pool_waiting", reason)
	run(now.Add(time.Second))
	require.Equal(t, 105, g.ReadyCapacity)
	accounts[0].Extra[openAICodexCookieHostExtraKey] = ""
	accounts[0].Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"one.example": now.Add(time.Hour).Format(time.RFC3339Nano), "two.example": now.Add(time.Hour).Format(time.RFC3339Nano)}
	run(now.Add(2 * time.Second))
	require.Equal(t, "exhausted", g.Members[1].State)
	require.Equal(t, "preparing", g.Members[4].State)
	require.Equal(t, 70, g.ReadyCapacity)
	require.Equal(t, int64(3), cookieDegradedTarget(&accounts[0], cfg, now))
	run(now.Add(3 * time.Second))
	require.Equal(t, 105, g.ReadyCapacity)
	// Restored mailbox rejoins the queue, never preempts active accounts.
	accounts[0].Extra[openAICodexCookieCooldownsExtraKey] = nil
	accounts[0].Extra[openAICodexCookieHostExtraKey] = "one.example"
	run(now.Add(2 * time.Minute))
	require.False(t, cookiePoolMayRotate(&accounts[0], cfg))
	require.Zero(t, cookieDegradedTarget(&accounts[0], cfg, now))
	require.Equal(t, "active", g.Members[4].State)
	// A disabled pool cannot use even a healthy cached binding or degrade.
	cfg.RotationPools[0].Enabled = false
	_, reason = openAICookieSchedulingStatus(&accounts[1], cfg, now)
	require.Equal(t, "cookie_pool_disabled", reason)
	require.Zero(t, cookieDegradedTarget(&accounts[0], cfg, now))
}

func TestCookiePoolUnselectedAndUnknownHostNeverDegrade(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{}}
	cfg := poolTestConfig(p, g)
	a := cookieGuardTestAccount("old.example", now.Add(-time.Minute))
	a.Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"one.example": now.Add(time.Hour).Format(time.RFC3339Nano), "two.example": now.Add(time.Hour).Format(time.RFC3339Nano)}
	require.Zero(t, cookieDegradedTarget(&a, cfg, now), "unselected accounts never degrade, even with inherited cooldowns")
	g.Members[a.ID] = CookieRotationPoolMember{State: "active"}
	require.Equal(t, int64(3), cookieDegradedTarget(&a, cfg, now))
	a.Schedulable = false
	require.Zero(t, cookieDegradedTarget(&a, cfg, now))
	a.Schedulable = true
	cfg.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return nil, nil }
	require.Zero(t, cookieDegradedTarget(&a, cfg, now), "empty library does not prove exhaustion")
	cfg.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return nil, errors.New("offline") }
	require.Zero(t, cookieDegradedTarget(&a, cfg, now))
	cfg = poolTestConfig(p, g)
	delete(a.Extra, openAICodexCookieCooldownsExtraKey)
	require.Zero(t, cookieDegradedTarget(&a, cfg, now), "expiry alone cannot degrade")
}

func TestCookiePoolHeterogeneousCapacityTimeoutAndMailboxDedup(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	g := &CookieRotationPoolGroup{}
	cfg := poolTestConfig(p, g)
	accounts := make([]Account, 5)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("", time.Time{})
		accounts[i].ID = int64(i + 1)
		accounts[i].Concurrency = 60
	}
	accounts[0].Credentials["email"] = "same@example.com"
	accounts[1].Credentials["email"] = "Same@Example.com"
	reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
	require.Len(t, g.Members, 2)
	require.Equal(t, "preparing", g.Members[1].State)
	require.Equal(t, "preparing", g.Members[3].State)
	reconcileCookiePool(g, p, accounts, cfg, now.Add(31*time.Second), map[string]bool{})
	require.Equal(t, "waiting", g.Members[1].State)
	require.Zero(t, cookieDegradedTarget(&accounts[0], cfg, now))
	require.Equal(t, "preparing", g.Members[4].State)
}

func TestCookiePoolPolicyValidation(t *testing.T) {
	settings := cookieDegradedTestSettings()
	settings.defaultSubGroupReader = cookieDegradedGroupReader{groups: map[int64]*Group{2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive}}}
	cfg := &OpenAICookieSettings{RotationGroupIDs: []int64{2}, RotationPools: []CookieRotationPoolPolicy{poolTestPolicy()}}
	require.NoError(t, settings.validateCookieRotationPools(context.Background(), cfg))
	cfg.RotationPools[0].MaxAccounts = 0
	require.NoError(t, settings.validateCookieRotationPools(context.Background(), cfg))
	cfg.RotationPools = []CookieRotationPoolPolicy{poolTestPolicy(), poolTestPolicy()}
	require.Error(t, settings.validateCookieRotationPools(context.Background(), cfg))
	cfg.RotationPools = cfg.RotationPools[:1]
	cfg.RotationGroupIDs = nil
	require.Error(t, settings.validateCookieRotationPools(context.Background(), cfg))
}

type cookieEmailRepoStub struct {
	*cookieTestRepo
	emails map[string]map[string]time.Time
	fail   bool
}

func (r *cookieEmailRepoStub) CookieEmailCooldowns(_ context.Context, email string) (map[string]time.Time, error) {
	if r.fail {
		return nil, errors.New("offline")
	}
	return r.emails[email], nil
}
func (r *cookieEmailRepoStub) SaveCookieEmailCooldown(_ context.Context, email, host string, until time.Time) error {
	if r.fail {
		return errors.New("offline")
	}
	if r.emails[email] == nil {
		r.emails[email] = map[string]time.Time{}
	}
	if until.After(r.emails[email][host]) {
		r.emails[email][host] = until
	}
	return nil
}

func TestCookieEmailCooldownSurvivesReimportAndBlocksBinding(t *testing.T) {
	ctx := context.Background()
	a := cookieGuardTestAccount("", time.Time{})
	a.Credentials["email"] = " User@Example.COM "
	repo := &cookieEmailRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{}}, emails: map[string]map[string]time.Time{}}
	settings := &SettingService{settingRepo: repo}
	svc := &OpenAIGatewayService{settingService: settings}
	require.NoError(t, svc.markOpenAICodexCookieHostCooldown(ctx, &a, "ONE.EXAMPLE", 3600))
	// New ID and clean Extra are equivalent to deleting and reauthorizing the account.
	b := cookieGuardTestAccount("", time.Time{})
	b.ID = 999
	b.Credentials["email"] = "user@example.com"
	hydrated, err := settings.cookieEmailAccount(ctx, &b)
	require.NoError(t, err)
	require.True(t, openAICodexCookieHostCooldownUntil(hydrated, "one.example").After(time.Now()))
	require.ErrorContains(t, svc.bindOpenAICodexCookieHost(ctx, &b, "one.example"), "冷却")
	cfg := poolTestConfig(poolTestPolicy(), &CookieRotationPoolGroup{})
	result, err := svc.validateBindAndBuildOpenAICookieHostLocked(ctx, &b, cfg, "one.example", "cookie", "", "", "")
	require.NoError(t, err)
	require.Equal(t, "skipped", result)
	b.Credentials["email"] = "other@example.com"
	hydrated, err = settings.cookieEmailAccount(ctx, &b)
	require.NoError(t, err)
	// Validation copied mailbox cooldowns only into the local account; use a fresh reimport for a different identity.
	b.Extra = map[string]any{}
	hydrated, err = settings.cookieEmailAccount(ctx, &b)
	require.NoError(t, err)
	require.True(t, openAICodexCookieHostCooldownUntil(hydrated, "one.example").IsZero())
	repo.fail = true
	_, err = settings.cookieEmailAccount(ctx, &b)
	require.Error(t, err)
	status := settings.OpenAICookieBinding(ctx, &b)
	require.Equal(t, "unavailable", status.Status)
	require.False(t, status.AvailableHostCountKnown, "an unreadable mailbox must not claim zero available Hosts")
	require.Zero(t, status.DegradedGroupID, "database errors are not proof of Host exhaustion")
	repo.fail = false
	_, err = settings.cookieEmailAccount(ctx, &b)
	require.NoError(t, err, "mailbox reads recover immediately when the database recovers")
}
