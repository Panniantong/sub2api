package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookiePoolNewAdmissionBypassesOldRetryOnly(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{1: {State: "preparing", Since: now}}}
	cfg := poolTestConfig(p, g)
	a := cookieGuardTestAccount("", time.Time{})
	a.Extra[openAICodexCookieRotationNextExtraKey] = now.Add(15 * time.Second).Format(time.RFC3339Nano)
	a.Extra[openAICodexCookieRotationStartedExtraKey] = now.Add(-time.Minute).Format(time.RFC3339Nano)
	require.True(t, cookiePoolRotationDue(&a, cfg, now), "new slot must start now, not 15 seconds later")
	a.Extra[openAICodexCookieRotationStartedExtraKey] = now.Add(time.Millisecond).Format(time.RFC3339Nano)
	require.False(t, cookiePoolRotationDue(&a, cfg, now.Add(time.Second)), "same admission respects backoff")
	a.Extra["codex_cookie_validation_resume_at"] = now.Add(time.Hour).Format(time.RFC3339Nano)
	require.False(t, cookiePoolRotationDue(&a, cfg, now.Add(time.Minute)), "quota reset always takes precedence")
}

func TestCookiePoolQuotaPausedReserveAndRecoveredOrdering(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	p.TargetConcurrency = 35
	p.RedundancyPercent = 0
	g := &CookieRotationPoolGroup{}
	cfg := poolTestConfig(p, g)
	accounts := make([]Account, 3)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("one.example", now.Add(time.Hour))
		accounts[i].ID = int64(i + 1)
		accounts[i].Concurrency = 35
	}
	accounts[0].Extra["codex_cookie_validation_resume_at"] = now.Add(time.Hour).Format(time.RFC3339Nano)
	reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
	require.Empty(t, g.Members[1].State)
	require.Equal(t, "preparing", g.Members[2].State)
	accounts[1].Schedulable = false
	reconcileCookiePool(g, p, accounts, cfg, now.Add(time.Second), map[string]bool{})
	require.Equal(t, "waiting", g.Members[2].State)
	require.False(t, g.Members[2].RetryAt.IsZero())
	require.Equal(t, "preparing", g.Members[3].State)
}

func TestCookiePoolMissingCookieDoesNotCountAsReady(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{1: {State: "active", Since: now}}}
	cfg := poolTestConfig(p, g)
	a := cookieGuardTestAccount("absent.example", now.Add(time.Hour))
	a.Concurrency = 105
	reconcileCookiePool(g, p, []Account{a}, cfg, now, map[string]bool{})
	require.Zero(t, g.ReadyCapacity)
	require.Equal(t, "preparing", g.Members[1].State)
	require.Zero(t, cookieDegradedTarget(&a, cfg, now), "missing library entry is not proof of exhaustion")
}

func TestCookieSettingsFailureClosesAdmission(t *testing.T) {
	settings := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: "invalid json"}}}
	a := cookieGuardTestAccount("one.example", time.Now().Add(time.Hour))
	svc := &OpenAIGatewayService{settingService: settings}
	require.Equal(t, "cookie_settings_unavailable", svc.openAICookieSchedulingBlockReason(context.Background(), &a))
	guarded, reason := settings.openAICookieSchedulingStatus(context.Background(), &a)
	require.True(t, guarded)
	require.Equal(t, "cookie_settings_unavailable", reason)
}

