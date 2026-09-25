package service

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestFreshHarvestDoesNotReuseOldCookies(t *testing.T) {
	account := ticketTestAccount(41)
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, &codexTicketFuncUpstream{do: func(req *http.Request) (*http.Response, error) {
		require.Empty(t, req.Header.Get("Cookie"))
		require.Empty(t, req.Header.Get(openAICodexTurnStateHeader))
		require.Equal(t, "true", req.Header.Get(responsesLiteHeaderKey))
		data, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, "additional_tools", gjson.GetBytes(data, "input.0.type").String())
		require.False(t, gjson.GetBytes(data, "store").Bool())
		require.False(t, gjson.GetBytes(data, "parallel_tool_calls").Bool())
		return codexTicketResponse(), nil
	}})
	require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292, CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), HarvestCookies: []string{"__cflb=old", "__oailb=old"}}))
	result := svc.requestCodexHarvestProbe(context.Background(), account, "dummy", "gpt-6-astra", "", nil, "fresh")
	require.NoError(t, result.Err)
}

func TestProbeRejectsReportedModelMismatch(t *testing.T) {
	require.Error(t, validateCodexProbeResponse([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-5.6-luna\"}}\n\n"), "gpt-6-astra"))
	require.NoError(t, validateCodexProbeResponse([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}\n\n"), "gpt-6-astra"))
}

func TestLiteTicketRestoresOnlyItsOwnCookieBundle(t *testing.T) {
	h := http.Header{"Cookie": []string{"foreign=secret"}}
	ticket := &openAICodexTicket{Model: "gpt-6-astra", HarvestLite: true, HarvestCookies: []string{"__cflb=new", "__oailb=new"}, HarvestCookiesAt: time.Now()}
	restoreBoundCodexTicketHarvestIdentity(h, ticket)
	require.Equal(t, "__cflb=new; __oailb=new", h.Get("Cookie"))
	require.Equal(t, "true", h.Get(responsesLiteHeaderKey))
	ticket.HarvestLite = false
	restoreBoundCodexTicketHarvestIdentity(h, ticket)
	require.Empty(t, h.Get(responsesLiteHeaderKey))
}
