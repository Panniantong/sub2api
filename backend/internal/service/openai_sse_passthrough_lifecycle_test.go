package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Only the test transport rewrites the target to a loopback server; request
// construction and detached-context handling use the real Forward path.
type sseLifecycleLoopbackUpstream struct {
	HTTPUpstream
	client *http.Client
	target *url.URL
}

func (u *sseLifecycleLoopbackUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	clone := req.Clone(req.Context())
	target := *u.target
	clone.URL, clone.Host, clone.RequestURI = &target, target.Host, ""
	return u.client.Do(clone)
}

func TestOpenAISSEReadPumpFullForwardSilence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"one partial output\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}\n\n")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("upstream test writer does not support http.Flusher")
			return
		}
		flusher.Flush()
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer unblock()
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}},
		httpUpstream: &sseLifecycleLoopbackUpstream{client: server.Client(), target: target},
	}
	a := ticketTestAccount(732)
	a.Extra = map[string]any{"openai_passthrough": true}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	type outcome struct {
		result *OpenAIForwardResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := svc.Forward(context.Background(), c, a, []byte(`{"model":"gpt-5.5","stream":true,"input":"synthetic","instructions":"synthetic"}`))
		done <- outcome{result, err}
	}()
	select {
	case <-entered:
	case got := <-done:
		t.Fatalf("upstream not entered: %v", got.err)
	case <-time.After(3 * time.Second):
		unblock()
		t.Fatal("upstream not entered within test bound")
	}
	select {
	case got := <-done:
		require.ErrorIs(t, got.err, errOpenAISSEIdle)
		require.NotNil(t, got.result, "observed partial usage must survive local timeout")
		require.Equal(t, 4, got.result.Usage.InputTokens)
		require.Equal(t, 2, got.result.Usage.OutputTokens)
		require.False(t, got.result.SucceededForScheduling())
		require.Empty(t, got.result.UpstreamTerminalEvent, "local timeout is not an upstream terminal")
		require.Contains(t, recorder.Body.String(), "response.failed")
		require.NotContains(t, recorder.Body.String(), "response.completed")
		require.True(t, IsResponseCommitted(c), "local timeout terminal must be visible to the handler")
		require.Nil(t, a.TempUnschedulableUntil)
	case <-time.After(3 * time.Second):
		unblock()
		<-done
		t.Fatal("silent stream exceeded configured interval")
	}
}

func TestOpenAIPassthroughStreamDataIntervalUsesImageTimeout(t *testing.T) {
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout:      180,
			ImageStreamDataIntervalTimeout: 900,
		}},
	}

	require.Equal(t, 180*time.Second, svc.openAIPassthroughStreamDataInterval(""))
	require.Equal(t, 900*time.Second, svc.openAIPassthroughStreamDataInterval("gpt-image-1"))
}
