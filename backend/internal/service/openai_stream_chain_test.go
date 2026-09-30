package service

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponseFlush_TwoHTTPHopsDeliverTextBeforeCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	textReceived := make(chan struct{})
	var sentAt atomic.Int64
	const prefix = "data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}\n\n"
	const text = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"first text\"}\n\n"
	const terminal = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_chain\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, prefix)
		w.(http.Flusher).Flush()
		sentAt.Store(time.Now().UnixNano())
		_, _ = io.WriteString(w, text)
		w.(http.Flusher).Flush()
		select {
		case <-textReceived:
			_, _ = io.WriteString(w, terminal)
		case <-ctx.Done():
		}
	}))
	defer upstream.Close()
	results := make(chan *openaiStreamingResult, 2)
	errs := make(chan error, 2)
	newHop := func(target string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			c, _ := gin.CreateTestContext(w)
			c.Request = r
			s := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 30}}, toolCorrector: NewCodexToolCorrector()}
			result, err := s.handleStreamingResponse(ctx, resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "gpt-5", "gpt-5")
			results <- result
			errs <- err
		}))
	}
	firstHop := newHop(upstream.URL)
	defer firstHop.Close()
	secondHop := newHop(firstHop.URL)
	defer secondHop.Close()
	defer cancel() // Unblock all servers before their Close calls on failure.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, secondHop.URL, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))
	scanner := bufio.NewScanner(resp.Body)
	var body strings.Builder
	sawText := false
	for scanner.Scan() {
		line := scanner.Text()
		body.WriteString(line + "\n")
		if !sawText && strings.Contains(line, `"delta":"first text"`) {
			sawText = true
			t.Logf("first text crossed two HTTP streaming hops in %s", time.Since(time.Unix(0, sentAt.Load())))
			close(textReceived)
		}
	}
	require.NoError(t, scanner.Err())
	require.True(t, sawText)
	require.True(t, strings.HasPrefix(body.String(), prefix+text), "incremental events must survive both hops byte-for-byte")
	// The existing accumulator fills missing response.output on completion.
	require.Contains(t, body.String(), `"type":"response.completed"`)
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
		result := <-results
		require.NotNil(t, result)
		require.Equal(t, 10, result.usage.InputTokens)
		require.Equal(t, 2, result.usage.OutputTokens)
	}
}
