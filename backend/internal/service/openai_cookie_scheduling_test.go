package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func cookieGuardTestSettings(t *testing.T) *SettingService {
	t.Helper()
	return &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{
		cookieSettingsKey: `{"cookie_host_scheduling_guard_enabled":true,"rotation_group_ids":[2],"cookie_rotation_enabled":true}`,
	}}}
}

func cookieGuardTestAccount(host string, expires time.Time) Account {
	return Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{2},
		Credentials: map[string]any{"chatgpt_account_id": "test-account"},
		Extra: map[string]any{openAICodexCookieHostExtraKey: host,
			"codex_cookie_host_binding_expires_at": expires.Format(time.RFC3339Nano)},
	}
}

func TestCookieHostSchedulingGuardScopeAndDeadline(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name   string
		change func(*Account, *OpenAICookieSettings)
		guard  bool
		reason string
	}{
		{"valid", nil, true, ""},
		{"unbound", func(a *Account, _ *OpenAICookieSettings) { delete(a.Extra, openAICodexCookieHostExtraKey) }, true, "cookie_host_unbound"},
		{"expired", func(a *Account, _ *OpenAICookieSettings) {
			a.Extra["codex_cookie_host_binding_expires_at"] = now.Add(-time.Second).Format(time.RFC3339Nano)
		}, true, "cookie_host_binding_expired"},
		{"exact deadline", func(a *Account, _ *OpenAICookieSettings) {
			a.Extra["codex_cookie_host_binding_expires_at"] = now.Format(time.RFC3339Nano)
		}, true, "cookie_host_binding_expired"},
		{"missing deadline", func(a *Account, _ *OpenAICookieSettings) { delete(a.Extra, "codex_cookie_host_binding_expires_at") }, true, "cookie_host_binding_invalid"},
		{"invalid deadline", func(a *Account, _ *OpenAICookieSettings) { a.Extra["codex_cookie_host_binding_expires_at"] = "invalid" }, true, "cookie_host_binding_invalid"},
		{"early rotation", func(a *Account, _ *OpenAICookieSettings) {
			a.Extra[openAICodexCookieRotationStatusExtraKey] = "running"
		}, true, ""},
		{"guard off", func(a *Account, s *OpenAICookieSettings) { s.CookieHostSchedulingGuardEnabled = false; a.Extra = nil }, false, ""},
		{"other group and explicit account", func(a *Account, s *OpenAICookieSettings) {
			a.GroupIDs = []int64{3}
			a.Extra = nil
			s.RotationAccountIDs = []int64{a.ID}
		}, false, ""},
		{"no groups", func(a *Account, s *OpenAICookieSettings) { s.RotationGroupIDs = nil; a.Extra = nil }, false, ""},
		{"account groups relation", func(a *Account, _ *OpenAICookieSettings) {
			a.GroupIDs = nil
			a.AccountGroups = []AccountGroup{{GroupID: 2}}
			a.Extra = nil
		}, true, "cookie_host_unbound"},
		{"groups relation", func(a *Account, _ *OpenAICookieSettings) {
			a.GroupIDs = nil
			a.Groups = []*Group{nil, &Group{ID: 2}}
			a.Extra = nil
		}, true, "cookie_host_unbound"},
		{"rotation and acquisition off", func(a *Account, s *OpenAICookieSettings) {
			s.CookieRotationEnabled = false
			s.Enabled = false
			a.Extra = nil
		}, true, "cookie_host_unbound"},
		{"api key", func(a *Account, _ *OpenAICookieSettings) { a.Type = AccountTypeAPIKey; a.Extra = nil }, false, ""},
		{"manual scheduling disabled", func(a *Account, _ *OpenAICookieSettings) { a.Schedulable = false }, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := cookieGuardTestAccount("old.example", now.Add(time.Minute))
			settings := &OpenAICookieSettings{CookieHostSchedulingGuardEnabled: true, RotationGroupIDs: []int64{2}, CookieRotationEnabled: true}
			if tc.change != nil {
				tc.change(&account, settings)
			}
			before, err := json.Marshal(account)
			require.NoError(t, err)
			guard, reason := openAICookieSchedulingStatus(&account, settings, now)
			require.Equal(t, tc.guard, guard)
			require.Equal(t, tc.reason, reason)
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "protection must not mutate account scheduling or binding state")
		})
	}
}

