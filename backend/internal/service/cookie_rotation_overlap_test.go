package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookieRotationDestinationUsesPersistedMembership(t *testing.T) {
	now := time.Now()
	cfg := &OpenAICookieSettings{RotationGroupIDs: []int64{3}, DegradedGroupID: 3}
	native := cookieGuardTestAccount("candidate.example", now.Add(time.Minute))
	native.GroupIDs = []int64{3}
	source := cookieGuardTestAccount("", time.Time{})
	source.GroupIDs = []int64{2}
	source.openaiCookieDegraded = true
	require.True(t, isOpenAICookieRotationAccount(&native, cfg))
	require.False(t, isOpenAICookieRotationAccount(&source, cfg), "temporary degraded service never grants rotation membership")
	cfg.RotationGroupIDs = []int64{2, 3}
	p2, p3 := poolTestPolicy(), poolTestPolicy()
	p2.GroupID = 2
	p3.GroupID = 3
	cfg.RotationPools = []CookieRotationPoolPolicy{p2, p3}
	require.Equal(t, int64(2), cookiePoolPolicy(&source, cfg).GroupID)
	require.Equal(t, int64(3), cookiePoolPolicy(&native, cfg).GroupID)
	require.Equal(t, []int64{2}, source.GroupIDs)
}

func TestCookieRotationDestinationPoolDispatch(t *testing.T) {
	for _, tc := range []struct {
		emptyLibrary, noPool, quotaPaused, foreignSource                   bool
		name, state                                                        string
		exhausted, bound, disabled, unschedulable, monitor, unknownLibrary bool
		lowHost, keepDegraded, wantDegraded, wantBlocked                   bool
	}{
		{name: "empty library serves degraded", state: "preparing", emptyLibrary: true, wantDegraded: true},
		{name: "missing cookie serves degraded", state: "active", bound: true, emptyLibrary: true, wantDegraded: true},
		{name: "without pool serves during preparation", noPool: true, wantDegraded: true},
		{name: "without pool missing cookie serves degraded", noPool: true, bound: true, emptyLibrary: true, wantDegraded: true},
		{name: "quota pause blocks fallback", state: "active", exhausted: true, quotaPaused: true, wantBlocked: true},
		{name: "quota pause blocks host", state: "active", bound: true, quotaPaused: true, wantBlocked: true},
		{name: "other source still waits", state: "preparing", foreignSource: true, wantBlocked: true},
		{name: "other source still degrades on exhaustion", state: "exhausted", foreignSource: true, exhausted: true, wantDegraded: true},
		{name: "active keeps host", state: "active", bound: true},
		{name: "preparing keeps valid old host", state: "preparing", bound: true},
		{name: "waiting keeps valid old host", state: "waiting", bound: true},
		{name: "not yet admitted serves degraded", wantDegraded: true},
		{name: "active exhaustion before reconciliation", state: "active", exhausted: true, wantDegraded: true},
		{name: "preparing exhaustion", state: "preparing", exhausted: true, wantDegraded: true},
		{name: "exhausted stays in same group", state: "exhausted", exhausted: true, wantDegraded: true},
		{name: "stale cooled binding is suppressed", state: "exhausted", exhausted: true, bound: true, wantDegraded: true},
		{name: "untried candidate serves degraded", state: "preparing", wantDegraded: true},
		{name: "recovered candidate serves degraded during admission", state: "exhausted", wantDegraded: true},
		{name: "low host recovery preserves degraded service", state: "exhausted", lowHost: true, wantDegraded: true},
		{name: "low host waiting preserves degraded service", state: "waiting", lowHost: true, keepDegraded: true, wantDegraded: true},
		{name: "recovered active uses host", state: "active", bound: true, lowHost: true},
		{name: "standby serves degraded", state: "standby", exhausted: true, wantDegraded: true},
		{name: "disabled pool cannot self degrade", state: "exhausted", exhausted: true, disabled: true, wantBlocked: true},
		{name: "disabled account cannot self degrade", state: "exhausted", exhausted: true, unschedulable: true, wantBlocked: true},
		{name: "monitor cannot self degrade", state: "exhausted", exhausted: true, monitor: true, wantBlocked: true},
		{name: "unknown library cannot self degrade", state: "exhausted", exhausted: true, unknownLibrary: true, wantBlocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			group := int64(3)
			a := cookieGuardTestAccount("", time.Time{})
			a.GroupIDs = []int64{group}
			if tc.foreignSource {
				a.GroupIDs = []int64{2}
			}
			if tc.quotaPaused {
				a.Extra["codex_cookie_validation_resume_at"] = now.Add(time.Hour).Format(time.RFC3339Nano)
			}
			if tc.bound {
				a.Extra[openAICodexCookieHostExtraKey] = "candidate.example"
				a.Extra["codex_cookie_host_binding_expires_at"] = now.Add(time.Hour).Format(time.RFC3339Nano)
			}
			if tc.exhausted {
				exhaustCookieTestCandidates(&a)
			}
			a.Schedulable = !tc.unschedulable
			p := poolTestPolicy()
			p.GroupID, p.Enabled = group, !tc.disabled
			if tc.foreignSource {
				p.GroupID = 2
			}
			p.LowHostRotationEnabled, p.LowHostThreshold, p.LowHostAccountSlots = tc.lowHost, 5, 10
			cfg := &OpenAICookieSettings{Enabled: true, CookieRotationEnabled: true, RotationGroupIDs: []int64{2, group}, DegradedGroupID: group, RotationPools: []CookieRotationPoolPolicy{p}}
			if tc.noPool {
				cfg.RotationPools = nil
			}
			if tc.monitor {
				cfg.HostMonitor = &CookieHostMonitorConfig{Enabled: true, AccountID: a.ID}
			}
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			base := cookieDegradedTestSettings().settingRepo.(*cookieTestRepo)
			base.values[cookieSettingsKey] = string(raw)
			if tc.emptyLibrary {
				base.values[SettingKeyOpenAICodexCookieLibrary] = "[]"
			}
			if tc.unknownLibrary {
				base.values[SettingKeyOpenAICodexCookieLibrary] = "invalid json"
			}
			repo := &cookiePoolRepoStub{cookieTestRepo: base, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{
				p.GroupID: {Members: map[int64]CookieRotationPoolMember{a.ID: {State: tc.state, Since: now, KeepDegraded: tc.keepDegraded}}},
			}}}
			accounts := &schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: repo}, accountRepo: accounts}
			ctx := withCookieSchedulingGroup(context.Background(), &group)
			got, err := svc.refreshCookieDispatchAccount(ctx, &a)
			if tc.wantBlocked {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantDegraded, got.openaiCookieDegraded)
			require.True(t, svc.openAIAccountMatchesSchedulingGroup(ctx, got, &group))
			if tc.wantDegraded {
				require.Empty(t, openAICodexCookieHostFromAccount(got))
				require.Empty(t, got.openaiCookieDispatchCookie)
			} else {
				require.Equal(t, "candidate.example", openAICodexCookieHostFromAccount(got))
				require.NotEmpty(t, got.openaiCookieDispatchCookie)
			}
			require.Equal(t, a.GroupIDs, accounts.accounts[0].GroupIDs)
			require.False(t, accounts.accounts[0].openaiCookieDegraded, "degradation is request-local")
		})
	}
}

