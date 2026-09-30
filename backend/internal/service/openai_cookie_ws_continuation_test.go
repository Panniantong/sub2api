package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cookieWSTerminalObserver struct {
	gin.ResponseWriter
	onTerminal func()
}

func (w *cookieWSTerminalObserver) Write(data []byte) (int, error) {
	if strings.Contains(string(data), `"type":"response.completed"`) {
		w.onTerminal()
	}
	return w.ResponseWriter.Write(data)
}

func TestCookieWSHTTPContinuationReusesFixedConnection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	settings := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{}}}
	value, err := settings.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	value.WSEnabled, value.WSConnections = true, 1
	require.NoError(t, settings.SetOpenAICookieSettings(ctx, value))
	_, err = settings.SetOpenAICodexCookieLibrary(ctx, []OpenAICodexCookieLibraryEntry{{Host: "host.example", Cookie: "bound=value", ExpiresAt: time.Now().Add(time.Hour)}})
	require.NoError(t, err)
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	account := &Account{ID: 1, Concurrency: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": "test", "chatgpt_account_id": "test"},
		Extra:       map[string]any{openAICodexCookieHostExtraKey: "host.example", "session_id": "test-session"}}
	conn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_probe"}}`),
	}}
	dialer := &openAIWSCaptureDialer{conn: conn}
	pool := newOpenAIWSConnPool(cfg, settings)
	defer pool.Close()
	pool.setClientDialerForTest(dialer)
	svc := &OpenAIGatewayService{cfg: cfg, settingService: settings, accountRepo: &cookieWSAccountRepo{account: account}, httpUpstream: cookieWSProbeHTTPUpstream{},
		openaiWSPool: pool, toolCorrector: NewCodexToolCorrector()}
	require.True(t, svc.SupportsOpenAIHTTPContinuation(account))
	require.NoError(t, svc.ScheduleCookieWSBinding(ctx, account))
	require.Eventually(t, func() bool { return svc.OpenAIWSAccountStatus(1).Total == 1 }, time.Second, time.Millisecond)
	conn.mu.Lock()
	require.NotEmpty(t, conn.writes, "the WS must be initialized before it enters the pool")
	warmupWrite := cloneMapStringAny(conn.writes[0])
	conn.mu.Unlock()
	require.Equal(t, "response.create", warmupWrite["type"])
	require.NotContains(t, warmupWrite, "previous_response_id", "an HTTP probe response ID is not valid on a new WS connection")
	require.Equal(t, true, warmupWrite["stream"])
	require.Equal(t, false, warmupWrite["store"])
	dialer.mu.Lock()
	require.NotEmpty(t, dialer.lastHeaders.Get("session_id"), "background WS dials need an upstream session")
	dialer.mu.Unlock()
	var previous, connectionID string
	for turn := 1; turn <= 3; turn++ {
		id := fmt.Sprintf("resp_turn_%d", turn)
		conn.mu.Lock()
		conn.events = [][]byte{
			[]byte(`{"type":"codex.response.metadata","id":"metadata_not_response_id"}`),
			[]byte(fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"status":"in_progress"}}`, id)),
			[]byte(`{"type":"response.output_text.delta","delta":"pong"}`),
			[]byte(fmt.Sprintf("{\n\"type\":\"response.completed\",\n\"response\":{\"id\":%q,\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}\n}", id)),
		}
		conn.mu.Unlock()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set("session_id", "same-client-session")
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
		SetOpenAIHTTPResponseOwner(c, 42, 7)
		c.Writer = &cookieWSTerminalObserver{ResponseWriter: c.Writer, onTerminal: func() {
			owned, bindErr := svc.ValidateOpenAIHTTPResponseOwner(ctx, 0, id, 42, 7)
			require.NoError(t, bindErr)
			require.True(t, owned, "ownership must be ready before the client receives completion")
			_, bound := svc.getOpenAIWSStateStore().GetResponseConn(id)
			require.True(t, bound, "connection affinity must be ready before completion")
		}}
		// Downstream IDs are deliberately unrelated, including a message ID.
		// The selected socket, not this field, must supply the continuation.
		prevField := `,"previous_response_id":"msg_untrusted_client_value"`
		if turn == 2 {
			prevField = "" // A tool continuation also works without a client ID.
		}
		input := `[{"role":"user","content":"ping"}]`
		if turn == 2 {
			input = `[{"type":"custom_tool_call_output","call_id":"call_native_custom","output":"pong"}]`
		} else if turn == 3 {
			input = `[{"type":"function_call_output","call_id":"call_native_function","output":"pong"}]`
		}
		body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","stream":true,"store":false,"input":%s%s}`, input, prevField))
		result, err := svc.Forward(ctx, c, account, body)
		require.NoError(t, err, rec.Body.String())
		require.True(t, result.OpenAIWSMode)
		require.Equal(t, id, result.ResponseID)
		require.Contains(t, rec.Body.String(), "response.completed")
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			if strings.HasPrefix(line, "data: ") {
				require.True(t, json.Valid([]byte(strings.TrimPrefix(line, "data: "))), "every SSE event must contain complete JSON: %s", line)
			}
		}
		if turn > 1 {
			var original []map[string]any
			require.NoError(t, json.Unmarshal([]byte(input), &original))
			sent := conn.lastWrite["input"].([]any)[0].(map[string]any)
			require.Equal(t, original[0]["call_id"], sent["call_id"], "tool output must reference the original upstream call ID")
		}
		owned, err := svc.ValidateOpenAIHTTPResponseOwner(ctx, 0, id, 42, 7)
		require.NoError(t, err)
		require.True(t, owned, "next HTTP turn must pass ownership validation")
		owned, err = svc.ValidateOpenAIHTTPResponseOwner(ctx, 0, id, 99, 8)
		require.NoError(t, err)
		require.False(t, owned, "other users must not inherit the conversation")
		boundConn, ok := svc.getOpenAIWSStateStore().GetResponseConn(id)
		require.True(t, ok)
		if connectionID != "" {
			require.Equal(t, connectionID, boundConn)
			require.Equal(t, previous, conn.lastWrite["previous_response_id"])
		}
		connectionID, previous = boundConn, id
		require.Equal(t, 1, dialer.DialCount(), "all turns must use the original fixed pool")
		require.Equal(t, 1, svc.OpenAIWSAccountStatus(1).Idle)
	}
	// Removing the binding restores the native OAuth HTTP continuation guard.
	delete(account.Extra, openAICodexCookieHostExtraKey)
	require.False(t, svc.SupportsOpenAIHTTPContinuation(account))
}

func TestForwardOpenAIWSV2_EmitsErrorAfterPartialStream(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			cfg := newOpenAIWSV2TestConfig()
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			conn := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.output_text.delta","delta":"started"}`)}}
			if malformed {
				conn.events = append(conn.events, []byte(`{"type":`))
			}
			pool := newOpenAIWSConnPool(cfg)
			defer pool.Close()
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: conn})
			svc := &OpenAIGatewayService{cfg: cfg, openaiWSPool: pool, toolCorrector: NewCodexToolCorrector()}
			account := &Account{ID: 3, Concurrency: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6-astra","stream":true,"input":"ping"}`))
			require.Error(t, err)
			require.Contains(t, rec.Body.String(), `"code":"upstream_stream_incomplete"`)
			require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"error"`))
		})
	}
}
