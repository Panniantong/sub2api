package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cookieTestRepo struct {
	SettingRepository
	values map[string]string
}

type cookieHarvestAccountStateRepo struct {
	AccountRepository
	setErrorCalls int
	accountID     int64
	errorMessage  string
}

func (r *cookieHarvestAccountStateRepo) SetError(_ context.Context, id int64, message string) error {
	r.setErrorCalls++
	r.accountID = id
	r.errorMessage = message
	return nil
}

type cookieManagedProxyRepo struct {
	ProxyRepository
	active []Proxy
	err    error
}

func (r *cookieManagedProxyRepo) ListActive(context.Context) ([]Proxy, error) {
	return r.active, r.err
}

func (r *cookieManagedProxyRepo) ListByIDs(_ context.Context, ids []int64) ([]Proxy, error) {
	selected := make([]Proxy, 0, len(ids))
	for _, id := range ids {
		for i := range r.active {
			if r.active[i].ID == id {
				selected = append(selected, r.active[i])
				break
			}
		}
	}
	return selected, r.err
}

func (r *cookieTestRepo) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}
func (r *cookieTestRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func TestOpenAICookieBindingEstimatedAvailableDuration(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	for _, tc := range []struct {
		name     string
		host     string
		binding  int
		advance  int
		count    int
		estimate int64
	}{
		{"bound", "current.example", 240, 10, 3, 690},
		{"unbound", "", 240, 10, 4, 920},
		{"updated config", "current.example", 600, 30, 3, 1710},
		{"immediate rotation", "current.example", 240, 240, 3, 0},
		{"invalid legacy interval", "current.example", 240, 300, 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{
				cookieSettingsKey: fmt.Sprintf(`{"cookie_host_binding_seconds":%d,"cookie_host_rotation_before_seconds":%d}`, tc.binding, tc.advance),
			}}}
			_, err := svc.SetOpenAICodexCookieLibrary(ctx, []OpenAICodexCookieLibraryEntry{
				{Host: "current.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)},
				{Host: "one.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)},
				{Host: "two.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)},
				{Host: "three.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)},
				{Host: "expired.example", Cookie: "cookie", ExpiresAt: now.Add(-time.Minute)},
				{Host: "cooldown.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)},
			})
			require.NoError(t, err)
			account := cookieGuardTestAccount(tc.host, now.Add(time.Minute))
			account.Extra[openAICodexCookieCooldownsExtraKey] = map[string]string{"cooldown.example": now.Add(time.Minute).Format(time.RFC3339Nano)}
			status := svc.OpenAICookieBinding(ctx, &account)
			require.NotNil(t, status)
			require.Equal(t, tc.count, status.AvailableHostCount)
			require.Equal(t, tc.binding, status.BindingSeconds)
			require.Equal(t, tc.advance, status.RotationBeforeSeconds)
			require.NotNil(t, status.EstimatedBindingSeconds)
			require.Equal(t, tc.estimate, *status.EstimatedBindingSeconds)
		})
	}
}

func TestIndependentCookieConfigurationAndWS(t *testing.T) {
	ctx := context.Background()
	repo := &cookieTestRepo{values: map[string]string{}}
	settings := &SettingService{settingRepo: repo}
	value, err := settings.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	value.Enabled = true
	value.Model = "cookie-model"
	value.WSEnabled = false
	require.NoError(t, settings.SetOpenAICookieSettings(ctx, value))
	ticket, err := settings.GetOpenAICodexTicketSettings(ctx)
	require.NoError(t, err)
	require.NotEqual(t, "cookie-model", ticket.Model)
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 10, Extra: map[string]any{openAICodexCookieHostExtraKey: "host.example"}}
	resolver := NewOpenAIWSProtocolResolver(cfg, settings)
	require.Equal(t, "cookie_ws_disabled", resolver.Resolve(account).Reason)
	value.WSEnabled = true
	require.NoError(t, settings.SetOpenAICookieSettings(ctx, value))
	require.Equal(t, OpenAIUpstreamTransportResponsesWebsocketV2, resolver.Resolve(account).Transport)
}

func TestResolveOpenAICookieProxyURLsMergesManagedProxies(t *testing.T) {
	expiredAt := time.Now().Add(-time.Minute)
	settings := &OpenAICookieSettings{
		ProxyURLs:            []string{"socks5://manual:secret@proxy.example:1080"},
		UseAllManagedProxies: true,
	}
	svc := &SettingService{proxyRepo: &cookieManagedProxyRepo{active: []Proxy{
		{Protocol: "socks5", Host: "proxy.example", Port: 1080, Username: "manual", Password: "secret", Status: StatusActive},
		{Protocol: "http", Host: "managed.example", Port: 8080, Status: StatusActive},
		{Protocol: "http", Host: "expired.example", Port: 8080, Status: StatusActive, ExpiresAt: &expiredAt},
	}}}

	result := svc.resolveOpenAICookieProxyURLs(context.Background(), settings)
	require.Equal(t, []string{
		"socks5://manual:secret@proxy.example:1080",
		"http://managed.example:8080",
	}, result)
}

func TestResolveOpenAICookieProxyURLsKeepsManualPoolWhenManagedDisabled(t *testing.T) {
	settings := &OpenAICookieSettings{ProxyURLs: []string{"http://manual.example:8080"}}
	repo := &cookieManagedProxyRepo{active: []Proxy{{Protocol: "http", Host: "managed.example", Port: 8080, Status: StatusActive}}}
	svc := &SettingService{proxyRepo: repo}

	require.Equal(t, settings.ProxyURLs, svc.resolveOpenAICookieProxyURLs(context.Background(), settings))
}

func TestResolveOpenAICookieProxyURLsUsesSelectedManagedProxies(t *testing.T) {
	settings := &OpenAICookieSettings{ManagedProxyIDs: []int64{2}}
	repo := &cookieManagedProxyRepo{active: []Proxy{
		{ID: 1, Protocol: "http", Host: "first.example", Port: 8080, Status: StatusActive},
		{ID: 2, Protocol: "socks5", Host: "selected.example", Port: 1080, Status: StatusActive},
	}}
	svc := &SettingService{proxyRepo: repo}

	require.Equal(t, []string{"socks5://selected.example:1080"}, svc.resolveOpenAICookieProxyURLs(context.Background(), settings))
}

type cookieTestUpstream struct {
	HTTPUpstream
	t      *testing.T
	cookie string
}

type cookieDeadlineBody struct {
	ctx context.Context
}

func (b *cookieDeadlineBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, io.EOF
}

func (b *cookieDeadlineBody) Close() error { return nil }

type cookieDeadlineUpstream struct {
	HTTPUpstream
	cookie string
}

func (u *cookieDeadlineUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Set-Cookie": []string{u.cookie + "; Path=/"}},
		Body:       &cookieDeadlineBody{ctx: req.Context()},
	}, nil
}

func (u *cookieTestUpstream) Do(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	require.Equal(u.t, "http://proxy.example:8080", proxy)
	require.Empty(u.t, req.Header.Get("Cookie"))
	require.Empty(u.t, req.Header.Get(openAICodexTurnStateHeader))
	require.NotEmpty(u.t, req.Header.Get("session_id"))
	require.NotEqual(u.t, "old-session", req.Header.Get("session_id"))
	return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": []string{u.cookie + "; Path=/"}, "Session_id": []string{"upstream-session"}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
}

func TestIndependentCookieAcquisitionDoesNotBindTicket(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"host": "host.example", "exp": time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	cookie := "__oailb=e30." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	repo := &cookieTestRepo{values: map[string]string{}}
	settings := &SettingService{settingRepo: repo}
	cfg := OpenAICodexTicketConfigDefaults(config.OpenAICodexTicketConfig{})
	cfg.CookieHarvestProxyURLs = []string{"http://proxy.example:8080"}
	svc := &OpenAIGatewayService{settingService: settings, httpUpstream: &cookieTestUpstream{t: t, cookie: cookie}}
	account := &Account{ID: 1, Name: "collector", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}, Extra: map[string]any{"session_id": "old-session", openAICodexCookieExtraKey: "old=cookie"}}
	svc.probeOpenAICodexTicket(context.Background(), account, cfg, &OpenAICookieSettings{})
	library, err := settings.GetOpenAICodexCookieLibrary(context.Background())
	require.NoError(t, err)
	require.Len(t, library, 1)
	require.Equal(t, cookie, library[0].Cookie)
	require.Equal(t, "old-session", account.Extra["session_id"])
	require.Equal(t, "old=cookie", account.Extra[openAICodexCookieExtraKey])
	require.Empty(t, OpenAICodexTicketHistories(account))
	logs, err := settings.GetOpenAICookieLogs(context.Background())
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.True(t, logs[0].Success)
	require.Contains(t, logs[0].Response, "upstream-session")
}

