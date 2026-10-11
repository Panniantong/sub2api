package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func lowHostFixture(t *testing.T, slots, candidates int) (CookieRotationPoolPolicy, *CookieRotationPoolGroup, *OpenAICookieSettings, []Account, *OpenAIGatewayService) {
	t.Helper()
	now := time.Now()
	p := poolTestPolicy()
	p.TargetConcurrency, p.RedundancyPercent, p.MaxAccounts, p.MaxPreparingAccounts = 35, 0, 1, 1
	p.LowHostRotationEnabled, p.LowHostThreshold, p.LowHostAccountSlots = true, 5, slots
	g := &CookieRotationPoolGroup{ReadyCapacity: 35, TargetCapacity: 35, Members: map[int64]CookieRotationPoolMember{1: {State: "active", Since: now}}}
	cfg := poolTestConfig(p, g)
	entries := []OpenAICodexCookieLibraryEntry{{Host: "one.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)}}
	cfg.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return entries, nil }
	accounts := make([]Account, candidates+1)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("", time.Time{})
		accounts[i].ID, accounts[i].Concurrency = int64(i+1), 35
	}
	accounts[0].Extra[openAICodexCookieHostExtraKey] = "one.example"
	accounts[0].Extra["codex_cookie_host_binding_expires_at"] = now.Add(time.Hour).Format(time.RFC3339Nano)
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	library, err := json.Marshal(entries)
	require.NoError(t, err)
	repo := &cookiePoolRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: string(raw), SettingKeyOpenAICodexCookieLibrary: string(library)}}, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: repo}}
	return p, g, cfg, accounts, svc
}

func TestCookieLowHostTenAccountsProcessDespiteFullPool(t *testing.T) {
	p, g, cfg, accounts, svc := lowHostFixture(t, 10, 10)
	now := time.Now()
	for batch := 0; batch < 5; batch++ {
		reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
		prepared := 0
		for i := 1; i < len(accounts); i++ {
			a := &accounts[i]
			m := g.Members[a.ID]
			if m.State != "preparing" {
				continue
			}
			prepared++
			require.True(t, m.LowHostForced)
			require.NoError(t, svc.publishCookiePoolReady(context.Background(), a, cfg, m, func() error {
				a.Extra[openAICodexCookieHostExtraKey] = "one.example"
				a.Extra["codex_cookie_host_binding_expires_at"] = now.Add(170 * time.Second).Format(time.RFC3339Nano)
				return nil
			}))
		}
		require.Equal(t, 2, prepared, "bounded background batches, independent of the ordinary preparation cap")
	}
	reconcileCookiePool(g, p, accounts, cfg, now.Add(time.Second), map[string]bool{})
	require.Equal(t, 10, cookieLowHostSlotsUsed(g))
	require.Equal(t, 385, g.ReadyCapacity)
	require.Equal(t, 350, g.LowHostReadyCapacity)
	for i := 1; i < len(accounts); i++ {
		require.Equal(t, "active", g.Members[accounts[i].ID].State)
		_, reason := openAICookieSchedulingStatus(&accounts[i], cfg, now)
		require.Empty(t, reason, "successful forced members must actually be dispatchable")
		require.False(t, cookiePoolMayRotate(&accounts[i], cfg), "do not consume another Host before this binding ends")
	}
	require.False(t, g.Members[1].LowHostForced, "ordinary incumbent retains its original admission")
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: accounts}
	normal, degraded := int64(2), int64(3)
	selected, err := svc.refreshCookieDispatchAccount(withCookieSchedulingGroup(context.Background(), &normal), &accounts[1])
	require.NoError(t, err, "final dispatch must allow the forced binding, not only the pool snapshot")
	require.Equal(t, "cookie", selected.openaiCookieDispatchCookie)
	_, err = svc.refreshCookieDispatchAccount(withCookieSchedulingGroup(context.Background(), &degraded), &accounts[1])
	require.Error(t, err, "successful recovery leaves degraded routing")
}

func TestCookieLowHostFailureReleasesSlotAndRetainsDegraded(t *testing.T) {
	p, g, cfg, accounts, _ := lowHostFixture(t, 1, 3)
	now := time.Now()
	accounts[1].Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"one.example": now.Add(-time.Second).Format(time.RFC3339Nano)}
	g.Members[2] = CookieRotationPoolMember{State: "exhausted", RetryAt: now.Add(time.Hour)}
	require.Equal(t, int64(3), cookieDegradedTarget(&accounts[1], cfg, now), "recovery retains proven degraded admission before next scan")
	reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
	require.True(t, g.Members[2].LowHostForced, "recovery event bypasses old exhausted retry delay")
	require.Equal(t, int64(3), cookieDegradedTarget(&accounts[1], cfg, now))
	accounts[1].Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"one.example": now.Add(time.Hour).Format(time.RFC3339Nano)}
	reconcileCookiePool(g, p, accounts, cfg, now.Add(time.Second), map[string]bool{})
	require.False(t, g.Members[2].LowHostForced)
	require.Equal(t, "exhausted", g.Members[2].State)
	require.Equal(t, int64(3), cookieDegradedTarget(&accounts[1], cfg, now))
	require.True(t, g.Members[3].LowHostForced, "failed account must not occupy the next account's slot")
	accounts[1].Extra[openAICodexCookieCooldownsExtraKey] = nil
	reconcileCookiePool(g, p, accounts, cfg, now.Add(2*time.Second), map[string]bool{})
	require.Equal(t, int64(3), cookieDegradedTarget(&accounts[1], cfg, now), "waiting behind a full forced pool remains usable as degraded")
	cfg.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return nil, errors.New("unavailable") }
	require.Zero(t, cookieDegradedTarget(&accounts[1], cfg, now), "unknown live state must fail closed")
}

