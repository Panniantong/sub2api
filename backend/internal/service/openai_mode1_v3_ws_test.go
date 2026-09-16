package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Inject the loopback CA at the HTTP client boundary. The production path
// chooses standard TLS (no mode1 profile); certificates are still verified.
type mode1V3StandardTestDialer struct {
	target    string
	client    *http.Client
	protected chan bool
}

func (d *mode1V3StandardTestDialer) Dial(ctx context.Context, _ string, headers http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	_, protected := ctx.Value(openAIWSTLSContextKey{}).(openAIWSTLSConfig)
	d.protected <- protected
	conn, response, err := coderws.Dial(ctx, d.target, &coderws.DialOptions{HTTPClient: d.client, HTTPHeader: headers})
	if err != nil {
		return nil, 0, nil, err
	}
	conn.SetReadLimit(openAIWSMessageReadLimitBytes)
	return &coderOpenAIWSClientConn{conn: conn}, response.StatusCode, response.Header, nil
}

func TestMode1V3StandardWSSPoolSeparatesConversations(t *testing.T) {
	server, _, upgrades := newMode1WSTestEndpoint(t)
	dialer := &mode1V3StandardTestDialer{target: "wss" + strings.TrimPrefix(server.URL, "https"), client: server.Client(), protected: make(chan bool, 4)}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
	pool := newOpenAIWSConnPool(cfg)
	defer pool.Close()
	pool.setClientDialerForTest(dialer)
	a := mode1WSAccount(71)
	mode1Marker(a)["policy_version"] = 3
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := openAIWSAcquireRequest{Account: a, WSURL: dialer.target, Headers: http.Header{"Session-Id": {"first"}, "Thread-Id": {"first"}}}
	first, err := pool.Acquire(ctx, request)
	require.NoError(t, err)
	requireMode1WSEcho(t, ctx, first)
	first.Release()
	request.Headers = http.Header{"Session-Id": {"second"}, "Thread-Id": {"second"}}
	second, err := pool.Acquire(ctx, request)
	require.NoError(t, err)
	requireMode1WSEcho(t, ctx, second)
	require.NotEqual(t, first.ConnID(), second.ConnID())
	second.Release()
	require.False(t, <-dialer.protected)
	require.False(t, <-dialer.protected)
	require.EqualValues(t, 2, upgrades.Load())
}

func TestMode1V3WSPassthroughAcceptsCompatibilityFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upgrades := &atomic.Int32{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		upgrades.Add(1)
		defer conn.CloseNow()
		for {
			kind, payload, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), kind, payload); err != nil {
				return
			}
			if err := conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_local","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}}`)); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	dialer := &mode1V3StandardTestDialer{target: "wss" + strings.TrimPrefix(upstream.URL, "https"), client: upstream.Client(), protected: make(chan bool, 4)}
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.TLSFingerprint.Enabled = false
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	a := mode1WSAccount(72)
	mode1Marker(a)["policy_version"] = 3
	a.Credentials = map[string]any{"chatgpt_account_id": "00000000-0000-4000-8000-000000000072"}
	a.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModePassthrough
	svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
	svc.openaiWSPassthroughDialer = dialer
	svc.pluginManager = &PluginManager{} // configured manager must not block
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, a)
	defer server.Close()
	first := `{"type":"response.create","model":"gpt-5.2","input":"Return OK","max_output_tokens":100,"reasoning":{"effort":"high"}}`
	client := dialPassthroughLifecycleClientWithPayload(t, server, first)
	defer client.CloseNow()
	_, output, err := client.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, validateMode1RequestIntegrityForAccount(a, []byte(first), output))
	require.Contains(t, string(output), "Return OK")
	require.False(t, <-dialer.protected)
	_, terminal, err := client.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(terminal), "response.completed")
	second := `{"type":"response.create","model":"gpt-5.2","input":[{"type":"function_call","call_id":"call_123","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_123","output":"answer"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"reasoning":{"effort":"high"}}`
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(second)))
	_, output, err = client.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, validateMode1RequestIntegrityForAccount(a, []byte(second), output))
	require.Contains(t, string(output), "answer")
	_, terminal, err = client.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(terminal), "response.completed")
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case <-serverErr:
	case <-ctx.Done():
		t.Fatal("passthrough did not close")
	}
	require.EqualValues(t, 1, upgrades.Load())
}