func TestCookieHarvestUnauthorizedSynchronizesAccountError(t *testing.T) {
	repo := &cookieHarvestAccountStateRepo{}
	account := &Account{ID: 21, Status: StatusActive, Schedulable: true}

	markOpenAICookieHarvestUnauthorized(context.Background(), repo, account, []byte(`{"error":"invalid cookie"}`))

	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, int64(21), repo.accountID)
	require.Equal(t, StatusError, account.Status)
	require.False(t, account.Schedulable)
	require.Equal(t, `Authentication failed (401): {"error":"invalid cookie"}`, account.ErrorMessage)
	require.Equal(t, account.ErrorMessage, repo.errorMessage)
	require.Contains(t, repo.errorMessage, "invalid cookie")
}

func TestCookiePersistenceSurvivesExpiredProbeContext(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"host": "slow.example", "exp": time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	cookie := "__oailb=e30." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	repo := &cookieTestRepo{values: map[string]string{}}
	settings := &SettingService{settingRepo: repo}
	cfg := OpenAICodexTicketConfigDefaults(config.OpenAICodexTicketConfig{})
	cfg.CookieHarvestProxyURLs = []string{"http://proxy.example:8080"}
	svc := &OpenAIGatewayService{settingService: settings, httpUpstream: &cookieDeadlineUpstream{cookie: cookie}}
	account := &Account{ID: 1, Name: "collector", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}}
	probeCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	svc.probeOpenAICodexTicket(probeCtx, account, cfg, &OpenAICookieSettings{})

	library, err := settings.GetOpenAICodexCookieLibrary(context.Background())
	require.NoError(t, err)
	require.Len(t, library, 1)
	require.Equal(t, "slow.example", library[0].Host)
	logs, err := settings.GetOpenAICookieLogs(context.Background())
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.True(t, logs[0].Success)
}