func TestCookiePoolValidationPublishesImmediatelyAndRejectsRevokedGeneration(t *testing.T) {
	for _, mode := range []string{"success", "revoked", "reassigned", "disabled", "removed_group", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			a := cookieGuardTestAccount("", time.Time{})
			g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{1: {State: "preparing", Since: now}}, TargetCapacity: 105}
			cfg := poolTestConfig(poolTestPolicy(), g)
			cfg.CookieHostBindingSeconds = 240
			cfg.CookieHostRotationBeforeSeconds = 10
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			repo := &cookiePoolRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: string(raw), SettingKeyOpenAICodexCookieLibrary: `[{"host":"one.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"}]`}}, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
			settings := &SettingService{settingRepo: repo}
			accountRepo := &cookieGuardBindingRepo{account: &a}
			svc := &OpenAIGatewayService{settingService: settings, accountRepo: accountRepo}
			svc.httpUpstream = cookieGuardValidationUpstream{status: http.StatusOK, answer: "yes", check: func(req *http.Request) {
				deadline, ok := req.Context().Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, deadline.Sub(now), 30*time.Second)
				m := g.Members[1]
				switch mode {
				case "revoked":
					m.State = "standby"
				case "reassigned":
					m.Since = m.Since.Add(time.Second)
				case "disabled":
					a.Schedulable = false
				case "removed_group":
					a.GroupIDs = []int64{9}
				case "timeout":
					m.Since = now.Add(-time.Minute)
				}
				g.Members[1] = m
			}}
			local := a
			result, err := svc.validateBindAndBuildOpenAICookieHostLocked(ctx, &local, cfg, "one.example", "cookie", "", "", "token")
			if mode == "success" {
				require.NoError(t, err)
				require.Equal(t, "yes", result)
				require.Equal(t, "active", g.Members[1].State, "must not wait for the next one-second worker tick")
				require.Equal(t, local.Concurrency, g.ReadyCapacity)
				_, reason := openAICookieSchedulingStatus(&local, cfg, time.Now())
				require.Empty(t, reason)
			} else {
				require.Error(t, err)
				require.Equal(t, "skipped", result)
				for _, update := range accountRepo.updates {
					require.NotContains(t, update, openAICodexCookieHostExtraKey, "revoked validation cannot publish a binding")
				}
			}
		})
	}
}

type cookieDistributedLockStub struct {
	*cookieTestRepo
	mu   sync.Mutex
	held map[string]bool
}

func (r *cookieDistributedLockStub) TryCookieRotationLock(_ context.Context, key string) (func(), bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.held[key] {
		return nil, false, nil
	}
	r.held[key] = true
	return func() { r.mu.Lock(); delete(r.held, key); r.mu.Unlock() }, true, nil
}
func TestCookieRotationMailboxLockAcrossGateways(t *testing.T) {
	repo := &cookieDistributedLockStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{}}, held: map[string]bool{}}
	one := &OpenAIGatewayService{settingService: &SettingService{settingRepo: repo}}
	two := &OpenAIGatewayService{settingService: &SettingService{settingRepo: repo}}
	a := cookieGuardTestAccount("", time.Time{})
	a.Credentials["email"] = "Same@Example.com"
	b := cookieGuardTestAccount("", time.Time{})
	b.ID = 99
	b.Credentials["email"] = "same@example.com"
	release, ok, err := one.lockCookieRotation(context.Background(), &a)
	require.NoError(t, err)
	require.True(t, ok)
	result, err := two.validateBindAndBuildOpenAICookieHost(context.Background(), &b, nil, "host", "cookie", "", "", "")
	require.NoError(t, err)
	require.Equal(t, "busy", result)
	release()
	release2, ok, err := two.lockCookieRotation(context.Background(), &b)
	require.NoError(t, err)
	require.True(t, ok)
	release2()
}

func TestCookiePoolReadyPublicationFailureIsNotSuccess(t *testing.T) {
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: &cookieTestRepo{}}}
	a := cookieGuardTestAccount("one.example", time.Now().Add(time.Hour))
	cfg := poolTestConfig(poolTestPolicy(), &CookieRotationPoolGroup{})
	require.Error(t, svc.publishCookiePoolReady(context.Background(), &a, cfg, CookieRotationPoolMember{}))
	cfg.rotationPool = func() (*CookieRotationPoolSnapshot, error) { return nil, errors.New("offline") }
	_, reason := openAICookieSchedulingStatus(&a, cfg, time.Now())
	require.Equal(t, "cookie_pool_unavailable", reason)
}
