package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookiePoolRecoveredHostClearsExhaustionWithoutAdmission(t *testing.T) {
	for _, recovery := range []string{"cooldown_expiry", "new_host"} {
		t.Run(recovery, func(t *testing.T) {
			now := time.Now()
			p := poolTestPolicy()
			p.TargetConcurrency, p.RedundancyPercent = 35, 0
			retryAt := now.Add(time.Minute)
			g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{1: {State: "active", Since: now}, 2: {State: "exhausted", Since: now.Add(-time.Hour), RetryAt: retryAt}}}
			cfg := poolTestConfig(p, g)
			a := cookieGuardTestAccount("one.example", now.Add(time.Hour))
			a.ID, a.Concurrency = 1, 35
			b := cookieGuardTestAccount("", time.Time{})
			b.ID, b.Concurrency = 2, 35
			cooldowns := map[string]string{"one.example": now.Add(time.Hour).Format(time.RFC3339Nano), "two.example": now.Add(time.Hour).Format(time.RFC3339Nano)}
			b.Extra[openAICodexCookieCooldownsExtraKey] = cooldowns
			_, reason := cookiePoolMember(&b, cfg)
			require.Equal(t, "cookie_pool_exhausted", reason)
			candidates := cfg.rotationCandidates
			cfg.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return nil, errors.New("offline") }
			reconcileCookiePool(g, p, []Account{a, b}, cfg, now, map[string]bool{})
			require.Equal(t, "exhausted", g.Members[2].State, "unknown library cannot establish recovery")
			cfg.rotationCandidates = candidates
			if recovery == "cooldown_expiry" {
				cooldowns["two.example"] = now.Add(-time.Second).Format(time.RFC3339Nano)
			} else {
				cfg.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) {
					return []OpenAICodexCookieLibraryEntry{{Host: "one.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)}, {Host: "new.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)}}, nil
				}
			}
			member, reason := cookiePoolMember(&b, cfg)
			require.Equal(t, "waiting", member.State)
			require.Equal(t, "cookie_pool_waiting", reason)
			require.Zero(t, cookieDegradedTarget(&b, cfg, now))
			require.False(t, cookiePoolMayRotate(&b, cfg))
			reconcileCookiePool(g, p, []Account{a, b}, cfg, now, map[string]bool{})
			require.Equal(t, "waiting", g.Members[2].State, "full pool must still clear stale exhaustion")
			require.Equal(t, retryAt, g.Members[2].RetryAt)
			require.Equal(t, 35, g.ReadyCapacity)
			p.TargetConcurrency = 70
			reconcileCookiePool(g, p, []Account{a, b}, cfg, now, map[string]bool{})
			require.Equal(t, "waiting", g.Members[2].State, "recovery cannot bypass retry delay")
			reconcileCookiePool(g, p, []Account{a, b}, cfg, retryAt.Add(time.Second), map[string]bool{})
			require.Equal(t, "preparing", g.Members[2].State, "recovered account may enter when demand and retry allow")
		})
	}
}

func TestCookieBindingCountAndExhaustionUseCurrentScopedHosts(t *testing.T) {
	now := time.Now()
	g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{1: {State: "exhausted"}}}
	cfg := poolTestConfig(poolTestPolicy(), g)
	cfg.HostWhitelist = []string{"one.example"}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &cookiePoolRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: string(raw), SettingKeyOpenAICodexCookieLibrary: `[{"host":"one.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"},{"host":"outside.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"}]`}}, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
	svc := &SettingService{settingRepo: repo}
	a := cookieGuardTestAccount("", time.Time{})
	a.ID = 1
	a.Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"one.example": now.Add(time.Hour).Format(time.RFC3339Nano)}
	status := svc.OpenAICookieBinding(context.Background(), &a)
	require.Zero(t, status.AvailableHostCount, "out-of-scope Hosts cannot be advertised as bindable")
	require.Equal(t, "cookie_pool_exhausted", status.SchedulingBlockReason)
	a.Extra[openAICodexCookieCooldownsExtraKey] = nil
	status = svc.OpenAICookieBinding(context.Background(), &a)
	require.Equal(t, 1, status.AvailableHostCount)
	require.Equal(t, "cookie_pool_waiting", status.SchedulingBlockReason)
	require.True(t, status.SchedulingBlocked)
	require.Zero(t, status.DegradedGroupID)
}
