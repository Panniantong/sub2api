//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIDownstreamCookieForwardHTTP(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, incoming := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/cookie=%v", stream, incoming), func(t *testing.T) {
				requestBody := []byte(fmt.Sprintf(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":%v}`, stream))
				payload := `{"id":"chat_test","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"delta":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`
				if stream {
					payload = "data: " + payload + "\n\ndata: [DONE]\n\n"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": {"__oailb=internal; HttpOnly; Path=/"}, "Cookie": {"__oailb=internal"}}, Body: io.NopCloser(strings.NewReader(payload))}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, responseHeaderFilter: responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{Enabled: true, AdditionalAllowed: []string{"set-cookie", "cookie"}})}
				if !incoming {
					svc.responseHeaderFilter = nil // Default filtering must not lose the fallback cookie.
				}
				var result *OpenAIForwardResult
				var forwardErr error
				router := gin.New()
				account := rawChatCompletionsTestAccount()
				account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
				router.POST("/v1/chat/completions", func(c *gin.Context) {
					result, forwardErr = svc.ForwardAsChatCompletions(context.Background(), c, account, requestBody, "", "")
				})
				server := httptest.NewServer(router)
				defer server.Close()
				req, err := http.NewRequest("POST", server.URL+"/v1/chat/completions", bytes.NewReader(requestBody))
				require.NoError(t, err)
				if incoming {
					req.Header.Set("Cookie", "__oailb=client-original; other=one")
				}
				resp, err := server.Client().Do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				require.NoError(t, forwardErr)
				require.Equal(t, 200, resp.StatusCode)
				require.Contains(t, string(body), "OK")
				if incoming {
					require.Equal(t, []string{"__oailb=client-original", "other=one"}, resp.Header.Values("Set-Cookie"))
				} else {
					require.Equal(t, []string{"__oailb=internal; HttpOnly; Path=/"}, resp.Header.Values("Set-Cookie"))
				}
				require.Empty(t, resp.Header.Get("Cookie"))
				require.NotNil(t, result)
				require.Equal(t, 9, result.Usage.InputTokens)
				require.Equal(t, 4, result.Usage.OutputTokens)
				if incoming {
					require.Equal(t, "__oailb=client-original; other=one", result.DownstreamRequestCookie)
					require.Equal(t, "__oailb=client-original\nother=one", result.DownstreamResponseCookie)
				} else {
					require.Empty(t, result.DownstreamRequestCookie)
					require.Equal(t, "__oailb=internal; HttpOnly; Path=/", result.DownstreamResponseCookie)
				}
				require.Equal(t, "__oailb=internal; HttpOnly; Path=/", result.UpstreamHeaders.Get("Set-Cookie"))
			})
		}
	}
}
