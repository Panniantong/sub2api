package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookieValidationExpiredAdmissionDoesNotCoolUntestedHosts(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "expired_admission", true: "canceled_parent"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := cookieGuardTestAccount("", time.Time{})
			g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{a.ID: {State: "preparing", Since: time.Now().Add(-time.Minute)}}}
			cfg := poolTestConfig(poolTestPolicy(), g)
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			repo := &cookiePoolRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: string(raw), SettingKeyOpenAICodexCookieLibrary: `[{"host":"one.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"},{"host":"two.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"}]`}}, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
			settings := &SettingService{settingRepo: repo}
			accountRepo := &cookieGuardBindingRepo{account: &a}
			calls := 0
			svc := &OpenAIGatewayService{settingService: settings, accountRepo: accountRepo, httpUpstream: cookieGuardValidationUpstream{status: 200, answer: "yes", check: func(*http.Request) { calls++ }}}
			if canceled {
				cancel()
			}
			result, err := svc.validateBindAndBuildOpenAICookieHostLocked(ctx, &a, cfg, "one.example", "cookie", "", "", "token")
			require.Error(t, err)
			require.Equal(t, "skipped", result)
			require.Zero(t, calls)
			require.True(t, openAICodexCookieHostCooldownUntil(&a, "one.example").IsZero())
			logs, err := settings.GetOpenAICookieValidationLogs(context.Background())
			require.NoError(t, err)
			for _, log := range logs {
				require.NotEqual(t, "validation_failed", log.Stage)
				require.NotEqual(t, "validation_started", log.Stage)
			}
		})
	}
}
