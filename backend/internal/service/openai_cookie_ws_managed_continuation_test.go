package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCookieManagedContinuationFixture(t *testing.T) (*OpenAIGatewayService, *Account, *openAIWSCaptureConn, *openAIWSCaptureDialer) {
	t.Helper()
	settings := &SettingService{settingRepo: &cookieTestRepo{values: map[string]string{}}}
	value, err := settings.GetOpenAICookieSettings(context.Background())
	require.NoError(t, err)
	value.WSEnabled, value.WSConnections = true, 1
	require.NoError(t, settings.SetOpenAICookieSettings(context.Background(), value))
	_, err = settings.SetOpenAICodexCookieLibrary(context.Background(), []OpenAICodexCookieLibraryEntry{{Host: "host.example", Cookie: "bound=value", ExpiresAt: time.Now().Add(time.Hour)}})
	require.NoError(t, err)
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	account := &Account{ID: 1, Concurrency: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": "test", "chatgpt_account_id": "test"},
		Extra:       map[string]any{openAICodexCookieHostExtraKey: "host.example", "session_id": "upstream-session"}}
	conn := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_warmup"}}`)}}
	dialer := &openAIWSCaptureDialer{conn: conn}
	pool := newOpenAIWSConnPool(cfg, settings)
	t.Cleanup(pool.Close)
	pool.setClientDialerForTest(dialer)
	svc := &OpenAIGatewayService{cfg: cfg, settingService: settings, accountRepo: &cookieWSAccountRepo{account: account},
		httpUpstream: cookieWSProbeHTTPUpstream{}, openaiWSPool: pool, toolCorrector: NewCodexToolCorrector()}
	require.NoError(t, svc.ScheduleCookieWSBinding(context.Background(), account))
	require.Eventually(t, func() bool { return svc.OpenAIWSAccountStatus(1).Total == 1 }, time.Second, time.Millisecond)
	return svc, account, conn, dialer
}

func TestCookieWSManagedHTTPContinuationIsolatedBySession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, account, conn, dialer := newCookieManagedContinuationFixture(t)
	tests := []struct {
		session, thread string
		key, group      int64
		wantPrevious    string
	}{
		{"A", "", 1, 1, ""},
		{"A", "", 1, 1, "resp_0"},
		{"B", "", 1, 1, ""},
		{"B", "", 1, 1, "resp_2"},
		{"A", "", 1, 1, ""}, // B replaced A's socket state.
		{"A", "", 1, 1, "resp_4"},
		{"A", "", 2, 1, ""}, // Same ID, different tenant key.
		{"A", "", 2, 2, ""}, // Same key/session, different group.
		{"A", "thread-one", 2, 2, ""},
		{"A", "thread-two", 2, 2, ""},
		{"A", "thread-two", 2, 2, "resp_9"},
		{"", "", 2, 2, ""},
		{"", "", 2, 2, ""}, // Shared cache key is not a session identity.
	}
	for i, tt := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			conn.mu.Lock()
			conn.events = [][]byte{[]byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`, i))}
			conn.mu.Unlock()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("session_id", tt.session)
			c.Request.Header.Set("thread-id", tt.thread)
			c.Set("api_key", &APIKey{ID: tt.key, GroupID: &tt.group})
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6-astra","stream":true,"store":false,"input":"hello","previous_response_id":"resp_from_another_conversation","prompt_cache_key":"shared-cache"}`))
			require.NoError(t, err)
			conn.mu.Lock()
			sent := cloneMapStringAny(conn.lastWrite)
			conn.mu.Unlock()
			if tt.wantPrevious == "" {
				require.NotContains(t, sent, "previous_response_id")
			} else {
				require.Equal(t, tt.wantPrevious, sent["previous_response_id"])
			}
			require.Equal(t, 1, dialer.DialCount(), "session changes must reuse the live socket")
		})
	}
}

