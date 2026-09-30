package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/streamlatency"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAICPAFirstByteReturnedByStreamHandlers(t *testing.T) {
	enableCPAModeForTest(t)
	for _, route := range []string{"responses", "passthrough", "chat", "messages", "raw_chat"} {
		t.Run(route, func(t *testing.T) {
			s := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			account := &Account{ID: 1, Platform: PlatformOpenAI}
			body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_cpa\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_cpa\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
			if route == "raw_chat" {
				body = "data: {\"id\":\"chat_test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"
			}
			resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
			streamlatency.ObserveFirstByte(resp, c.Request, time.Now())
			start := time.Now().Add(-time.Hour)
			var ms *int
			var err error
			switch route {
			case "responses":
				result, e := s.handleStreamingResponse(c.Request.Context(), resp, c, account, start, "model", "model")
				err = e
				if result != nil {
					ms = result.firstTokenMs
					require.Equal(t, 1, result.usage.OutputTokens)
				}
			case "passthrough":
				result, e := s.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, start, "model", "model")
				err = e
				if result != nil {
					ms = result.firstTokenMs
					require.Equal(t, 1, result.usage.OutputTokens)
				}
			case "chat":
				result, e := s.handleChatStreamingResponse(resp, c, account, "model", "model", "model", start, 1)
				err = e
				if result != nil {
					ms = result.FirstTokenMs
				}
			case "messages":
				result, e := s.handleAnthropicStreamingResponse(resp, c, account, "model", "model", "model", start)
				err = e
				if result != nil {
					ms = result.FirstTokenMs
				}
			case "raw_chat":
				result, e := s.streamRawChatCompletions(c, resp, account, "model", "model", "model", nil, nil, start, 1)
				err = e
				if result != nil {
					ms = result.FirstTokenMs
				}
			}
			require.NoError(t, err)
			require.NotNil(t, ms)
			recorded, tracked := streamlatency.FirstByteMilliseconds(resp)
			require.True(t, tracked)
			require.Equal(t, recorded, ms)
			require.Less(t, *ms, 3600000, "must exclude pre-upstream time")
			require.Contains(t, rec.Body.String(), "OK")
		})
	}
}

func TestOpenAICPAPreambleDoesNotDisarmNativeTimeout(t *testing.T) {
	enableCPAModeForTest(t)
	s := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, OpenAIFirstOutputTimeoutSeconds: 1}}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	// Finite writer lifetime also bounds the regression if the timer is broken.
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer writer.Close()
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.created\"}\n\n")
		time.Sleep(1500 * time.Millisecond)
	}()
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader}
	streamlatency.ObserveFirstByte(resp, c.Request, time.Now())
	_, err := s.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "model", "model")
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Contains(t, string(failover.ResponseBody), "first_output_timeout")
	ms, tracked := streamlatency.FirstByteMilliseconds(resp)
	require.True(t, tracked)
	require.NotNil(t, ms)
	require.NotContains(t, rec.Body.String(), "response.created")
	<-done
}

func TestOpenAICPAFirstByteModeAndEmptyBody(t *testing.T) {
	enableCPAModeForTest(t)
	require.Equal(t, OpenAITTFTModeCPA, normalizeOpenAITTFTMode(" CPA "))
	require.Equal(t, OpenAITTFTModeSemantic, normalizeOpenAITTFTMode(""))
	legacy := 123
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(""))}
	require.Same(t, &legacy, (&OpenAIGatewayService{}).openAIHTTPFirstTokenMs(resp, &legacy))
	streamlatency.ObserveFirstByte(resp, httptest.NewRequest("POST", "/", nil), time.Now())
	_, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Nil(t, (&OpenAIGatewayService{}).openAIHTTPFirstTokenMs(resp, &legacy))
}

func enableCPAModeForTest(t *testing.T) {
	previous := gatewayForwardingCache.Load()
	if previous == nil {
		previous = (*cachedGatewayForwardingSettings)(nil)
	}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{openAITTFTMode: OpenAITTFTModeCPA, expiresAt: time.Now().Add(time.Minute).UnixNano()})
	t.Cleanup(func() { gatewayForwardingCache.Store(previous) })
}
