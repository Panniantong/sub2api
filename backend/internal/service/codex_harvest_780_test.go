package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func mint780Pair(exp time.Time, node string) []string {
	claims := fmt.Sprintf(`{"exp":%d,"host":"chat.gateway.%s.api.openai.com"}`, exp.Unix(), node)
	return []string{"__cflb=route", "__oailb=e30." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".sig"}
}
func mint780State(issued time.Time) string {
	raw := make([]byte, 585)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:9], uint64(issued.Unix()))
	return base64.URLEncoding.EncodeToString(raw)
}
func TestCodex780Validation(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	pair := mint780Pair(now.Add(time.Hour), "unified-95")
	_, _, err := codex780Route(pair, "unified-95", now)
	require.NoError(t, err)
	for _, cookies := range [][]string{pair[:1], append(append([]string{}, pair...), pair[0]), mint780Pair(now, "unified-95"), mint780Pair(now.Add(time.Hour), "unified-88")} {
		_, _, err = codex780Route(cookies, "unified-95", now)
		require.Error(t, err)
	}
	r := codexHarvestProbeResult{State: mint780State(now.Add(-20 * time.Second)), Status: 200, Sent: true, Cookies: pair, Gateway: "unified-95"}
	cfg := config.OpenAICodexTicketConfig{TargetLength: 780, TTLSeconds: 3600}
	shape, kind := classifyCodexHarvestProbe(context.Background(), ticketTestAccount(1), cfg, r)
	require.Equal(t, "success", kind)
	r.Shape = shape
	ticket := codexHarvestTicket(ticketTestAccount(1), "gpt-6-astra", r, cfg, 1)
	require.True(t, ticket.valid(now, 780))
	require.False(t, ticket.valid(now.Add(221*time.Second), 780))
	require.True(t, codexTicketCookiesFresh(ticket, now.Add(300*time.Second)))
	require.WithinDuration(t, shape.IssuedAt.Add(240*time.Second), ticket.ExpiresAt, time.Second)
	ticket.Transport = "unknown"
	require.False(t, ticket.valid(now, 780))
}
func TestCodex780CreatedBoundedAndStrict(t *testing.T) {
	good := `{"type":"response.created","response":{"id":"r1","model":"gpt-6-astra"}}`
	require.NoError(t, readCodex780Created(strings.NewReader("data: "+good+"\n\n"), "gpt-6-astra"))
	require.NoError(t, readCodex780Created(strings.NewReader("\ufeffdata: "+good+"\r\r"), "gpt-6-astra"))
	for _, body := range []string{"data: " + good + "\n", "data: " + good + "\n\n", "event: response.completed\ndata: " + good + "\n\n", "data: {\"type\":\"response.failed\"}\n\n", strings.Repeat(":x\n", 9000) + "data: " + good + "\n\n"} {
		model := "gpt-6-astra"
		if body == "data: "+good+"\n\n" {
			model = "gpt-6-sol"
		}
		require.Error(t, readCodex780Created(strings.NewReader(body), model))
	}
}
func TestCodex780NativeProbe(t *testing.T) {
	account := ticketTestAccount(1)
	s := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICodexTicket: config.OpenAICodexTicketConfig{TargetLength: 780}}}}
	pair := mint780Pair(time.Now().Add(time.Hour), "unified-95")
	s.httpUpstream = &harvestProxyUpstream{do: func(req *http.Request, proxy string) (*http.Response, error) {
		require.Equal(t, "socks5://127.0.0.1:1080", proxy)
		require.Equal(t, "session", req.Header.Get("session-id"))
		require.Empty(t, req.Header.Get(responsesLiteHeaderKey))
		body, _ := io.ReadAll(req.Body)
		require.NotContains(t, string(body), "additional_tools")
		h := http.Header{"Set-Cookie": pair}
		h.Set(openAICodexTurnStateHeader, mint780State(time.Now()))
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"model\":\"gpt-6-astra\"}}\n\n"))}, nil
	}}
	out := s.requestCodex780Probe(context.Background(), account, "token", "gpt-6-astra", "socks5://127.0.0.1:1080", func() bool { return true }, "session")
	require.NoError(t, out.Err)
	require.Len(t, out.State, 780)
	require.Len(t, out.Cookies, 2)
}

func TestCodex780ProtocolIsolationAndRouteDeletion(t *testing.T) {
	account := ticketTestAccount(1)
	cfg := config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 780, FailClosed: true, Models: []string{"gpt-6-astra"}}
	s := ticketTestService(t, cfg, nil)
	now := time.Now()
	state := mint780State(now)
	ticket := &openAICodexTicket{AccountID: 1, Model: "gpt-6-astra", State: state, Length: 780, Transport: "sse", Gateway: "unified-95", IssuedAt: now, ExpiresAt: now.Add(240 * time.Second), HarvestCookies: mint780Pair(now.Add(time.Hour), "unified-95"), HarvestSessionID: "mint-session"}
	s.openaiCodexTickets.Store(openAICodexTicketKey(1, "gpt-6-astra"), ticket)
	h := http.Header{}
	require.NoError(t, s.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, state, h.Get(openAICodexTurnStateHeader))
	require.Error(t, s.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", http.Header{}, "websocket"))
	require.Empty(t, harvestTicketSessionID(ticket))
	req, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", nil)
	req.Header = h
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": []string{"__oailb=; Max-Age=0"}}}
	s.observeCodexTicketResponse(req, resp, account)
	require.True(t, s.lookupOpenAICodexTicket(account, "gpt-6-astra").Revoked)
}