func TestCookieWSManagedSessionWaitsForItsBusyConnection(t *testing.T) {
	svc, account, _, _ := newCookieManagedContinuationFixture(t)
	pool := svc.getOpenAIWSConnPool()
	ctx := context.Background()
	first, err := pool.Acquire(ctx, openAIWSAcquireRequest{Account: account, CookieSessionKey: "A"})
	require.NoError(t, err)
	defer first.Release()
	first.conn.rememberCookieContinuation("A", "gpt-6-astra", "resp_A")
	// Leave another healthy socket idle. A second A request must wait rather
	// than acquire it and silently lose its continuation.
	ap, _ := pool.getAccountPool(account.ID)
	other := newOpenAIWSConn("other", account.ID, &openAIWSCaptureConn{}, nil)
	ap.mu.Lock()
	ap.conns[other.id] = other
	ap.cookieBatch.target = 2
	ap.mu.Unlock()
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	_, err = pool.Acquire(waitCtx, openAIWSAcquireRequest{Account: account, CookieSessionKey: "A"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	second, err := pool.Acquire(ctx, openAIWSAcquireRequest{Account: account, CookieSessionKey: "B"})
	require.NoError(t, err)
	require.NotEqual(t, first.ConnID(), second.ConnID())
	second.Release()
	first.Release()
	again, err := pool.Acquire(ctx, openAIWSAcquireRequest{Account: account, CookieSessionKey: "A"})
	require.NoError(t, err)
	require.Equal(t, first.ConnID(), again.ConnID())
	again.MarkBroken()
	again.Release()
	// A lost socket cannot donate its response ID to the remaining socket.
	replacement, err := pool.Acquire(ctx, openAIWSAcquireRequest{Account: account, CookieSessionKey: "A"})
	require.NoError(t, err)
	defer replacement.Release()
	require.Equal(t, other.id, replacement.ConnID())
	payload, err := replacement.conn.cookieContinuationPayload([]byte(`{"previous_response_id":"resp_A","input":"full history"}`), "A")
	require.NoError(t, err)
	require.NotContains(t, string(payload), "previous_response_id")
}

func TestCookieWSManagedHTTPModelSwitchReusesSocket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, account, conn, dialer := newCookieManagedContinuationFixture(t)
	account.Credentials["model_mapping"] = map[string]any{"astra-alias": "gpt-6-astra"}
	steps := []struct{ model, upstream, previous string }{
		{"gpt-6-astra", "gpt-6-astra", ""},
		{"astra-alias", "gpt-6-astra", "resp_model_0"},
		{"gpt-5.4", "gpt-5.4", ""},
		{"gpt-5.4", "gpt-5.4", "resp_model_2"},
		{"gpt-6-astra", "gpt-6-astra", ""},
		{"gpt-6-astra", "gpt-6-astra", "resp_model_4"},
	}
	for i, step := range steps {
		conn.mu.Lock()
		conn.events = [][]byte{[]byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_model_%d","output":[]}}`, i))}
		conn.mu.Unlock()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set("session_id", "model-switch")
		c.Set("api_key", &APIKey{ID: 1})
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
		// A fresh chain must retain the supplied call and result, including their
		// original IDs, so switching models never silently drops tool context.
		body := fmt.Sprintf(`{"model":%q,"stream":true,"input":[{"type":"function_call","call_id":"call_history","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_history","output":"ok"}],"previous_response_id":"resp_client_ignored"}`, step.model)
		_, err := svc.Forward(context.Background(), c, account, []byte(body))
		require.NoError(t, err, "turn %d", i)
		conn.mu.Lock()
		sent := cloneMapStringAny(conn.lastWrite)
		conn.mu.Unlock()
		require.Equal(t, step.upstream, sent["model"], "turn %d", i)
		if step.previous == "" {
			require.NotContains(t, sent, "previous_response_id", "turn %d", i)
		} else {
			require.Equal(t, step.previous, sent["previous_response_id"], "turn %d", i)
		}
		input := sent["input"].([]any)
		require.Len(t, input, 2)
		require.Equal(t, "call_history", input[0].(map[string]any)["call_id"])
		require.Equal(t, "call_history", input[1].(map[string]any)["call_id"])
		require.Equal(t, "ok", input[1].(map[string]any)["output"])
		require.Equal(t, 1, dialer.DialCount(), "model changes must reuse the live socket")
	}
}

func TestCookieWSManagedIngressContinuation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, account, upstream, dialer := newCookieManagedContinuationFixture(t)
	account.Credentials["model_mapping"] = map[string]any{"astra-alias": "gpt-6-astra"}
	serverErrors := make(chan error, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		_, first, err := ws.Read(ctx)
		if err != nil {
			serverErrors <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r
		c.Set("api_key", &APIKey{ID: 1})
		serverErrors <- svc.ProxyResponsesWebSocketFromClient(ctx, c, ws, account, "test", first, nil)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Neither connection declares an identity. Each downstream WS gets its own
	// server identity, while multiple frames on that connection share a chain.
	for session := 0; session < 2; session++ {
		client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		require.NoError(t, err)
		models := []string{"gpt-6-astra", "astra-alias", "gpt-5.4", "gpt-5.4", "gpt-6-astra", "gpt-6-astra"}
		for turn, model := range models {
			upstream.mu.Lock()
			upstream.events = [][]byte{[]byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_ws_%d_%d","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`, session, turn))}
			upstream.mu.Unlock()
			request := fmt.Sprintf(`{"type":"response.create","model":%q,"input":"hello","previous_response_id":"msg_ignore_me"}`, model)
			if turn%2 == 1 {
				request = fmt.Sprintf(`{"type":"response.create","model":%q,"input":[{"type":"function_call_output","call_id":"call_from_this_session","output":"ok"}]}`, model)
			}
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(request)))
			_, _, err = client.Read(ctx)
			require.NoError(t, err)
			upstream.mu.Lock()
			sent := cloneMapStringAny(upstream.lastWrite)
			upstream.mu.Unlock()
			wantModel := model
			if model == "astra-alias" {
				wantModel = "gpt-6-astra"
			}
			require.Equal(t, wantModel, sent["model"])
			if turn%2 == 0 {
				require.NotContains(t, sent, "previous_response_id")
			} else {
				require.Equal(t, fmt.Sprintf("resp_ws_%d_%d", session, turn-1), sent["previous_response_id"])
				input, ok := sent["input"].([]any)
				require.True(t, ok)
				require.Len(t, input, 1)
				require.Equal(t, "call_from_this_session", input[0].(map[string]any)["call_id"])
			}
		}
		require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
		select {
		case err := <-serverErrors:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	require.Equal(t, 1, dialer.DialCount())
}

func TestCookieWSManagedDirtySessionConnectionReleasesWaiters(t *testing.T) {
	svc, account, _, _ := newCookieManagedContinuationFixture(t)
	pool := svc.getOpenAIWSConnPool()
	lease, err := pool.Acquire(context.Background(), openAIWSAcquireRequest{Account: account, CookieSessionKey: "A"})
	require.NoError(t, err)
	lease.conn.rememberCookieContinuation("A", "gpt-6-astra", "resp_A")
	lease.conn.readerLoopResults = make(chan []byte, 1)
	lease.conn.readerLoopResults <- []byte(`{"type":"response.output_text.delta","delta":"late data"}`)
	lease.Release()
	ap, _ := pool.getAccountPool(account.ID)
	other := newOpenAIWSConn("clean", account.ID, &openAIWSCaptureConn{}, nil)
	ap.mu.Lock()
	ap.conns[other.id] = other
	ap.cookieBatch.target = 2
	ap.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	replacement, err := pool.Acquire(ctx, openAIWSAcquireRequest{Account: account, CookieSessionKey: "A"})
	require.NoError(t, err)
	defer replacement.Release()
	require.Equal(t, other.id, replacement.ConnID())
	payload, err := replacement.conn.cookieContinuationPayload([]byte(`{"input":"hello"}`), "A")
	require.NoError(t, err)
	require.NotContains(t, string(payload), "previous_response_id")
}