func TestCookieRotationDestinationDispatchUsesHostUntilExhausted(t *testing.T) {
	ctx := context.Background()
	settings := cookieDegradedTestSettings()
	repo := settings.settingRepo.(*cookieTestRepo)
	repo.values[cookieSettingsKey] = `{"rotation_group_ids":[2,3],"cookie_rotation_enabled":true,"degraded_group_id":3}`
	group := int64(3)
	ctx = withCookieSchedulingGroup(ctx, &group)
	native := cookieGuardTestAccount("candidate.example", time.Now().Add(time.Minute))
	native.GroupIDs = []int64{3}
	native.Extra[openAICodexCookieRotationStatusExtraKey] = "running"
	accounts := &schedulerTestOpenAIAccountRepo{accounts: []Account{native}}
	svc := &OpenAIGatewayService{settingService: settings, accountRepo: accounts}
	got, err := svc.refreshCookieDispatchAccount(ctx, &native)
	require.NoError(t, err)
	require.False(t, got.openaiCookieDegraded, "native healthy destination account must retain Host transport")
	require.Equal(t, "candidate.example", openAICodexCookieHostFromAccount(got))
	require.NotEmpty(t, got.openaiCookieDispatchCookie)
	delete(native.Extra, openAICodexCookieHostExtraKey)
	exhaustCookieTestCandidates(&native)
	accounts.accounts = []Account{native}
	got, err = svc.refreshCookieDispatchAccount(ctx, &native)
	require.NoError(t, err)
	require.True(t, got.openaiCookieDegraded, "exhausted native account can use ordinary transport within its own group")
	require.Empty(t, openAICodexCookieHostFromAccount(got))
	require.Equal(t, []int64{3}, accounts.accounts[0].GroupIDs)
	// Removing actual membership from configured scope leaves ordinary service.
	native.GroupIDs = []int64{4}
	cfg, err := settings.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	enabled, reason := cookieHostBindingStatus(&native, cfg, time.Now())
	require.False(t, enabled)
	require.Empty(t, reason)
}