func TestCookieLowHostExpirationRetiresWithoutRenewing(t *testing.T) {
	p, g, cfg, accounts, svc := lowHostFixture(t, 1, 2)
	now := time.Now()
	a := &accounts[1]
	a.Extra[openAICodexCookieHostExtraKey] = "one.example"
	a.Extra["codex_cookie_host_binding_expires_at"] = now.Add(-time.Second).Format(time.RFC3339Nano)
	g.Members[a.ID] = CookieRotationPoolMember{State: "active", Since: now.Add(-time.Minute), LowHostForced: true, LowHostBindingUntil: now.Add(-time.Second)}
	g.LowHostCursor = a.ID
	reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
	require.Equal(t, 35, g.ReadyCapacity, "expired binding is not counted ready")
	require.True(t, cookiePoolMayRotate(a, cfg), "expired binding must persist its cooldown before retirement")
	require.False(t, svc.rotateDueOpenAICookieHost(context.Background(), a, cfg), "retirement does not validate or renew a Host")
	require.Empty(t, openAICodexCookieHostFromAccount(a))
	require.True(t, openAICodexCookieHostCooldownUntil(a, "one.example").After(now))
	reconcileCookiePool(g, p, accounts, cfg, now.Add(time.Second), map[string]bool{})
	require.Equal(t, "exhausted", g.Members[a.ID].State)
	require.Equal(t, int64(3), cookieDegradedTarget(a, cfg, now))
	require.True(t, g.Members[3].LowHostForced, "next waiting account receives the released slot")
}

func TestCookieLowHostSafetyGatesAndStrictThreshold(t *testing.T) {
	for _, name := range []string{"disabled", "zero", "equal_threshold", "unknown", "quota", "account_disabled", "monitor", "duplicate_mailbox", "timeout"} {
		t.Run(name, func(t *testing.T) {
			p, g, cfg, accounts, _ := lowHostFixture(t, 1, 2)
			now := time.Now()
			a := &accounts[1]
			switch name {
			case "disabled":
				p.LowHostRotationEnabled = false
			case "zero":
				a.Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"one.example": now.Add(time.Hour).Format(time.RFC3339Nano)}
			case "equal_threshold":
				p.LowHostThreshold = 2
				cfg.rotationCandidates = poolTestConfig(p, g).rotationCandidates
			case "unknown":
				cfg.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return nil, errors.New("offline") }
			case "quota":
				a.Extra["codex_cookie_validation_resume_at"] = now.Add(time.Hour).Format(time.RFC3339Nano)
			case "account_disabled":
				a.Schedulable = false
			case "monitor":
				cfg.HostMonitor = &CookieHostMonitorConfig{Enabled: true, AccountID: a.ID}
			case "duplicate_mailbox":
				accounts[0].Credentials["email"], a.Credentials["email"] = "same@example.com", "Same@Example.com"
			case "timeout":
				g.Members[a.ID] = CookieRotationPoolMember{State: "preparing", LowHostForced: true, Since: now.Add(-time.Hour)}
			}
			cfg.RotationPools[0] = p
			reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
			require.False(t, g.Members[a.ID].LowHostForced)
			if name == "zero" {
				require.Equal(t, int64(3), cookieDegradedTarget(a, cfg, now))
			}
		})
	}
}

func TestCookieLowHostLateSuccessCannotReviveRevokedSlot(t *testing.T) {
	p, g, cfg, accounts, svc := lowHostFixture(t, 2, 3)
	now := time.Now()
	reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
	old := g.Members[3]
	p.LowHostAccountSlots, cfg.RotationPools[0].LowHostAccountSlots = 1, 1
	reconcileCookiePool(g, p, accounts, cfg, now.Add(time.Second), map[string]bool{})
	require.Equal(t, 1, cookieLowHostSlotsUsed(g))
	require.Error(t, svc.publishCookiePoolReady(context.Background(), &accounts[2], cfg, old, func() error {
		t.Fatal("revoked validation must not persist its binding")
		return nil
	}))
	p.LowHostRotationEnabled, cfg.RotationPools[0].LowHostRotationEnabled = false, false
	old = g.Members[2]
	require.Error(t, svc.publishCookiePoolReady(context.Background(), &accounts[1], cfg, old, func() error {
		t.Fatal("disabled policy must not publish success")
		return nil
	}))
}

func TestCookieLowHostPolicyRoundTripAndValidation(t *testing.T) {
	p, _, cfg, _, svc := lowHostFixture(t, 10, 1)
	svc.settingService.defaultSubGroupReader = cookieDegradedGroupReader{groups: map[int64]*Group{2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive}}}
	loaded, err := svc.settingService.GetOpenAICookieSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, p, loaded.RotationPools[0])
	for _, values := range [][2]int{{1, 10}, {5, 0}, {10001, 10}, {5, 10001}, {-1, 10}} {
		cfg.RotationPools[0].LowHostThreshold, cfg.RotationPools[0].LowHostAccountSlots = values[0], values[1]
		require.Error(t, svc.settingService.validateCookieRotationPools(context.Background(), cfg))
	}
	cfg.RotationPools[0] = p
	require.NoError(t, svc.settingService.validateCookieRotationPools(context.Background(), cfg))
}