func TestCookieHostSchedulingGuardSelectionAndRecovery(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		for _, state := range []string{"unbound", "expired"} {
			t.Run(advanced+"/"+state, func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				ctx := context.Background()
				groupID := int64(2)
				blocked := cookieGuardTestAccount("old.example", time.Now().Add(-time.Second))
				if state == "unbound" {
					blocked.Extra = nil
				}
				backup := cookieGuardTestAccount("backup.example", time.Now().Add(time.Minute))
				backup.ID, backup.Priority = 2, 10
				repo := schedulerTestOpenAIAccountRepo{accounts: []Account{blocked, backup}}
				cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:cookie-guard": 1}}
				svc := &OpenAIGatewayService{accountRepo: repo, settingService: cookieGuardTestSettings(t),
					cache: cache, cfg: &config.Config{}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced),
					concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "cookie-guard", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.Equal(t, int64(2), selection.Account.ID, "sticky binding must not bypass protection")
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				status := svc.settingService.OpenAICookieBinding(ctx, &repo.accounts[0])
				require.True(t, status.SchedulingGuardEnabled)
				require.True(t, status.SchedulingBlocked)
				require.Equal(t, svc.openAICookieSchedulingBlockReason(ctx, &repo.accounts[0]), status.SchedulingBlockReason)
				require.True(t, repo.accounts[0].Schedulable)

				// A successful rotation releases only the temporary protection.
				repo.accounts[0].Extra = cookieGuardTestAccount("new.example", time.Now().Add(time.Minute)).Extra
				cache.sessionBindings["openai:cookie-guard"] = 1
				selection, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "cookie-guard", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.Equal(t, int64(1), selection.Account.ID)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				require.False(t, svc.settingService.OpenAICookieBinding(ctx, &repo.accounts[0]).SchedulingBlocked)
				repo.accounts[0].Schedulable = false
				cache.sessionBindings["openai:cookie-guard"] = 1
				selection, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "cookie-guard", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.Equal(t, int64(2), selection.Account.ID, "valid Cookie must not override manual disabling")
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			})
		}
	}
}

type cookieGuardValidationUpstream struct {
	HTTPUpstream
	check  func(*http.Request)
	answer string
	status int
}

