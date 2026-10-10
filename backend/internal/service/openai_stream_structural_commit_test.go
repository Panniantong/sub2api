package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setOpenAIStreamStructuralCommitForTest(t *testing.T, enabled bool) {
	t.Helper()
	previous, _ := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
		openAITTFTMode:               OpenAITTFTModeSemantic,
		openAIStreamStructuralCommit: enabled,
		fingerprintUnification:       true,
		expiresAt:                    time.Now().Add(time.Minute).UnixNano(),
	})
	// Always leave an expired entry behind so later tests see the code default,
	// regardless of which test ran first.
	_ = previous
	t.Cleanup(func() {
		gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{expiresAt: 1})
	})
}

func TestOpenAIStreamDataCommitsFirstOutput(t *testing.T) {
	emptyReasoning := `{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[]}}`
	for _, tc := range []struct {
		name       string
		data       string
		eventType  string
		structural bool
		want       bool
	}{
		{"created is metadata either way", `{"type":"response.created","response":{"id":"resp_1"}}`, "response.created", true, false},
		{"in_progress is metadata either way", `{"type":"response.in_progress","response":{"id":"resp_1"}}`, "response.in_progress", true, false},
		{"empty reasoning item held without structural commit", emptyReasoning, "response.output_item.added", false, false},
		{"empty reasoning item commits with structural commit", emptyReasoning, "response.output_item.added", true, true},
		{"event type inferred from payload", emptyReasoning, "", true, true},
		{"text delta commits regardless", `{"type":"response.output_text.delta","delta":"hi"}`, "response.output_text.delta", false, true},
		{"retryable error frame never commits", `{"type":"error","code":"server_is_overloaded","message":"overloaded"}`, "error", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAIStreamDataCommitsFirstOutput(tc.data, tc.eventType, tc.structural))
		})
	}
}

func TestOpenAIStreamStructuralCommitDefaultsToEnabled(t *testing.T) {
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{expiresAt: 1})
	svc := &OpenAIGatewayService{}
	require.True(t, svc.openAIStreamStructuralCommit(t.Context()))
	setOpenAIStreamStructuralCommitForTest(t, false)
	require.False(t, svc.openAIStreamStructuralCommit(t.Context()))
	require.True(t, parseOpenAIStreamStructuralCommit(""))
	require.True(t, parseOpenAIStreamStructuralCommit("true"))
	require.False(t, parseOpenAIStreamStructuralCommit("false"))
}

const structuralCommitPreamble = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_pre\"}}\n\n" +
	"data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_pre\"}}\n\n" +
	"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"rs_pre\",\"type\":\"reasoning\",\"summary\":[]}}\n\n"

func runStructuralCommitStream(t *testing.T, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "model", "model")
	return rec, err
}

// With structural commit the preamble reaches the client as soon as the model
// opens its first output item, before any visible content exists.
func TestOpenAIStreamStructuralCommitReleasesPreambleAtFirstOutputItem(t *testing.T) {
	setOpenAIStreamStructuralCommitForTest(t, true)
	rec, _ := runStructuralCommitStream(t, structuralCommitPreamble)
	body := rec.Body.String()
	require.Contains(t, body, "response.created")
	require.Contains(t, body, "response.in_progress")
	require.Contains(t, body, "response.output_item.added")
}

func TestOpenAIStreamStructuralCommitDisabledHoldsPreambleUntilVisibleContent(t *testing.T) {
	setOpenAIStreamStructuralCommitForTest(t, false)
	rec, err := runStructuralCommitStream(t, structuralCommitPreamble)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr, "an attempt without committed output must stay failover-eligible")
	require.NotContains(t, rec.Body.String(), "response.output_item.added")
	require.NotContains(t, rec.Body.String(), "response.created")

	// Visible content still opens the stage with the preamble in order.
	rec, _ = runStructuralCommitStream(t, structuralCommitPreamble+
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"+
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_pre\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	body := rec.Body.String()
	require.Less(t, strings.Index(body, "response.created"), strings.Index(body, "response.output_item.added"))
	require.Less(t, strings.Index(body, "response.output_item.added"), strings.Index(body, "hello"))
}

// Once the structural commit has released the preamble the attempt is owned by
// the client: a later retryable failure is forwarded, not replayed elsewhere.
func TestOpenAIStreamStructuralCommitForwardsLaterFailureInsteadOfFailover(t *testing.T) {
	failed := "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_pre\",\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"Rate limit reached\"}}}\n\n"

	setOpenAIStreamStructuralCommitForTest(t, false)
	rec, err := runStructuralCommitStream(t, structuralCommitPreamble+failed)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr, "held preamble keeps pre-output failover")
	require.NotContains(t, rec.Body.String(), "response.output_item.added")

	setOpenAIStreamStructuralCommitForTest(t, true)
	rec, err = runStructuralCommitStream(t, structuralCommitPreamble+failed)
	require.False(t, isUpstreamFailoverError(err), "committed attempt must not fail over: %v", err)
	require.Contains(t, rec.Body.String(), "response.output_item.added")
	require.Contains(t, rec.Body.String(), "response.failed")
}

// The first-output timeout guards an attempt only until it is committed. With
// structural commit the opened reasoning item commits it, so the timer is
// disarmed and the preamble is already downstream when upstream stalls.
func TestOpenAIStreamStructuralCommitDisarmsFirstOutputTimeout(t *testing.T) {
	setOpenAIStreamStructuralCommitForTest(t, true)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:                     defaultMaxLineSize,
		OpenAIFirstOutputTimeoutSeconds: 1,
	}}}
	reader, writer := io.Pipe()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer func() { _ = writer.Close() }()
		_, _ = io.WriteString(writer, structuralCommitPreamble)
		_, _ = io.WriteString(writer, "data: {\"type\":\"keepalive\"}\n\n")
		time.Sleep(1200 * time.Millisecond)
	}()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "model", "model")
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		require.False(t, failoverErr.SafeToFailoverAfterWrite, "committed preamble must not be replayed on another account")
	}
	require.Contains(t, rec.Body.String(), "response.output_item.added")
	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("synthetic upstream writer did not exit")
	}
}

func isUpstreamFailoverError(err error) bool {
	var failoverErr *UpstreamFailoverError
	return err != nil && errors.As(err, &failoverErr)
}
