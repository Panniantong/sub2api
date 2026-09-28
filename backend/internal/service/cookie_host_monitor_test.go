package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookieHostMonitorRecoveryIgnoresErrorsAndRequiresNo(t *testing.T) {
	now := time.Now()
	rows := []CookieHostMonitorSample{
		{ExperimentID: "one", Result: "yes", FinishedAt: now.Add(7 * time.Hour)},
		{ExperimentID: "one", Result: "error", FinishedAt: now.Add(6 * time.Hour)},
		{ExperimentID: "one", Result: "no", FinishedAt: now.Add(5 * time.Hour)},
		{ExperimentID: "one", Result: "no", FinishedAt: now},
	}
	first, last, recovered := CookieHostMonitorRecovery(rows, "one")
	require.Equal(t, now, *first)
	require.Equal(t, now.Add(5*time.Hour), *last)
	require.Equal(t, now.Add(7*time.Hour), *recovered)
	first, last, recovered = CookieHostMonitorRecovery(rows[:2], "one")
	require.Nil(t, first)
	require.Nil(t, last)
	require.Nil(t, recovered)
	first, last, recovered = CookieHostMonitorRecovery(rows[1:], "one")
	require.NotNil(t, first)
	require.NotNil(t, last)
	require.Nil(t, recovered)
}

func TestCookieHostMonitorScopeAndWait(t *testing.T) {
	c := CookieHostMonitorConfig{Enabled: true, Email: "a@example.com", Host: "fixed.example", ExperimentID: "one", StartedAt: time.Now(), InitialWaitSeconds: 21600, IntervalSeconds: 300}
	settings := &OpenAICookieSettings{HostMonitor: &c, RotationGroupIDs: []int64{2}}
	a := cookieGuardTestAccount("other.example", time.Now().Add(time.Hour))
	a.Credentials["email"] = "A@EXAMPLE.COM"
	require.True(t, cookieHostMonitorOwns(&a, settings))
	require.False(t, isOpenAICookieRotationAccount(&a, settings))
	require.False(t, isOpenAICookieCollector(&a, settings))
	_, reason := openAICookieSchedulingStatus(&a, settings, time.Now())
	require.Equal(t, "cookie_host_monitoring", reason)
	a.ID = 999
	require.True(t, cookieHostMonitorOwns(&a, settings), "email survives reimport")
	require.Equal(t, c.StartedAt.Add(6*time.Hour), CookieHostMonitorNext(c, []CookieHostMonitorSample{{ExperimentID: "one", FinishedAt: c.StartedAt.Add(time.Minute)}}))
	require.Equal(t, c.StartedAt.Add(7*time.Hour+5*time.Minute), CookieHostMonitorNext(c, []CookieHostMonitorSample{{ExperimentID: "one", FinishedAt: c.StartedAt.Add(7 * time.Hour)}}))
	c.Enabled = false
	require.False(t, cookieHostMonitorOwns(&a, settings))
}

func TestCookieHostMonitorConfigPreservesIdentityAndUnrelatedSettings(t *testing.T) {
	ctx := context.Background()
	svc := &OpenAIGatewayService{settingService: cookieGuardTestSettings(t)}
	stale, err := svc.settingService.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	c := CookieHostMonitorConfig{Enabled: true, Email: "A@EXAMPLE.COM", Host: "fixed.example", Model: "test-model", IntervalSeconds: 300, InitialWaitSeconds: 21600}
	require.NoError(t, svc.SetCookieHostMonitor(ctx, c))
	saved, err := svc.settingService.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	id := saved.HostMonitor.ExperimentID
	require.NotEmpty(t, id)
	require.Equal(t, "a@example.com", saved.HostMonitor.Email)
	require.NoError(t, svc.SetCookieHostMonitor(ctx, c))
	require.NoError(t, svc.settingService.SetOpenAICookieSettings(ctx, stale))
	saved, err = svc.settingService.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, id, saved.HostMonitor.ExperimentID, "stale settings cannot wipe monitor")
	c.Host = "new.example"
	require.ErrorContains(t, svc.SetCookieHostMonitor(ctx, c), "先关闭监控")
	c.Enabled = false
	require.NoError(t, svc.SetCookieHostMonitor(ctx, c))
	c.Enabled = true
	require.NoError(t, svc.SetCookieHostMonitor(ctx, c))
	saved, err = svc.settingService.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	require.NotEqual(t, id, saved.HostMonitor.ExperimentID)
}