func TestCookieSchedulerLogsDeduplicateConsecutiveMessages(t *testing.T) {
	repo := &cookieTestRepo{values: map[string]string{}}
	settings := &SettingService{settingRepo: repo}
	item := OpenAICookieAcquisitionLog{ID: "first", Kind: "scheduler", CreatedAt: time.Now(), Message: "no matching accounts"}
	require.NoError(t, settings.appendOpenAICookieLog(context.Background(), item))
	item.ID = "second"
	require.NoError(t, settings.appendOpenAICookieLog(context.Background(), item))

	logs, err := settings.GetOpenAICookieLogs(context.Background())
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, "first", logs[0].ID)
}

func TestCookieWSStatusCountsLiveConnections(t *testing.T) {
	pool := &openAIWSConnPool{}
	svc := &OpenAIGatewayService{openaiWSPool: pool}
	ap := pool.getOrCreateAccountPool(1)
	idle := newOpenAIWSConn("idle", 1, nil, nil)
	busy := newOpenAIWSConn("busy", 1, nil, nil)
	require.True(t, busy.tryAcquire())
	closed := newOpenAIWSConn("closed", 1, nil, nil)
	closed.close()
	ap.conns = map[string]*openAIWSConn{"idle": idle, "busy": busy, "closed": closed}
	ap.creating = 2
	require.Equal(t, &OpenAIWSAccountStatus{Total: 2, Idle: 1, InUse: 1, Connecting: 2}, svc.OpenAIWSAccountStatus(1))
	require.Equal(t, &OpenAIWSAccountStatus{}, svc.OpenAIWSAccountStatus(2))
}

func TestCookieHostCooldownsAreAccountHostScoped(t *testing.T) {
	now := time.Now()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		openAICodexCookieCooldownsExtraKey: map[string]string{
			"host-a.example": now.Add(time.Hour).Format(time.RFC3339Nano),
			"host-b.example": now.Add(2 * time.Hour).Format(time.RFC3339Nano),
		},
	}}

	require.WithinDuration(t, now.Add(time.Hour), openAICodexCookieHostCooldownUntil(account, "HOST-A.EXAMPLE"), time.Second)
	require.WithinDuration(t, now.Add(2*time.Hour), openAICodexCookieHostCooldownUntil(account, "host-b.example"), time.Second)
	require.True(t, openAICodexCookieHostCooldownUntil(account, "host-c.example").IsZero())

	status := (&SettingService{}).OpenAICookieBinding(context.Background(), account)
	require.Len(t, status.Cooldowns, 2)
	require.Contains(t, status.Cooldowns, "host-a.example")
	require.Contains(t, status.Cooldowns, "host-b.example")
}

