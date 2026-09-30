//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseCookieSyncDoesNotBlockHTTPForward(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, incoming := range []string{"", "client=original"} {
			repo := &responseSyncBlockingRepo{values: map[string]string{cookieSettingsKey: `{"response_cookie_sync_enabled":true}`}, entered: make(chan struct{}), release: make(chan struct{})}
			settings := &SettingService{settingRepo: repo}
			cookie := responseSyncTestCookie("upstream.example", 100, time.Now().Add(time.Hour).Unix())
			payload := `{"id":"chat_test","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"delta":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`
			body := fmt.Sprintf(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":%v}`, stream)
			if stream {
				payload = "data: " + payload + "\n\ndata: [DONE]\n\n"
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": {cookie + "; HttpOnly; Path=/"}}, Body: io.NopCloser(strings.NewReader(payload))}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, settingService: settings}
			account := rawChatCompletionsTestAccount()
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			ctx, cancel := context.WithCancel(context.Background())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
			if incoming != "" {
				c.Request.Header.Set("Cookie", incoming)
			}
			done := make(chan error, 1)
			go func() { _, err := svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", ""); done <- err }()
			var forwardErr error
			select {
			case forwardErr = <-done:
			case <-time.After(2 * time.Second):
				close(repo.release)
				cancel()
				t.Fatal("cookie storage blocked HTTP forwarding")
			}
			cancel()
			close(repo.release)
			require.Eventually(t, func() bool { return !svc.responseCookieSync.running.Load() }, 10*time.Second, time.Millisecond)
			require.NoError(t, forwardErr)
			require.Equal(t, 200, writer.Code)
			require.Contains(t, writer.Body.String(), "OK")
			want := cookie + "; HttpOnly; Path=/"
			if incoming != "" {
				want = incoming
			}
			require.Equal(t, want, writer.Header().Get("Set-Cookie"))
			entries, err := settings.GetOpenAICodexCookieLibrary(context.Background())
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, cookie, entries[0].Cookie)
		}
	}
}
