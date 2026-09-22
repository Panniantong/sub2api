package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketHarvestCookiesFollowFreshnessWindow(t *testing.T) {
	now := time.Now()
	ticket := &openAICodexTicket{CapturedAt: now, HarvestCookies: []string{"__cflb=secret", "__oai=secret2"}}
	require.True(t, codexTicketCookiesFresh(ticket, now.Add(239*time.Second)))
	require.False(t, codexTicketCookiesFresh(ticket, now.Add(240*time.Second)))

	headers := http.Header{}
	restoreBoundCodexTicketHarvestIdentity(headers, &openAICodexTicket{
		HarvestSessionID: "fresh-session",
		CapturedAt:       now,
		HarvestCookies:   ticket.HarvestCookies,
		Model:            "gpt-6-astra",
	})
	require.Equal(t, "__cflb=secret; __oai=secret2", headers.Get("Cookie"))
}

func TestCodexTicketCookiesRefreshOnlyFromAcceptedState(t *testing.T) {
	oldAt := time.Now().Add(-2 * time.Minute)
	account := ticketTestAccount(41)
	state := fakeCodexTicketState(292)

	t.Run("312 does not refresh frozen cookies", func(t *testing.T) {
		svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, nil)
		require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
			Model: "gpt-6-astra", State: state, Length: 292, CapturedAt: oldAt, ExpiresAt: time.Now().Add(time.Hour),
			HarvestCookies: []string{"__cflb=good"}, HarvestCookiesAt: oldAt,
		}))
		req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(openAICodexTurnStateHeader, state)
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}
		resp.Header.Set(openAICodexTurnStateHeader, fakeCodexTicketState(312))
		resp.Header.Add("Set-Cookie", "__cflb=bad; Path=/; Secure")
		svc.observeCodexTicketResponse(req, resp, account)
		got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
		require.Equal(t, []string{"__cflb=good"}, got.HarvestCookies)
		require.Equal(t, oldAt, got.HarvestCookiesAt)
	})

	t.Run("292 refreshes and merges frozen cookies", func(t *testing.T) {
		svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, nil)
		require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
			Model: "gpt-6-astra", State: state, Length: 292, CapturedAt: oldAt, ExpiresAt: time.Now().Add(time.Hour),
			HarvestCookies: []string{"__cflb=old", "stable=keep"}, HarvestCookiesAt: oldAt,
		}))
		req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(openAICodexTurnStateHeader, state)
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}
		resp.Header.Set(openAICodexTurnStateHeader, state)
		resp.Header.Add("Set-Cookie", "__cflb=new; Path=/; Secure")
		svc.observeCodexTicketResponse(req, resp, account)
		got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
		require.Equal(t, []string{"__cflb=new", "stable=keep"}, got.HarvestCookies)
		require.True(t, got.HarvestCookiesAt.After(oldAt))
	})
}
