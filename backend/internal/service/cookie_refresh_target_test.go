package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cookieRefreshUpstream struct {
	HTTPUpstream
	request *http.Request
	cookie  string
}

func (u *cookieRefreshUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.request = req
	return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": {u.cookie + "; Path=/"}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
}

func TestCookieRefreshCarriesTargetCookieAndOnlyUpdatesTarget(t *testing.T) {
	for _, tc := range []struct {
		name, host string
		iat        int64
		stage      string
	}{
		{"newer", "target.example", 200, "updated"},
		{"older", "target.example", 50, "ignored_older"},
		{"other host", "other.example", 200, "ignored_host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{}}}
			old := responseSyncTestCookie("target.example", 100, time.Now().Add(time.Minute).Unix())
			require.NoError(t, settings.UpsertOpenAICodexCookie(context.Background(), OpenAICodexCookieLibraryEntry{Cookie: old}))
			upstream := &cookieRefreshUpstream{cookie: responseSyncTestCookie(tc.host, tc.iat, time.Now().Add(time.Hour).Unix())}
			svc := &OpenAIGatewayService{settingService: settings, httpUpstream: upstream}
			cfg := OpenAICodexTicketConfigDefaults(config.OpenAICodexTicketConfig{})
			cfg.CookieHarvestProxyURLs = []string{"http://proxy.example:8080"}
			account := &Account{ID: 1, Name: "collector", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "collector-token", "chatgpt_account_id": "collector-id"}, Extra: map[string]any{openAICodexCookieExtraKey: "bound=other", "session_id": "old-session"}}
			svc.probeOpenAICodexTicket(context.Background(), account, cfg, &OpenAICookieSettings{harvestTask: "refresh", harvestTarget: "target.example", AutoValidateHost: true})
			require.NotNil(t, upstream.request)
			require.Equal(t, old, upstream.request.Header.Get("Cookie"))
			require.Equal(t, "Bearer collector-token", upstream.request.Header.Get("Authorization"))
			require.Empty(t, upstream.request.Header.Get(openAICodexTurnStateHeader))
			require.Equal(t, "bound=other", account.Extra[openAICodexCookieExtraKey])
			require.Equal(t, "old-session", account.Extra["session_id"])
			entries, err := settings.GetOpenAICodexCookieLibrary(context.Background())
			require.NoError(t, err)
			require.Len(t, entries, 1)
			want := old
			if tc.stage == "updated" {
				want = upstream.cookie
			}
			require.Equal(t, want, entries[0].Cookie)
			logs, err := settings.GetOpenAICookieLogs(context.Background())
			require.NoError(t, err)
			require.Len(t, logs, 1)
			require.Equal(t, "refresh", logs[0].Task)
			require.Equal(t, "target.example", logs[0].TargetHost)
			require.Equal(t, tc.stage, logs[0].Stage)
			require.Empty(t, svc.loadOpenAICookieProxyBindings(context.Background()), "forced Host requests must not pollute proxy discovery")
		})
	}
}

func TestCookieRefreshTargetsLibraryWithoutProxyHistory(t *testing.T) {
	r := cookieHarvestRuntime{}
	r.init()
	settings := &OpenAICookieSettings{CookieProxyScheduleMode: "dynamic", CookieRefreshBeforeSeconds: 600, HarvestPolicy: &CookieHarvestPolicy{ExplorePercent: 1, RefreshPercent: 99}}
	task := r.plan(time.Now(), settings, []string{"proxy"}, nil, []OpenAICodexCookieLibraryEntry{{Host: "response.example", ExpiresAt: time.Now().Add(time.Minute)}})
	require.NotNil(t, task)
	require.Equal(t, "refresh", task.kind)
	require.Equal(t, "response.example", task.host)
}