func TestActiveCookieWSHostIsNotReportedAsCooldown(t *testing.T) {
	now := time.Now()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		"codex_cookie_ws_host":             "active.example",
		"codex_cookie_ws_started_at":       now.Format(time.RFC3339Nano),
		"codex_cookie_host_cooldown_until": now.Add(4 * time.Hour).Format(time.RFC3339Nano),
	}}

	require.True(t, openAICodexCookieHostCooldownUntil(account, "active.example").IsZero())
	status := (&SettingService{}).OpenAICookieBinding(context.Background(), account)
	require.Empty(t, status.Cooldowns)
}

func TestCookieValidationLogsPageKeepsAttemptTimelineTogether(t *testing.T) {
	ctx := context.Background()
	logs := []OpenAICookieAcquisitionLog{
		{ID: "a3", AttemptID: "attempt-a", AccountID: 1, Stage: "ws_build_succeeded"},
		{ID: "a2", AttemptID: "attempt-a", AccountID: 1, Stage: "validation_succeeded"},
		{ID: "a1", AttemptID: "attempt-a", AccountID: 1, Stage: "host_selected"},
		{ID: "b2", AttemptID: "attempt-b", AccountID: 1, Stage: "validation_rejected"},
		{ID: "b1", AttemptID: "attempt-b", AccountID: 1, Stage: "host_selected"},
		{ID: "other", AttemptID: "attempt-other", AccountID: 2, Stage: "host_selected"},
	}
	raw, err := json.Marshal(logs)
	require.NoError(t, err)
	settings := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{"openai_cookie_validation_logs": string(raw)}}}

	first, total, err := settings.GetOpenAICookieValidationLogsPage(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, first, 3)
	for _, item := range first {
		require.Equal(t, "attempt-a", item.AttemptID)
	}

	second, total, err := settings.GetOpenAICookieValidationLogsPage(ctx, 1, 2, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, second, 2)
	for _, item := range second {
		require.Equal(t, "attempt-b", item.AttemptID)
	}
}

func TestCookieValidationDecisionUsesAssistantOutputOnly(t *testing.T) {
	require.Equal(t, "yes", openAICodexCookieValidationDecision([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"yes\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n")))
	require.Equal(t, "no", openAICodexCookieValidationDecision([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"no\"}]}]}}\n")))
	// A yes/no in metadata or the echoed prompt is not an assistant answer.
	require.Empty(t, openAICodexCookieValidationDecision([]byte("data: {\"type\":\"response.completed\",\"response\":{},\"metadata\":{\"note\":\"yes\"}}\n")))
	require.Empty(t, openAICodexCookieValidationDecision([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"yes, definitely\"}\n")))
}

func TestCookieValidationDecisionDoesNotDuplicateStreamSnapshots(t *testing.T) {
	for _, answer := range []string{"yes", "no"} {
		t.Run(answer, func(t *testing.T) {
			final := fmt.Sprintf(`{"output":[{"type":"message","content":[{"type":"output_text","text":%q}]}]}`, answer)
			stream := fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\ndata: {\"type\":\"response.output_text.done\",\"text\":%q}\n\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\ndata: {\"type\":\"response.done\",\"response\":%s}\n\n", answer, answer, final, final)
			require.Equal(t, answer, openAICodexCookieValidationDecision([]byte(stream)))
		})
	}
}

func TestCookieValidationDecisionRejectsFailedStream(t *testing.T) {
	for _, terminal := range []string{"error", "response.failed", "response.incomplete"} {
		stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"yes\"}\n\ndata: {\"type\":\"" + terminal + "\"}\n\n"
		require.Empty(t, openAICodexCookieValidationDecision([]byte(stream)))
	}
	// Genuine repeated output must not be mistaken for a repeated snapshot.
	require.Empty(t, openAICodexCookieValidationDecision([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"yes\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"yes\"}\n")))
	// Preserve whitespace in chunks instead of turning 'y es' into 'yes'.
	require.Empty(t, openAICodexCookieValidationDecision([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"y \"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"es\"}\n")))
}
