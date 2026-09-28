package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func cookieDegradedTestSettings() *SettingService {
	return &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: `{"rotation_group_ids":[2],"cookie_rotation_enabled":true,"degraded_group_id":3,"degraded_group_name":"降级组"}`, SettingKeyOpenAICodexCookieLibrary: `[{"host":"candidate.example","cookie":"candidate","expires_at":"2099-01-01T00:00:00Z"}]`}}}
}

func TestCookieDegradedUnboundRequiresExhaustedCandidates(t *testing.T) {
	ctx := context.Background()
	svc := cookieDegradedTestSettings()
	cfg, err := svc.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	a := cookieGuardTestAccount("", time.Time{})
	now := time.Now()
	require.Zero(t, cookieDegradedTarget(&a, cfg, now), "a candidate awaiting validation prevents degradation")
	a.Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"candidate.example": now.Add(time.Minute).Format(time.RFC3339Nano)}
	require.Equal(t, int64(3), cookieDegradedTarget(&a, cfg, now))
	status := svc.OpenAICookieBinding(ctx, &a)
	require.Equal(t, int64(3), status.DegradedGroupID)
	require.Zero(t, status.AvailableHostCount, "account cooldowns must be excluded from the displayed count")
	require.Zero(t, cookieDegradedTarget(&a, cfg, now.Add(2*time.Minute)), "cooldown expiry immediately makes the candidate available")
	_, err = svc.SetOpenAICodexCookieLibrary(ctx, []OpenAICodexCookieLibraryEntry{{Host: "new.example", Cookie: "new", ExpiresAt: now.Add(time.Hour)}})
	require.NoError(t, err)
	require.Zero(t, cookieDegradedTarget(&a, cfg, now), "new harvest invalidates the candidate snapshot immediately")
	status = svc.OpenAICookieBinding(ctx, &a)
	require.Zero(t, status.DegradedGroupID)
	require.True(t, status.SchedulingBlocked)
	require.Equal(t, 1, status.AvailableHostCount)
	_, err = svc.SetOpenAICodexCookieLibrary(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, int64(3), cookieDegradedTarget(&a, cfg, now), "empty library is exhausted")
	cfg.GroupIDs = []int64{3}
	require.True(t, isOpenAICookieCollector(&a, cfg), "exhausted unbound accounts can collect for the degraded group")
	a.GroupIDs = []int64{4}
	require.Zero(t, cookieDegradedTarget(&a, cfg, now), "unselected groups never degrade")
	a.GroupIDs = []int64{2}
	cfg.HostMonitor = &CookieHostMonitorConfig{Enabled: true, AccountID: a.ID}
	require.Zero(t, cookieDegradedTarget(&a, cfg, now), "monitor lock takes precedence")
}

func TestCookieDegradedUnknownLibraryDoesNotDegrade(t *testing.T) {
	svc := cookieDegradedTestSettings()
	svc.settingRepo.(*cookieTestRepo).values[SettingKeyOpenAICodexCookieLibrary] = "invalid json"
	cfg, err := svc.GetOpenAICookieSettings(context.Background())
	require.NoError(t, err)
	a := cookieGuardTestAccount("", time.Time{})
	require.Zero(t, cookieDegradedTarget(&a, cfg, time.Now()))
	cfg.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return nil, fmt.Errorf("network failure") }
	require.Zero(t, cookieDegradedTarget(&a, cfg, time.Now()))
}

func TestCookieDegradedRotationCandidateMatchesAccountState(t *testing.T) {
	now := time.Now()
	a := cookieGuardTestAccount("bound.example", now.Add(time.Minute))
	for _, tc := range []struct {
		name, host, cookie string
		expires            time.Time
		want               bool
	}{
		{"valid", "candidate.example", "cookie", now.Add(time.Hour), true},
		{"expired", "candidate.example", "cookie", now, false},
		{"missing expiry", "candidate.example", "cookie", time.Time{}, false},
		{"empty cookie", "candidate.example", "", now.Add(time.Hour), false},
		{"current host", "bound.example", "cookie", now.Add(time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAICookieRotationCandidate(&a, OpenAICodexCookieLibraryEntry{Host: tc.host, Cookie: tc.cookie, ExpiresAt: tc.expires}, now))
		})
	}
}

