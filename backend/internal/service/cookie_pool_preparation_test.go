package service

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookiePoolPreparationQuotaDoesNotOverrideRequestLoad(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		t.Run(advanced, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			now := time.Now()
			a := cookieGuardTestAccount("one.example", now.Add(time.Hour))
			a.ID, a.Concurrency = 1, 20
			b := cookieGuardTestAccount("one.example", now.Add(time.Hour))
			b.ID, b.Concurrency = 2, 20
			a.Extra["codex_5h_used_percent"], b.Extra["codex_5h_used_percent"] = 20.0, 80.0
			p := poolTestPolicy()
			p.QuotaPriority = "used_desc"
			g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{1: {State: "active", Since: now}, 2: {State: "active", Since: now}}}
			cfg := poolTestConfig(p, g)
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			repo := &cookiePoolRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: string(raw), SettingKeyOpenAICodexCookieLibrary: `[{"host":"one.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"}]`}}, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, settingService: &SettingService{settingRepo: repo}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a, b}}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{1: {AccountID: 1}, 2: {AccountID: 2, CurrentConcurrency: 14, LoadRate: 70}}}), cache: &schedulerTestGatewayCache{sessionBindings: map[string]int64{}}}
			svc.cfg.Gateway.Scheduling.LoadBatchEnabled = true
			svc.cfg.Gateway.OpenAIWS.LBTopK = 1
			group := int64(2)
			ctx := withCookieSchedulingGroup(context.Background(), &group)
			selected, _, err := svc.SelectAccountWithScheduler(ctx, &group, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selected)
			require.EqualValues(t, 1, selected.Account.ID)
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
		})
	}
}

type cookieSerializedPoolRepo struct {
	*cookiePoolRepoStub
	mu sync.Mutex
}

func (r *cookieSerializedPoolRepo) UpdateCookieRotationPool(_ context.Context, fn func(*CookieRotationPoolSnapshot) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fn(r.snapshot)
}

func TestCookiePoolConcurrentSuccessCannotOverAdmit(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	p.MaxAccounts = 3
	p.PreparationMultiplier = 2
	g := &CookieRotationPoolGroup{TargetCapacity: 105, Members: map[int64]CookieRotationPoolMember{}}
	cfg := poolTestConfig(p, g)
	for id := int64(1); id <= 6; id++ {
		g.Members[id] = CookieRotationPoolMember{State: "preparing", Since: now}
	}
	repo := &cookieSerializedPoolRepo{cookiePoolRepoStub: &cookiePoolRepoStub{snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}}
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: repo}}
	var wg sync.WaitGroup
	for id := int64(1); id <= 6; id++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			a := cookieGuardTestAccount("", time.Time{})
			a.ID, a.Concurrency = id, 35
			_ = svc.publishCookiePoolReady(context.Background(), &a, cfg, CookieRotationPoolMember{State: "preparing", Since: now}, func() error { return nil })
		}(id)
	}
	wg.Wait()
	active := 0
	for _, m := range g.Members {
		if m.State == "active" {
			active++
		}
	}
	require.Equal(t, 3, active)
	require.Equal(t, 105, g.ReadyCapacity)
}

func TestCookiePoolQuotaPriorityAndUnknownUsage(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	p.TargetConcurrency, p.RedundancyPercent, p.QuotaPriority = 35, 0, "used_desc"
	g := &CookieRotationPoolGroup{}
	cfg := poolTestConfig(p, g)
	accounts := make([]Account, 3)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("", time.Time{})
		accounts[i].ID, accounts[i].Concurrency = int64(i+1), 35
	}
	accounts[0].Extra["codex_5h_used_percent"] = 20.0
	accounts[1].Extra["codex_5h_used_percent"] = 90.0
	reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
	require.Equal(t, "preparing", g.Members[2].State)
	require.Empty(t, g.Members[1].State)
	accounts[1].Extra["codex_5h_reset_at"] = now.Add(-time.Second).Format(time.RFC3339Nano)
	_, known := cookiePoolQuotaUsed(&accounts[1], now)
	require.False(t, known, "expired window cannot get priority")
	require.Less(t, cookiePoolQuotaCompare(&accounts[0], &accounts[2], "remaining_desc", now), 0, "unknown is not presumed full")
}

func TestCookiePoolDoublePreparationFirstSuccessWins(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	p.PreparationMultiplier, p.MaxAccounts = 2, 3
	g := &CookieRotationPoolGroup{}
	cfg := poolTestConfig(p, g)
	accounts := make([]Account, 10)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("", time.Time{})
		accounts[i].ID, accounts[i].Concurrency = int64(i+1), 35
	}
	reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
	require.Len(t, g.Members, 6, "three required slots get six preparation slots")
	repo := &cookiePoolRepoStub{snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: repo}}
	for _, id := range []int64{6, 5, 4} {
		a := &accounts[id-1]
		require.NoError(t, svc.publishCookiePoolReady(context.Background(), a, cfg, g.Members[id], func() error {
			a.Extra[openAICodexCookieHostExtraKey] = "one.example"
			a.Extra["codex_cookie_host_binding_expires_at"] = now.Add(170 * time.Second).Format(time.RFC3339Nano)
			return nil
		}))
	}
	require.Equal(t, 105, g.ReadyCapacity)
	require.Error(t, svc.publishCookiePoolReady(context.Background(), &accounts[0], cfg, g.Members[1], func() error { t.Fatal("losing validation must not bind"); return nil }))
	require.Equal(t, "standby", g.Members[1].State)
	reconcileCookiePool(g, p, accounts, cfg, now.Add(time.Second), map[string]bool{})
	for _, id := range []int64{4, 5, 6} {
		require.Equal(t, "active", g.Members[id].State)
	}
	for _, id := range []int64{1, 2, 3} {
		require.Equal(t, "standby", g.Members[id].State)
	}
	require.False(t, cookiePoolMayRotate(&accounts[0], cfg))
}

