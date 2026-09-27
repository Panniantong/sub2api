package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAICodexCookieValidationRequestUsesResponsesLiteReasoningContext(t *testing.T) {
	body, err := openAICodexCookieValidationRequestBody("gpt-6-astra")
	require.NoError(t, err)

	var request map[string]any
	require.NoError(t, json.Unmarshal(body, &request))
	reasoning, ok := request["reasoning"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "all_turns", reasoning["context"])
	require.Equal(t, "Answer exactly yes or no.", request["instructions"])
}

func TestOpenAICodexCookieCaptureAndOverride(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		OpenAICodexTicket: config.OpenAICodexTicketConfig{Enabled: true},
	}}}
	account := &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{openAICodexCookieExtraKey: "old=keep; replace=before"},
	}
	ticket := "gAAAAA" + strings.Repeat("x", 774)
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		openAICodexTurnStateHeader: []string{ticket},
		"Set-Cookie": []string{
			"replace=after; Path=/; HttpOnly; Secure",
			"new=value; Path=/; SameSite=Lax",
		},
	}}

	captured := svc.captureOpenAICodexCookie(account, response, ticket)
	require.Equal(t, "new=value; replace=after", captured)

	headers := http.Header{"Cookie": []string{"client=discard"}}
	headers["cookie"] = []string{"lowercase=discard"}
	svc.applyOpenAICodexCookie(account, headers)
	require.Equal(t, "client=discard; lowercase=discard; new=value; replace=after", headers.Get("Cookie"))
	require.Len(t, headers.Values("Cookie"), 1)
	for key := range headers {
		require.Equal(t, "Cookie", key, "cookie header must be emitted once with canonical HTTP casing")
	}
}

func TestOpenAICodexCookieHostWhitelist(t *testing.T) {
	cookie := "__oailb=eyJhbGciOiJub25lIn0.eyJob3N0IjoiY2hhdC5nYXRld2F5LnVuaWZpZWQtODQuYXBpLm9wZW5haS5jb20ifQ.signature"
	require.True(t, openAICodexCookieHostAllowed(cookie, nil))
	require.True(t, openAICodexCookieHostAllowed(cookie, []string{"CHAT.GATEWAY.UNIFIED-84.API.OPENAI.COM"}))
	require.False(t, openAICodexCookieHostAllowed(cookie, []string{"chat.gateway.unified-189.api.openai.com"}))
	require.False(t, openAICodexCookieHostAllowed("__oailb=invalid", []string{"chat.gateway.unified-84.api.openai.com"}))
}

func TestMergeOpenAICodexCookieHeadersOverlaysBoundKeysOnly(t *testing.T) {
	require.Equal(t,
		"__cf_bm=old; extra=keep; new=added",
		mergeOpenAICodexCookieHeaders("__cf_bm=old; extra=keep", "__cf_bm=bound; new=added"),
	)
	require.Equal(t, "new=added", mergeOpenAICodexCookieHeaders("", "__cf_bm=bound; new=added"))
}

func TestOpenAICodexHistoryUsesOnlyResponseCookieAndDeduplicatesFailures(t *testing.T) {
	now := time.Now()
	account := &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			openAICodexCookieExtraKey: "bound=must-not-be-copied",
			openAICodexTicketHistoryExtraKey: []OpenAICodexTicketHistory{
				{Cookie: "bound=must-not-be-copied", Source: "harvest", StatusCode: http.StatusForbidden, CapturedAt: now},
				{Cookie: "bound=must-not-be-copied", Source: "harvest", StatusCode: http.StatusForbidden, CapturedAt: now.Add(time.Second)},
				{Ticket: "gAAAAA" + strings.Repeat("x", 350), Cookie: "bound=must-not-be-copied", Source: "harvest", StatusCode: http.StatusOK, CapturedAt: now.Add(2 * time.Second)},
				{Ticket: "gAAAAA" + strings.Repeat("x", 774), Cookie: "response=fresh", Source: "harvest", StatusCode: http.StatusOK, CapturedAt: now.Add(3 * time.Second)},
			},
		},
	}

	history := OpenAICodexTicketHistories(account)
	require.Len(t, history, 3)
	require.Equal(t, "response=fresh", history[0].Cookie)
	require.Empty(t, history[1].Cookie)
	require.Equal(t, 356, len(history[1].Ticket))
	require.Empty(t, history[2].Cookie)
	require.Equal(t, http.StatusForbidden, history[2].StatusCode)
}