func TestCookieDegradedGroupCollectorsFollowTransientMembership(t *testing.T) {
	a := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
	settings := &OpenAICookieSettings{DegradedGroupID: 3, RotationGroupIDs: []int64{2}, GroupIDs: []int64{3}, CookieRotationEnabled: true}
	require.True(t, isOpenAICookieCollector(&a, settings))
	require.True(t, isExplicitOpenAICookieCollector(&a, settings))
	require.True(t, openAICookieProbeSettings(&a, settings).CookieRotationEnabled, "degraded collectors must retain their original rotation scope")
	require.Equal(t, []int64{2}, a.GroupIDs, "matching must not modify actual membership")
	a.Extra["codex_cookie_host_binding_expires_at"] = time.Now().Add(time.Minute).Format(time.RFC3339Nano)
	require.False(t, isOpenAICookieCollector(&a, settings), "recovered accounts leave degraded-only collection scope")
	require.False(t, isExplicitOpenAICookieCollector(&a, settings))
	delete(a.Extra, openAICodexCookieHostExtraKey)
	require.False(t, isOpenAICookieCollector(&a, settings), "unbound accounts do not become degraded collectors")
	settings.AccountIDs = []int64{a.ID}
	require.True(t, isOpenAICookieCollector(&a, settings), "directly selected collectors keep their existing behavior")
	settings.AccountIDs = nil
	settings.GroupIDs = []int64{2}
	require.True(t, isOpenAICookieCollector(&a, settings), "original explicit collection groups are preserved")
	settings.HostMonitor = &CookieHostMonitorConfig{Enabled: true, AccountID: a.ID}
	require.False(t, isOpenAICookieCollector(&a, settings))
	require.False(t, isExplicitOpenAICookieCollector(&a, settings))
}

func TestCookieDegradedGroupSelectionAndAutomaticRecovery(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		for _, state := range []string{"expired", "unbound", "invalid", "exhausted"} {
			t.Run(advanced+"/"+state, func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				ctx := context.Background()
				source, target, other := int64(2), int64(3), int64(4)
				a := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
				if state == "unbound" || state == "exhausted" {
					delete(a.Extra, openAICodexCookieHostExtraKey)
				}
				if state == "exhausted" {
					a.Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"candidate.example": time.Now().Add(time.Hour).Format(time.RFC3339Nano)}
				}
				if state == "invalid" {
					a.Extra["codex_cookie_host_binding_expires_at"] = "invalid"
				}
				repo := schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: repo, settingService: cookieDegradedTestSettings(), rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}), cache: &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:degraded": 1}}}
				selectGroup := func(group *int64) (*AccountSelectionResult, error) {
					selection, _, err := svc.SelectAccountWithScheduler(ctx, group, "", "degraded", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
					return selection, err
				}
				for _, group := range []*int64{&source, &other, nil} {
					selection, err := selectGroup(group)
					require.Error(t, err)
					require.Nil(t, selection)
				}
				selection, err := selectGroup(&target)
				if state == "unbound" {
					require.Error(t, err)
					require.Nil(t, selection, "unbound accounts must never enter the degraded group")
				} else {
					require.NoError(t, err)
					require.Equal(t, a.ID, selection.Account.ID)
					require.True(t, selection.Account.openaiCookieDegraded)
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
				}
				require.Equal(t, []int64{2}, repo.accounts[0].GroupIDs)
				require.True(t, repo.accounts[0].Schedulable)
				require.False(t, repo.accounts[0].openaiCookieDegraded)
				status := svc.settingService.OpenAICookieBinding(ctx, &repo.accounts[0])
				if state == "unbound" {
					require.Zero(t, status.DegradedGroupID)
					require.True(t, status.SchedulingBlocked)
					require.Equal(t, "cookie_host_unbound", status.SchedulingBlockReason)
				} else {
					require.Equal(t, int64(3), status.DegradedGroupID)
					require.False(t, status.SchedulingBlocked)
				}
				require.NotEmpty(t, status.SchedulingBlockReason)
				// Recovery changes only the binding, never account/group membership.
				repo.accounts[0].Extra[openAICodexCookieHostExtraKey] = "healthy.example"
				repo.accounts[0].Extra["codex_cookie_host_binding_expires_at"] = time.Now().Add(time.Minute).Format(time.RFC3339Nano)
				selection, err = selectGroup(&target)
				require.Error(t, err)
				require.Nil(t, selection)
				selection, err = selectGroup(&source)
				require.NoError(t, err)
				require.False(t, selection.Account.openaiCookieDegraded)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			})
		}
	}
}