func (u cookieGuardValidationUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.check(req)
	return &http.Response{StatusCode: u.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"output_text":%q}`, u.answer)))}, nil
}

type cookieGuardBindingRepo struct {
	AccountRepository
	updates []map[string]any
	err     error
}

func (r *cookieGuardBindingRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updates = append(r.updates, updates)
	return r.err
}

func TestCookieHostSchedulingGuardValidationHandoff(t *testing.T) {
	for _, state := range []string{"unbound", "expired", "early"} {
		for _, answer := range []string{"yes", "no", "error", "save-failed"} {
			t.Run(state+"/"+answer, func(t *testing.T) {
				ctx := context.Background()
				account := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
				if state == "unbound" {
					account.Extra = map[string]any{}
				}
				if state == "early" {
					account.Extra["codex_cookie_host_binding_expires_at"] = time.Now().Add(time.Minute).Format(time.RFC3339Nano)
				}
				oldHost, oldExpiry := openAICodexCookieHostFromAccount(&account), account.GetExtraString("codex_cookie_host_binding_expires_at")
				repo := &cookieGuardBindingRepo{}
				settingsService := cookieGuardTestSettings(t)
				settings, err := settingsService.GetOpenAICookieSettings(ctx)
				require.NoError(t, err)
				svc := &OpenAIGatewayService{settingService: settingsService, accountRepo: repo}
				calls := 0
				response, status := answer, http.StatusOK
				if answer == "error" {
					status = http.StatusInternalServerError
				}
				if answer == "save-failed" {
					response = "yes"
					repo.err = errors.New("save failed")
				}
				var firstRequestAt time.Time
				svc.httpUpstream = cookieGuardValidationUpstream{answer: response, status: status, check: func(req *http.Request) {
					calls++
					if firstRequestAt.IsZero() {
						firstRequestAt = time.Now()
					}
					require.Equal(t, "candidate=cookie", req.Header.Get("Cookie"))
					require.Equal(t, oldHost, openAICodexCookieHostFromAccount(&account))
					require.Equal(t, oldExpiry, account.GetExtraString("codex_cookie_host_binding_expires_at"))
					require.Equal(t, state != "early", svc.openAICookieSchedulingBlockReason(ctx, &account) != "")
					for _, update := range repo.updates {
						require.NotContains(t, update, openAICodexCookieHostExtraKey, "validation must not publish candidate binding")
					}
				}}
				start := time.Now()
				result, err := svc.validateBindAndBuildOpenAICookieHost(ctx, &account, settings, "new.example", "candidate=cookie", "", "ignored", "test-token")
				if answer == "save-failed" {
					require.Error(t, err)
					require.Equal(t, "error", result)
				} else {
					require.NoError(t, err)
					require.Equal(t, answer, result)
				}
				if answer == "error" {
					require.Equal(t, 3, calls)
				} else {
					require.Equal(t, 1, calls)
				}
				if answer == "yes" {
					require.Equal(t, "new.example", openAICodexCookieHostFromAccount(&account))
					expires, parseErr := time.Parse(time.RFC3339Nano, account.GetExtraString("codex_cookie_host_binding_expires_at"))
					require.NoError(t, parseErr)
					bindingStart := expires.Add(-time.Duration(settings.CookieHostBindingSeconds) * time.Second)
					require.False(t, bindingStart.Before(start))
					require.False(t, bindingStart.After(firstRequestAt))
					require.Equal(t, expires.Add(-time.Duration(settings.CookieHostRotationBeforeSeconds)*time.Second).Format(time.RFC3339Nano), account.GetExtraString(openAICodexCookieRotationNextExtraKey))
					require.Empty(t, svc.openAICookieSchedulingBlockReason(ctx, &account))
				} else {
					require.Equal(t, oldHost, openAICodexCookieHostFromAccount(&account))
					require.Equal(t, oldExpiry, account.GetExtraString("codex_cookie_host_binding_expires_at"), "failed attempt must not extend old binding")
				}
				require.True(t, account.Schedulable)
			})
		}
	}
}

func TestCookieHostSchedulingGuardSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := cookieGuardTestSettings(t)
	settings, err := s.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	require.NoError(t, s.SetOpenAICookieSettings(ctx, settings))
	loaded, err := (&SettingService{settingRepo: s.settingRepo}).GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	require.True(t, loaded.CookieHostSchedulingGuardEnabled)
	settings.CookieHostSchedulingGuardEnabled = false
	require.NoError(t, s.SetOpenAICookieSettings(ctx, settings))
	account := cookieGuardTestAccount("", time.Time{})
	guard, reason := s.openAICookieSchedulingStatus(ctx, &account)
	require.False(t, guard)
	require.Empty(t, reason)
}

func TestCookieHostSchedulingGuardFreshDBAndWSContinuation(t *testing.T) {
	ctx := context.Background()
	groupID := int64(2)
	stale := cookieGuardTestAccount("old.example", time.Now().Add(time.Minute))
	latest := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
	settings := cookieGuardTestSettings(t)
	value, err := settings.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	value.CookieRotationEnabled, value.WSEnabled = false, true
	require.NoError(t, settings.SetOpenAICookieSettings(ctx, value))
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	svc := &OpenAIGatewayService{
		settingService: settings, cfg: cfg, cache: &schedulerTestGatewayCache{},
		accountRepo:       schedulerTestOpenAIAccountRepo{accounts: []Account{latest}},
		schedulerSnapshot: &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{accountsByID: map[int64]*Account{1: &stale}}},
	}
	require.Nil(t, svc.recheckSelectedOpenAIAccountFromDB(ctx, &stale, &groupID, PlatformOpenAI, "", false, ""))
	store := svc.getOpenAIWSStateStore()
	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_cookie_guard", stale.ID, time.Hour))
	id, account, _, _ := svc.resolveAccountByPreviousResponseIDForCapability(ctx, &groupID, "resp_cookie_guard", "", nil, "", false)
	require.Zero(t, id)
	require.Nil(t, account, "WS continuation must recheck current binding, not trust stale snapshot")
}