func TestOpenAICodexCookieRequiresTicketResponse(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{}}
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"Set-Cookie": []string{"ignored=value; Path=/"},
	}}

	require.Empty(t, svc.captureOpenAICodexCookie(account, response, ""))
	require.Empty(t, openAICodexCookieFromAccount(account))
	require.Empty(t, svc.captureOpenAICodexCookie(account, response, "gAAAAA"+strings.Repeat("x", 350)))
	require.Empty(t, openAICodexCookieFromAccount(account))
}

func TestOpenAICodexTicketOnlyLength780IsReady(t *testing.T) {
	now := time.Now()
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{openAICodexTicketExtraKey: &openAICodexTicket{
			State:     "gAAAAA" + strings.Repeat("x", 350),
			ExpiresAt: now.Add(time.Hour),
		}},
	}

	statuses := OpenAICodexTicketStatuses(account, config.OpenAICodexTicketConfig{}, now)
	require.Len(t, statuses, 1)
	require.False(t, statuses[0].Ready)
	require.Equal(t, 356, statuses[0].Length)

	account.Extra[openAICodexTicketExtraKey] = &openAICodexTicket{
		State:     "gAAAAA" + strings.Repeat("x", 774),
		ExpiresAt: now.Add(time.Hour),
	}
	statuses = OpenAICodexTicketStatuses(account, config.OpenAICodexTicketConfig{}, now)
	require.True(t, statuses[0].Ready)
	require.Equal(t, 780, statuses[0].Length)
}

func TestStoreOpenAICodexTicketDoesNotReplaceValidTicketWithWrongLength(t *testing.T) {
	now := time.Now()
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		OpenAICodexTicket: config.OpenAICodexTicketConfig{Enabled: true, TTLSeconds: 300},
	}}}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{}}
	valid := "gAAAAA" + strings.Repeat("x", 774)
	wrongLength := "gAAAAA" + strings.Repeat("y", 350)

	require.NotNil(t, svc.storeOpenAICodexTicket(account, valid, now, svc.codexTicketConfig()))
	require.Nil(t, svc.storeOpenAICodexTicket(account, wrongLength, now.Add(time.Second), svc.codexTicketConfig()))
	require.Equal(t, valid, svc.lookupOpenAICodexTicket(account).State)
}

func TestReadOpenAICodexSessionIDFromSSE(t *testing.T) {
	body := strings.NewReader("event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"session_id\":\"response-session\"}}\n\n")
	require.Equal(t, "response-session", readOpenAICodexSessionID(body))
}

func TestApplyOpenAIAccountSessionIDOnlyUsesHarvestedBinding(t *testing.T) {
	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra:       map[string]any{},
	}
	headers := make(http.Header)
	headers.Set("session_id", "client-session")
	headers.Set("session-id", "stale-session")
	headers.Set("Cookie", "client=cookie")

	applyOpenAIAccountSessionID(account, headers)
	require.Empty(t, account.GetOpenAISessionID())
	require.Empty(t, headers.Get("session_id"))
	require.Empty(t, headers.Get("session-id"))

	account.Extra[openAICodexSessionIDExtraKey] = "harvested-session"
	applyOpenAIAccountSessionID(account, headers)
	require.Equal(t, "harvested-session", headers.Get("session_id"))
	require.Equal(t, "harvested-session", headers.Get("session-id"))
}

func TestApplyOpenAIAccountSessionIDGeneratesStableBindingWhenMissing(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{
		ID:          42,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra:       map[string]any{},
	}
	first := make(http.Header)
	applyOpenAIAccountSessionID(account, first, svc)
	second := make(http.Header)
	applyOpenAIAccountSessionID(account, second, svc)
	require.NotEmpty(t, first.Get("session_id"))
	require.Equal(t, first.Get("session_id"), first.Get("session-id"))
	require.Equal(t, first.Get("session_id"), second.Get("session_id"))
	require.Equal(t, first.Get("session_id"), account.GetOpenAISessionID())
}