type cookieMonitorRepo struct{ schedulerTestOpenAIAccountRepo }

func (r cookieMonitorRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			for key, value := range updates {
				if value == nil {
					delete(r.accounts[i].Extra, key)
				} else {
					r.accounts[i].Extra[key] = value
				}
			}
			return nil
		}
	}
	return ErrAccountNotFound
}

func (r cookieMonitorRepo) ListByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func TestCookieHostMonitorProbePinsActualBinding(t *testing.T) {
	ctx := context.Background()
	a := cookieGuardTestAccount("original.example", time.Now().Add(time.Minute))
	a.Credentials["email"] = "a@example.com"
	a.Credentials["access_token"] = "test-token"
	settings := cookieGuardTestSettings(t)
	_, err := settings.SetOpenAICodexCookieLibrary(ctx, []OpenAICodexCookieLibraryEntry{{Host: "fixed.example", Cookie: "monitor=cookie", ExpiresAt: time.Now().Add(time.Hour)}})
	require.NoError(t, err)
	svc := &OpenAIGatewayService{settingService: settings, accountRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{a}}}}
	calls := 0
	svc.httpUpstream = cookieGuardValidationUpstream{answer: "no", status: 200, check: func(req *http.Request) { calls++; require.Equal(t, "monitor=cookie", req.Header.Get("Cookie")) }}
	c := CookieHostMonitorConfig{Email: "a@example.com", Host: "fixed.example", Model: "gpt-6-astra", ExperimentID: "test"}
	row := svc.probeCookieHostMonitor(ctx, c)
	require.Equal(t, "no", row.Result)
	require.Equal(t, 1, calls)
	require.Equal(t, a.ID, row.AccountID)
	require.NotNil(t, row.CookieExpiresAt)
	require.False(t, row.FinishedAt.Before(row.StartedAt))
	require.Equal(t, "fixed.example", a.Extra[openAICodexCookieHostExtraKey])
	require.Nil(t, a.Extra["codex_cookie_host_binding_expires_at"])
	c.Host = "missing.example"
	row = svc.probeCookieHostMonitor(ctx, c)
	require.Equal(t, "cookie_unavailable", row.Result)
	require.Equal(t, 1, calls, "missing pinned Host must not fall back to another Host")
}

func TestCookieHostMonitorSelectedAccountAndHostLock(t *testing.T) {
	ctx := context.Background()
	a := cookieGuardTestAccount("original.example", time.Now().Add(time.Hour))
	a.Credentials["email"] = "selected@example.com"
	svc := &OpenAIGatewayService{settingService: cookieGuardTestSettings(t), accountRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{a}}}}
	c := CookieHostMonitorConfig{Enabled: true, AccountID: a.ID, Email: "ignored@example.com", Host: "fixed.example", IntervalSeconds: 300}
	require.NoError(t, svc.SetCookieHostMonitor(ctx, c))
	settings, err := svc.settingService.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "selected@example.com", settings.HostMonitor.Email)
	require.Equal(t, "fixed.example", a.Extra[openAICodexCookieHostExtraKey])
	require.Equal(t, "cookie_host_monitoring", svc.openAICookieSchedulingBlockReason(ctx, &a))
	require.ErrorContains(t, svc.bindOpenAICodexCookieHost(ctx, &a, "other.example"), "先关闭监控")
	require.ErrorContains(t, svc.unbindOpenAICodexCookieHost(ctx, &a, "fixed.example"), "先关闭监控")
	_, err = svc.ValidateAndBindOpenAICookieHost(ctx, &a, "other.example")
	require.ErrorContains(t, err, "先关闭监控")
	c = *settings.HostMonitor
	c.Enabled = false
	require.NoError(t, svc.SetCookieHostMonitor(ctx, c))
	require.NoError(t, svc.checkCookieHostMonitorLock(ctx, &a))
}
