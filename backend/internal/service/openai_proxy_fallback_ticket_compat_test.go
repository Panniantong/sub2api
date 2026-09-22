//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRuntimeProxyFallbackPreservesTicketResponsePolicy(t *testing.T) {
	for _, mode := range []string{FallbackModeDirect, FallbackModeProxy} {
		for _, strict := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "compatible", true: "strict"}[strict], func(t *testing.T) {
				account := ticketTestAccount(41)
				primary := proxyForTest(1, "primary.invalid", 8080)
				primary.FallbackMode, primary.BackupProxyID = mode, i64(2)
				backup := proxyForTest(2, "backup.invalid", 8080)
				backup.FallbackMode = FallbackModeDirect
				account.Proxy, account.ProxyID = primary, i64(primary.ID)
				state := fakeCodexTicketState(292)
				replacement := state[:len(state)-4] + "BBBB"
				body := &codexTicketHeaderOnlyBody{}
				var calls int
				upstream := &runtimeFallbackUpstream{do: func(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
					calls++
					require.Equal(t, state, req.Header.Get(openAICodexTurnStateHeader), "fallback preserves the bound ticket")
					require.NoError(t, req.Body.Close())
					if calls == 1 {
						require.Equal(t, primary.URL(), proxy)
						httptrace.ContextClientTrace(req.Context()).GetConn("primary.invalid")
						return nil, syscall.ECONNREFUSED
					}
					want := ""
					if mode == FallbackModeProxy {
						want = backup.URL()
					}
					require.Equal(t, want, proxy)
					return &http.Response{StatusCode: http.StatusOK, Body: body,
						Header: http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"malformed"}}}, nil
				}}
				svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, upstream)
				svc.proxyRepo = &fakeProxyLookup{byID: map[int64]*Proxy{backup.ID: backup}}
				values := map[string]string{SettingKeyOpenAICodexTicketStrict: "false"}
				if strict {
					values[SettingKeyOpenAICodexTicketStrict] = "true"
				}
				svc.settingService = NewSettingService(&codexTicketSettingRepo{
					codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: values},
				}, svc.cfg)
				for _, ticketState := range []string{state, replacement} {
					svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
						Model: "gpt-6-astra", State: ticketState, Length: 292,
						CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
					})
				}
				req, err := http.NewRequest(http.MethodPost, "https://example.invalid/responses", strings.NewReader("{}"))
				require.NoError(t, err)
				req.Header.Set(openAICodexTurnStateHeader, state)
				resp, err := svc.doOpenAIUpstream(req, primary.URL(), account)
				require.Equal(t, 2, calls, "a received response must never trigger another fallback")
				require.Zero(t, body.reads, "ticket validation must remain header-only")
				require.Equal(t, replacement, svc.lookupOpenAICodexTicket(account, "gpt-6-astra").State,
					"observe the final fallback response and preserve the standby replacement")
				if strict {
					require.ErrorIs(t, err, ErrCodexTicketResponseRejected)
					require.Nil(t, resp)
					require.Equal(t, 1, body.closes)
				} else {
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.Zero(t, body.closes)
					id, _ := openAIResponseEgressProxyID(account, resp)
					if mode == FallbackModeDirect {
						require.Zero(t, id)
					} else {
						require.Equal(t, backup.ID, id)
					}
					require.NoError(t, resp.Body.Close())
				}
			})
		}
	}
}

func TestRuntimeProxyFallbackDoesNotRedirectTicketHarvest(t *testing.T) {
	account := ticketTestAccount(41)
	primary := proxyForTest(1, "business.invalid", 8080)
	primary.FallbackMode = FallbackModeDirect
	account.Proxy, account.ProxyID = primary, i64(primary.ID)
	calls := 0
	upstream := &runtimeFallbackUpstream{do: func(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
		calls++
		require.Equal(t, "http://harvest.invalid:8080", proxy)
		require.Equal(t, HTTPUpstreamProfileOpenAIHarvest, HTTPUpstreamProfileFromContext(req.Context()))
		require.True(t, req.Close)
		require.NoError(t, req.Body.Close())
		return nil, syscall.ECONNREFUSED
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, upstream)
	_, _, err := svc.fireOpenAICodexTicketProbe(context.Background(), account, "synthetic-token",
		"gpt-6-astra", "http://harvest.invalid:8080", time.Second)
	require.Error(t, err)
	require.Equal(t, 1, calls, "harvesting must not use the business proxy's direct fallback")
}

func TestBoundTicketPinsHarvestEgressAndSkipsFallback(t *testing.T) {
	account := ticketTestAccount(41)
	primary := proxyForTest(1, "primary.invalid", 8080)
	primary.FallbackMode, primary.BackupProxyID = FallbackModeProxy, i64(2)
	backup := proxyForTest(2, "backup.invalid", 8080)
	account.Proxy, account.ProxyID = primary, i64(primary.ID)
	state := fakeCodexTicketState(292)
	var calls int
	var seen []string
	upstream := &runtimeFallbackUpstream{do: func(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
		calls++
		seen = append(seen, proxy)
		require.Equal(t, state, req.Header.Get(openAICodexTurnStateHeader))
		require.NoError(t, req.Body.Close())
		// Pinned tickets skip the fallback tracer. If pin is later removed, this
		// GetConn is what would otherwise authorize a second egress hop.
		if tr := httptrace.ContextClientTrace(req.Context()); tr != nil && tr.GetConn != nil {
			tr.GetConn("primary.invalid")
		}
		return nil, syscall.ECONNREFUSED
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, upstream)
	svc.proxyRepo = &fakeProxyLookup{byID: map[int64]*Proxy{backup.ID: backup}}
	require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		Model: "gpt-6-astra", State: state, Length: 292, HarvestProxyURL: primary.URL(),
		CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}))
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/responses", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set(openAICodexTurnStateHeader, state)
	_, err = svc.doOpenAIUpstream(req, primary.URL(), account)
	require.Error(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, []string{primary.URL()}, seen)
}