func TestCookieRefreshSkipsAlreadyRenewedTarget(t *testing.T) {
	settings := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{}}}
	cookie := responseSyncTestCookie("target.example", 200, time.Now().Add(time.Hour).Unix())
	require.NoError(t, settings.UpsertOpenAICodexCookie(context.Background(), OpenAICodexCookieLibraryEntry{Cookie: cookie}))
	upstream := &cookieRefreshUpstream{}
	svc := &OpenAIGatewayService{settingService: settings, httpUpstream: upstream}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "collector-token", "chatgpt_account_id": "collector-id"}}
	cfg := OpenAICodexTicketConfigDefaults(config.OpenAICodexTicketConfig{})
	cfg.CookieHarvestProxyURLs = []string{"http://proxy.example:8080"}
	svc.probeOpenAICodexTicket(context.Background(), account, cfg, &OpenAICookieSettings{harvestTask: "refresh", harvestTarget: "target.example", CookieRefreshBeforeSeconds: 600})
	require.Nil(t, upstream.request)
	logs, err := settings.GetOpenAICookieLogs(context.Background())
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, "ignored_fresh", logs[0].Stage)
}

type staleCookieValidationUpstream struct {
	HTTPUpstream
	stale             string
	validationCookies []string
}

func (u *staleCookieValidationUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if req.Header.Get("Cookie") == "" {
		return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": {u.stale}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
	}
	u.validationCookies = append(u.validationCookies, req.Header.Get("Cookie"))
	return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"unsupported model"}}`))}, nil
}

func TestCookieAcquisitionValidatesCurrentLibraryAfterRejectingStaleCookie(t *testing.T) {
	for _, relay := range []bool{false, true} {
		t.Run(fmt.Sprintf("relay=%v", relay), func(t *testing.T) {
			settings := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: `{}`}}}
			current := responseSyncTestCookie("target.example", 200, time.Now().Add(time.Hour).Unix())
			require.NoError(t, settings.UpsertOpenAICodexCookie(context.Background(), OpenAICodexCookieLibraryEntry{Cookie: current}))
			upstream := &staleCookieValidationUpstream{stale: responseSyncTestCookie("target.example", 100, time.Now().Add(time.Hour).Unix())}
			svc := &OpenAIGatewayService{settingService: settings, httpUpstream: upstream}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "collector-token", "chatgpt_account_id": "collector-id"}}
			cfg := OpenAICodexTicketConfigDefaults(config.OpenAICodexTicketConfig{})
			cfg.CookieHarvestProxyURLs = []string{"http://proxy.example:8080"}
			options := &OpenAICookieSettings{AutoValidateHost: true}
			if relay {
				log := &OpenAICookieAcquisitionLog{}
				require.True(t, svc.acceptOpenAICodexRelayMint(context.Background(), account, "collector-token", "model", "session", cfg, options, log, openAICodexRelayMintResult{Cookie: upstream.stale, StatusCode: 200}))
				require.Equal(t, "ignored_older", log.Stage)
			} else {
				svc.probeOpenAICodexTicket(context.Background(), account, cfg, options)
				logs, err := settings.GetOpenAICookieLogs(context.Background())
				require.NoError(t, err)
				require.Equal(t, "ignored_older", logs[0].Stage)
			}
			require.Equal(t, []string{current}, upstream.validationCookies)
		})
	}
}

func TestCookieRefreshUnchangedResponseBacksOffTarget(t *testing.T) {
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{}}}}
	policy := defaultCookieHarvestPolicy()
	policy.TargetMissLimit = 2
	options := &OpenAICookieSettings{HarvestPolicy: policy, harvestProxy: "http://proxy.example:80"}
	item := &OpenAICookieAcquisitionLog{harvestAttempted: true, Task: "refresh", TargetHost: "target.example", Host: "target.example", StatusCode: 200, Stage: "ignored_older", Success: true}
	for i := 0; i < 2; i++ {
		svc.recordCookieHarvestOutcome(options, item)
	}
	require.True(t, svc.cookieHarvestRuntime.deferredTargets["http://proxy.example:80\ntarget.example"].After(time.Now()))
}
