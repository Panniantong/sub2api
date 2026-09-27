package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type cookieWSProbeHTTPUpstream struct{}

func (cookieWSProbeHTTPUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_probe\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_probe\"}}\n\n")),
	}, nil
}

func (cookieWSProbeHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return cookieWSProbeHTTPUpstream{}.Do(req, proxyURL, accountID, concurrency)
}

type cookieWSAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *cookieWSAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	copy := *r.account
	return &copy, nil
}

func TestCookieWSBindingBuildsOnceAndNeverReplenishes(t *testing.T) {
	ctx := context.Background()
	repo := &cookieTestRepo{values: map[string]string{}}
	settings := &SettingService{settingRepo: repo}
	settingsValue, err := settings.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	settingsValue.WSEnabled, settingsValue.WSConnections = true, 3
	require.NoError(t, settings.SetOpenAICookieSettings(ctx, settingsValue))
	_, err = settings.SetOpenAICodexCookieLibrary(ctx, []OpenAICodexCookieLibraryEntry{{Host: "host.example", Cookie: "bound=value", ExpiresAt: time.Now().Add(time.Hour)}})
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled, cfg.Gateway.OpenAIWS.OAuthEnabled, cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true, true, true
	account := &Account{ID: 1, Concurrency: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test", "chatgpt_account_id": "test"}, Extra: map[string]any{openAICodexCookieHostExtraKey: "host.example", "session_id": "test-session"}}
	pool := newOpenAIWSConnPool(cfg, settings)
	defer pool.Close()
	dialer := &openAIWSCountingDialer{}
	pool.setClientDialerForTest(dialer)
	svc := &OpenAIGatewayService{cfg: cfg, settingService: settings, accountRepo: &cookieWSAccountRepo{account: account}, openaiWSPool: pool, httpUpstream: cookieWSProbeHTTPUpstream{}}
	require.NoError(t, svc.ScheduleCookieWSBinding(ctx, account))
	require.Eventually(t, func() bool { return svc.OpenAIWSAccountStatus(1).Connecting == 0 }, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, 3, svc.OpenAIWSAccountStatus(1).Total, svc.OpenAIWSAccountStatus(1).LastError)
	require.Equal(t, 3, dialer.DialCount())
	req := openAIWSAcquireRequest{Account: account, WSURL: "wss://example.test", ForceNewConn: true}
	missingCtx, cancelMissing := context.WithTimeout(ctx, time.Second)
	_, err = pool.Acquire(missingCtx, openAIWSAcquireRequest{Account: account, ForcePreferredConn: true})
	cancelMissing()
	require.ErrorIs(t, err, errOpenAIWSPreferredConnUnavailable, "missing continuation affinity must fail immediately, not wait on idle connections")
	lease, err := pool.Acquire(ctx, req)
	require.NoError(t, err)
	connID := lease.ConnID()
	lease.Release()
	req.PreferredConnID, req.ForcePreferredConn = connID, true
	again, err := pool.Acquire(ctx, req)
	require.NoError(t, err)
	require.Equal(t, connID, again.ConnID())
	again.MarkBroken()
	again.Release()
	_, err = pool.Acquire(ctx, req)
	require.ErrorIs(t, err, errOpenAIWSPreferredConnUnavailable)
	require.NoError(t, svc.ScheduleCookieWSBinding(ctx, account))
	require.Equal(t, 3, dialer.DialCount())
	req.PreferredConnID, req.ForcePreferredConn = "", false
	for i := 0; i < 2; i++ {
		lease, err = pool.Acquire(ctx, req)
		require.NoError(t, err)
		lease.MarkBroken()
		lease.Release()
	}
	_, err = pool.Acquire(ctx, req)
	require.ErrorIs(t, err, errCookieWSPoolUnavailable)
	pool.ensureTargetIdleAsync(1)
	require.Equal(t, 3, dialer.DialCount(), "requests and failures must never replenish the pool")
}

func TestCookieWSIdleRateLimitsDoNotDestroyReusableConnection(t *testing.T) {
	conn := newOpenAIWSConn("test", 1, nil, http.Header{})
	conn.readerLoopResults = make(chan []byte, 1)
	conn.readerLoopResults <- []byte(`{"type":"codex.rate_limits"}`)
	require.True(t, conn.tryAcquire())
	conn.release()
	conn.readerLoopResults <- []byte(`{"type":"response.output_text.delta","delta":"unexpected"}`)
	require.False(t, conn.tryAcquire(), "actual response data must still invalidate a dirty connection")
}