func TestCookieDegradedGroupHonorsIsolationAndFinalAdmission(t *testing.T) {
	ctx := context.Background()
	target := int64(3)
	targetCtx := withCookieSchedulingGroup(ctx, &target)
	a := cookieGuardTestAccount("expired.example", time.Now().Add(-time.Minute))
	svc := &OpenAIGatewayService{settingService: cookieDegradedTestSettings()}
	clone := svc.cookieDegradedRequestAccount(targetCtx, &a)
	require.NotSame(t, &a, clone)
	headers := http.Header{"Cookie": []string{"__oailb=expired"}, openAICodexTurnStateHeader: []string{"stale"}}
	svc.applyOpenAIAccountBoundState(clone, headers)
	require.Empty(t, headers.Get("Cookie"))
	require.Empty(t, headers.Get(openAICodexTurnStateHeader))
	require.Empty(t, svc.openAICodexCookieForAccount(clone))
	require.Empty(t, openAICodexCookieHostFromAccount(clone))
	require.Equal(t, "expired.example", openAICodexCookieHostFromAccount(&a))
	a.Extra["codex_cookie_host_binding_expires_at"] = time.Now().Add(time.Minute).Format(time.RFC3339Nano)
	releases := 0
	selection, err := svc.newAcquiredSelectionResult(targetCtx, &a, func() { releases++ })
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	require.Equal(t, 1, releases)
	settings, err := svc.settingService.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	a.Extra["codex_cookie_host_binding_expires_at"] = time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
	a.GroupIDs = []int64{4}
	settings.RotationAccountIDs = []int64{a.ID}
	require.Zero(t, cookieDegradedTarget(&a, settings, time.Now()), "individual rotation selection does not enable group degradation")
	a.GroupIDs = []int64{2}
	settings.HostMonitor = &CookieHostMonitorConfig{Enabled: true, AccountID: a.ID}
	require.Zero(t, cookieDegradedTarget(&a, settings, time.Now()), "monitor isolation must never degrade")
}

type cookieDegradedGroupReader struct{ groups map[int64]*Group }

func (r cookieDegradedGroupReader) GetByID(_ context.Context, id int64) (*Group, error) {
	if group := r.groups[id]; group != nil {
		return group, nil
	}
	return nil, fmt.Errorf("group not found")
}

func TestCookieDegradedSettingsValidation(t *testing.T) {
	ctx := context.Background()
	settings := cookieGuardTestSettings(t)
	settings.defaultSubGroupReader = cookieDegradedGroupReader{groups: map[int64]*Group{3: {ID: 3, Name: "低优先级", Platform: PlatformOpenAI, Status: StatusActive}}}
	c, err := settings.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	c.DegradedGroupID = 2
	require.ErrorContains(t, settings.SetOpenAICookieSettings(ctx, c), "不能同时")
	c.DegradedGroupID = 999
	require.Error(t, settings.SetOpenAICookieSettings(ctx, c))
	c.DegradedGroupID = 3
	require.NoError(t, settings.SetOpenAICookieSettings(ctx, c))
	loaded, err := settings.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "低优先级", loaded.DegradedGroupName)
}

func TestCookieDegradedFreshDBAndWSRecovery(t *testing.T) {
	ctx := context.Background()
	source, target := int64(2), int64(3)
	stale := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
	latest := cookieGuardTestAccount("new.example", time.Now().Add(time.Minute))
	stale.Extra["openai_oauth_responses_websockets_v2_enabled"] = true
	latest.Extra["openai_oauth_responses_websockets_v2_enabled"] = true
	repo := schedulerTestOpenAIAccountRepo{accounts: []Account{latest}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, settingService: cookieDegradedTestSettings(), accountRepo: repo, cache: &schedulerTestGatewayCache{}, schedulerSnapshot: &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{accountsByID: map[int64]*Account{1: &stale}}}}
	svc.cfg.Gateway.OpenAIWS.Enabled = true
	svc.cfg.Gateway.OpenAIWS.OAuthEnabled = true
	svc.cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	store := svc.getOpenAIWSStateStore()
	require.NoError(t, store.BindResponseAccount(ctx, target, "resp_degraded", stale.ID, time.Hour))
	id, _, _, _ := svc.resolveAccountByPreviousResponseIDForCapability(ctx, &target, "resp_degraded", "", nil, "", false)
	require.Zero(t, id, "recovered account cannot remain sticky in degraded group")
	selection, err := svc.newSelectionResult(withCookieSchedulingGroup(ctx, &target), &stale, false, nil, nil)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	selection, err = svc.newSelectionResult(withCookieSchedulingGroup(ctx, &source), &stale, false, nil, nil)
	require.NoError(t, err)
	require.False(t, selection.Account.openaiCookieDegraded)
	repo.accounts[0].Extra["codex_cookie_host_binding_expires_at"] = time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
	id, account, _, _ := svc.resolveAccountByPreviousResponseIDForCapability(ctx, &target, "resp_degraded", "", nil, "", false)
	require.Equal(t, stale.ID, id)
	require.True(t, account.openaiCookieDegraded)
	repo.accounts[0].Schedulable = false
	selection, err = svc.newSelectionResult(withCookieSchedulingGroup(ctx, &target), &stale, false, nil, nil)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
}
