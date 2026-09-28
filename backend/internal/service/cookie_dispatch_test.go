package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookieDispatchUsesCurrentBindingAfterQueue(t *testing.T) {
	ctx := context.Background()
	old := cookieGuardTestAccount("old.example", time.Now().Add(time.Minute))
	current := cookieGuardTestAccount("new.example", time.Now().Add(time.Minute))
	settings := cookieGuardTestSettings(t)
	_, err := settings.SetOpenAICodexCookieLibrary(ctx, []OpenAICodexCookieLibraryEntry{{Host: "new.example", Cookie: "__oailb=new", ExpiresAt: time.Now().Add(time.Hour)}})
	require.NoError(t, err)
	repo := &schedulerTestOpenAIAccountRepo{accounts: []Account{current}}
	svc := &OpenAIGatewayService{settingService: settings, accountRepo: repo}
	got, err := svc.refreshCookieDispatchAccount(ctx, &old)
	require.NoError(t, err)
	require.Equal(t, "new.example", openAICodexCookieHostFromAccount(got))
	require.Equal(t, "old.example", openAICodexCookieHostFromAccount(&old))
	// Existing WS handshakes must not carry another turn after the transition.
	require.Error(t, svc.checkCookieDispatchTurn(ctx, &old))
	require.NoError(t, svc.checkCookieDispatchTurn(ctx, got))
	got.Extra["request_local"] = true
	require.NotContains(t, repo.accounts[0].Extra, "request_local")
	// An old request cannot erase the newer binding when its Cookie disappears.
	require.Empty(t, svc.openAICodexCookieForAccount(&old))
	require.Equal(t, "new.example", openAICodexCookieHostFromAccount(&repo.accounts[0]))
}

func TestCookieDispatchRejectsInvalidCurrentRoute(t *testing.T) {
	for _, state := range []string{"expired", "unbound", "disabled", "missing_cookie", "deleted"} {
		t.Run(state, func(t *testing.T) {
			old := cookieGuardTestAccount("old.example", time.Now().Add(time.Minute))
			current := cookieGuardTestAccount("new.example", time.Now().Add(time.Minute))
			switch state {
			case "expired":
				current.Extra["codex_cookie_host_binding_expires_at"] = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
			case "unbound":
				delete(current.Extra, openAICodexCookieHostExtraKey)
			case "disabled":
				current.Schedulable = false
			}
			repo := &schedulerTestOpenAIAccountRepo{accounts: []Account{current}}
			if state == "deleted" {
				repo.accounts = nil
			}
			svc := &OpenAIGatewayService{settingService: cookieGuardTestSettings(t), accountRepo: repo}
			got, err := svc.refreshCookieDispatchAccount(context.Background(), &old)
			require.Nil(t, got)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.RequestScopedTransient)
		})
	}
}

func TestCookieDispatchRecoveryCannotRemainInDegradedGroup(t *testing.T) {
	settings := cookieDegradedTestSettings()
	old := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
	old.openaiCookieDegraded = true
	current := cookieGuardTestAccount("new.example", time.Now().Add(time.Minute))
	svc := &OpenAIGatewayService{settingService: settings, accountRepo: &schedulerTestOpenAIAccountRepo{accounts: []Account{current}}}
	group := int64(3)
	_, err := svc.refreshCookieDispatchAccount(withCookieSchedulingGroup(context.Background(), &group), &old)
	require.Error(t, err)
}

func TestCookieDispatchFinalSelectionIgnoresStaleCache(t *testing.T) {
	old := cookieGuardTestAccount("old.example", time.Now().Add(time.Minute))
	current := cookieGuardTestAccount("new.example", time.Now().Add(time.Minute))
	svc := &OpenAIGatewayService{settingService: cookieGuardTestSettings(t), accountRepo: &schedulerTestOpenAIAccountRepo{accounts: []Account{current}}}
	selection, err := svc.newSelectionResult(context.Background(), &old, false, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "new.example", openAICodexCookieHostFromAccount(selection.Account))
	// A cache entry that predates the first binding must recover just as fast.
	delete(old.Extra, openAICodexCookieHostExtraKey)
	selection, err = svc.newSelectionResult(context.Background(), &old, false, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "new.example", openAICodexCookieHostFromAccount(selection.Account))
	// The wire error must contain valid structured JSON without credentials.
	require.True(t, json.Valid(cookieDispatchUnavailable().ResponseBody))
}

func TestCookieDispatchValidationCandidateKeepsItsOwnHost(t *testing.T) {
	// Validation probes deliberately target an unbound candidate, and must not
	// be redirected back to the production binding by the dispatch fence.
	candidate := cookieGuardTestAccount("candidate.example", time.Now().Add(time.Minute))
	candidate.openaiCookieValidationCookie = "__oailb=probe"
	svc := &OpenAIGatewayService{settingService: cookieGuardTestSettings(t)}
	got, err := svc.refreshCookieDispatchAccount(context.Background(), &candidate)
	require.NoError(t, err)
	require.Same(t, &candidate, got)
}
