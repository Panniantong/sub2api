package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRelayCookieHeaderOnlyUsesRouteCookies(t *testing.T) {
	got := relayCookieHeader(map[string]string{
		"__cflb":  "cf-value",
		"__oailb": "oa-value",
		"__cf_bm": "request-value",
	})
	require.Equal(t, "__cflb=cf-value; __oailb=oa-value", got)
}

func TestMintOpenAICodexViaRelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "relay-secret", r.Header.Get("X-Relay-Key"))
		require.Equal(t, "1", r.Header.Get("X-Relay-Mint"))
		require.Equal(t, "gpt-6-astra", r.Header.Get("X-Mint-Model"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"turn_state":"gAAAAA` + strings.Repeat("a", 774) + `","cookie_header":"__cflb=cf; __oailb=oa"}`))
	}))
	defer server.Close()

	result, err := (&OpenAIGatewayService{}).mintOpenAICodexViaRelay(context.Background(), nil, "token", "gpt-6-astra", []byte(`{}`), config.OpenAICodexTicketConfig{
		RelayEnabled:        true,
		RelayAllowMint:      true,
		RelayMode:           "mint",
		RelayURL:            server.URL,
		RelayKey:            "relay-secret",
		RelayTimeoutSeconds: 5,
	})
	require.NoError(t, err)
	require.Equal(t, 780, len(result.Ticket))
	require.Equal(t, "__cflb=cf; __oailb=oa", result.Cookie)
}