func TestCookiePoolShrinkFinishesBindingAndCancelsRenewal(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	p.TargetConcurrency, p.RedundancyPercent = 35, 0
	g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{1: {State: "active", Since: now.Add(-120 * time.Second)}, 2: {State: "active", Since: now.Add(-120 * time.Second)}}}
	cfg := poolTestConfig(p, g)
	a := cookieGuardTestAccount("one.example", now.Add(time.Hour))
	a.ID, a.Concurrency = 1, 35
	b := cookieGuardTestAccount("one.example", now.Add(50*time.Second))
	b.ID, b.Concurrency = 2, 35
	b.Extra["codex_cookie_host_rotation_at"] = now.Add(-time.Second).Format(time.RFC3339Nano)
	expected := g.Members[2]
	reconcileCookiePool(g, p, []Account{a, b}, cfg, now, map[string]bool{})
	require.Equal(t, "draining", g.Members[2].State)
	require.WithinDuration(t, now.Add(50*time.Second), g.Members[2].DrainUntil, time.Millisecond)
	require.Equal(t, 70, g.ReadyCapacity, "draining binding remains usable")
	_, reason := openAICookieSchedulingStatus(&b, cfg, now)
	require.Empty(t, reason)
	require.False(t, cookiePoolRotationDue(&b, cfg, now))
	repo := &cookiePoolRepoStub{snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: repo}}
	require.Error(t, svc.publishCookiePoolReady(context.Background(), &b, cfg, expected, func() error { t.Fatal("in-flight renewal cannot extend draining binding"); return nil }))
	p.TargetConcurrency = 70
	reconcileCookiePool(g, p, []Account{a, b}, cfg, now.Add(time.Second), map[string]bool{})
	require.Equal(t, "active", g.Members[2].State, "rising demand revokes exit before expiry")
	p.TargetConcurrency = 35
	reconcileCookiePool(g, p, []Account{a, b}, cfg, now.Add(2*time.Second), map[string]bool{})
	require.WithinDuration(t, now.Add(50*time.Second), g.Members[2].DrainUntil, time.Millisecond, "deadline must never be restarted")
	reconcileCookiePool(g, p, []Account{a, b}, cfg, now.Add(51*time.Second), map[string]bool{})
	require.Equal(t, "standby", g.Members[2].State)
	require.Equal(t, 35, g.ReadyCapacity)
	_, reason = openAICookieSchedulingStatus(&b, cfg, now.Add(51*time.Second))
	require.NotEmpty(t, reason)
	require.False(t, cookiePoolMayRotate(&b, cfg))
}

func TestCookiePoolPreparationCapAndTimeout(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	p.PreparationMultiplier, p.MaxPreparingAccounts = 2, 4
	g := &CookieRotationPoolGroup{}
	cfg := poolTestConfig(p, g)
	accounts := make([]Account, 10)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("", time.Time{})
		accounts[i].ID, accounts[i].Concurrency = int64(i+1), 35
	}
	reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
	require.Len(t, g.Members, 4)
	reconcileCookiePool(g, p, accounts, cfg, now.Add(31*time.Second), map[string]bool{})
	for i := int64(1); i <= 4; i++ {
		require.Equal(t, "waiting", g.Members[i].State)
	}
	for i := int64(5); i <= 8; i++ {
		require.Equal(t, "preparing", g.Members[i].State)
	}
}

func TestCookiePoolShortageRetryPreservesSafetyGates(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	p.ShortageRetrySeconds = 10
	p.PreparationMultiplier = 2
	g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{}}
	cfg := poolTestConfig(p, g)
	accounts := make([]Account, 4)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("", time.Time{})
		accounts[i].ID, accounts[i].Concurrency = int64(i+1), 35
		g.Members[int64(i+1)] = CookieRotationPoolMember{State: "waiting", RetryAt: now.Add(60 * time.Second)}
	}
	accounts[1].Extra["codex_cookie_validation_resume_at"] = now.Add(time.Hour).Format(time.RFC3339Nano)
	accounts[2].Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"one.example": now.Add(time.Hour).Format(time.RFC3339Nano), "two.example": now.Add(time.Hour).Format(time.RFC3339Nano)}
	accounts[3].Schedulable = false
	reconcileCookiePool(g, p, accounts, cfg, now.Add(9*time.Second), map[string]bool{})
	require.Equal(t, "waiting", g.Members[1].State)
	reconcileCookiePool(g, p, accounts, cfg, now.Add(11*time.Second), map[string]bool{})
	require.Equal(t, "preparing", g.Members[1].State)
	for id := int64(2); id <= 4; id++ {
		require.Equal(t, "waiting", g.Members[id].State)
	}
	m := CookieRotationPoolMember{State: "waiting", RetryAt: now.Add(time.Minute)}
	require.Equal(t, m.RetryAt, cookiePoolEffectiveRetryAt(m, p, false), "no shortage keeps normal retry")
	m.State = "standby"
	require.Equal(t, m.RetryAt, cookiePoolEffectiveRetryAt(m, p, true), "natural binding retirement is not failure retry")
}
