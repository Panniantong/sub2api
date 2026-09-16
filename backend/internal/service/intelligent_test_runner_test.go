package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type intelligentRunnerRepo struct {
	AccountRepository
	account *Account
	writes  int
}

func (r *intelligentRunnerRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}
func (r *intelligentRunnerRepo) SetError(context.Context, int64, string) error {
	r.writes++
	return nil
}
func (r *intelligentRunnerRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	r.writes++
	return nil
}
func (r *intelligentRunnerRepo) SetRateLimited(context.Context, int64, time.Time) error {
	r.writes++
	return nil
}

type intelligentNetworkUpstream struct{ client *http.Client }

func (h intelligentNetworkUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return h.client.Do(req)
}
func (h intelligentNetworkUpstream) DoWithTLS(req *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return h.Do(req, p, id, n)
}
func intelligentRunnerFixture(t *testing.T, upstream http.HandlerFunc) (*AccountTestService, *intelligentRunnerRepo) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	repo := &intelligentRunnerRepo{account: &Account{ID: 81, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-private-test-secret-abc123", "base_url": server.URL}, Extra: map[string]any{"anti_degradation": true}}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := NewAccountTestService(repo, nil, nil, nil, nil, intelligentNetworkUpstream{client: server.Client()}, cfg, nil)
	return svc, repo
}

func TestResolveIntelligentTestModelCodexFallback(t *testing.T) {
	codex := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	require.Equal(t, "gpt-5.3-codex", resolveIntelligentTestModel(codex, ""))
	for _, configured := range []string{"gpt-5.4", "GPT-5.4", "gpt-5.4-mini"} {
		require.Equal(t, configured, resolveIntelligentTestModel(codex, configured))
	}
	require.Equal(t, "gpt-5.3-codex", resolveIntelligentTestModel(codex, "gpt-5.3-codex"))
	apiKey := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.Equal(t, "gpt-5.4", resolveIntelligentTestModel(apiKey, "gpt-5.4"))
}

func TestIntelligentRunnerActualHTTPPromptOutputAndRaw(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var received map[string]any
	svc, repo := intelligentRunnerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer sk-private-test-secret-abc123", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"24 - 6 = 18; 18 / 2 + 3 = 12.\\nANSWER: 12\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
	})
	record := &IntelligentTestRecord{AccountID: 81, Input: "Please solve the configured candy question", Model: "test-model", ConfigSnapshot: &IntelligentTestConfig{Model: "test-model"}}
	require.NoError(t, svc.RunIntelligentTest(context.Background(), record))
	body, _ := json.Marshal(received)
	require.Contains(t, string(body), record.Input)
	require.NotContains(t, string(body), `"hi"`)
	require.Contains(t, record.Result, "ANSWER: 12")
	require.Contains(t, record.RawResponse, "response.completed")
	require.NotContains(t, record.RawResponse, "sk-private")
	require.Equal(t, 0, repo.writes)
	require.Equal(t, true, repo.account.Extra["anti_degradation"])
}
func TestIntelligentRunnerFailureDoesNotModifyAccountAndRedacts(t *testing.T) {
	for _, status := range []int{401, 429, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			svc, repo := intelligentRunnerFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"message":"upstream error sk-private-test-secret-abc123"}}`)
			})
			record := &IntelligentTestRecord{AccountID: 81, Input: "test", Model: "model", ConfigSnapshot: &IntelligentTestConfig{}}
			require.Error(t, svc.RunIntelligentTest(context.Background(), record))
			require.Equal(t, map[int]string{401: "account_error", 429: "rate_limited", 404: "model_error"}[status], record.Status)
			require.NotContains(t, record.RawResponse, "sk-private-test-secret")
			require.NotContains(t, record.ErrorMessage, "sk-private-test-secret")
			require.Equal(t, 0, repo.writes)
		})
	}
}
func TestIntelligentRunnerIncompleteAndSyntheticNeverSuccess(t *testing.T) {
	svc, repo := intelligentRunnerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ANSWER: 12\"}\n\n")
	})
	record := &IntelligentTestRecord{AccountID: 81, Input: "test", Model: "model", ConfigSnapshot: &IntelligentTestConfig{}}
	require.Error(t, svc.RunIntelligentTest(context.Background(), record))
	require.Equal(t, "failed", record.Status)
	repo.account.Extra["synthetic_ui_test"] = true
	require.Error(t, svc.RunIntelligentTest(context.Background(), record))
	require.NotContains(t, record.Result, "healthy and interactive")
}
func TestIntelligentRunnerContextCancellation(t *testing.T) {
	svc, _ := intelligentRunnerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"working\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	record := &IntelligentTestRecord{AccountID: 81, Input: "test", ConfigSnapshot: &IntelligentTestConfig{}}
	require.Error(t, svc.RunIntelligentTest(ctx, record))
}
func TestIntelligentRawCompletionRejectsTruncation(t *testing.T) {
	cases := []struct {
		raw string
		ok  bool
	}{{`{"stop_reason":"end_turn"}`, true}, {`{"stop_reason":"max_tokens"}`, false}, {"data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n", true}, {"data: {\"candidates\":[{\"finishReason\":\"MAX_TOKENS\"}]}\n", false}, {"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\"}}\n", false}, {"data: {\"type\":\"message_stop\"}\n", true}, {`{"choices":[{"finish_reason":null}]}`, false}}
	for _, c := range cases {
		require.Equal(t, c.ok, intelligentRawComplete(c.raw), c.raw)
	}
}
func TestIntelligentSVGAndCandyEvaluation(t *testing.T) {
	good := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100"><title>Pelican</title><circle cx="20" cy="70" r="15"/><path d="M20 70 L60 10 L100 70 Z" fill="none"/></svg>`
	safe, err := SanitizeIntelligentTestSVG("```svg\n" + good + "\n```")
	require.NoError(t, err)
	require.Contains(t, safe, "<circle")
	for _, bad := range []string{`<svg><script>alert(1)</script><circle r="1"/></svg>`, `<svg><foreignObject/><circle r="1"/></svg>`, `<svg onload="alert(1)"><circle r="1"/></svg>`, `<svg><image href="https://secret"/><circle r="1"/></svg>`, `<svg><circle fill="url(https://secret)"/></svg>`, `<svg><text>only text</text></svg>`, `<svg><circle></svg>`} {
		_, err := SanitizeIntelligentTestSVG(bad)
		require.Error(t, err, bad)
	}
	evaluators := DefaultIntelligentTestEvaluators()
	result := evaluators["svg_structure"].Evaluate(good, IntelligentTestConfig{})
	require.Equal(t, "completed", result.Status)
	require.Nil(t, result.Score)
	require.Equal(t, "not_evaluated", result.Detail["answer_verdict"])
	cfg := IntelligentTestConfig{ExpectedAnswer: "12"}
	require.Equal(t, "incorrect", evaluators["exact_answer"].Evaluate("ANSWER: 11", cfg).Detail["answer_verdict"])
	require.Equal(t, "undetermined", evaluators["exact_answer"].Evaluate("ANSWER: 12\nANSWER: 11", cfg).Detail["answer_verdict"])
	require.Equal(t, "correct", evaluators["exact_answer"].Evaluate("12", cfg).Detail["answer_verdict"])
	require.Equal(t, "completed", evaluators["exact_answer"].Evaluate("reasoning\nANSWER: 12", cfg).Status)
}
func TestIntelligentCaptureBounded(t *testing.T) {
	capture := &intelligentCapture{}
	n, err := io.Copy(capture, strings.NewReader(strings.Repeat("x", 2<<20)))
	require.NoError(t, err)
	require.EqualValues(t, 2<<20, n)
	require.Equal(t, 1<<20, capture.body.Len())
	require.True(t, capture.truncated)
}

type intelligentRedirectUpstream struct {
	target   *url.URL
	client   *http.Client
	requests int
}

func (h *intelligentRedirectUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	h.requests++
	copy := req.Clone(req.Context())
	target := *req.URL
	target.Scheme = h.target.Scheme
	target.Host = h.target.Host
	copy.URL = &target
	copy.Host = ""
	return h.client.Do(copy)
}
func (h *intelligentRedirectUpstream) DoWithTLS(req *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return h.Do(req, p, id, n)
}
func TestIntelligentAntigravitySingleObservationPreservesPolicy(t *testing.T) {
	var sent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`)
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	transport := &intelligentRedirectUpstream{target: target, client: server.Client()}
	repo := &intelligentRunnerRepo{account: &Account{ID: 81, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Concurrency: 1, Extra: map[string]any{"anti_degradation": true}}}
	gateway := &AntigravityGatewayService{httpUpstream: transport, accountRepo: repo}
	capture := &intelligentCapture{}
	ctx := context.WithValue(context.Background(), intelligentRunKey{}, &intelligentRunContext{prompt: "draw the configured pelican", capture: capture})
	body, err := intelligentAntigravityBody(ctx, []byte(`{"model":"test","request":{"contents":[{"parts":[{"text":"."}]}],"generationConfig":{"maxOutputTokens":1}}}`))
	require.NoError(t, err)
	resp, err := gateway.runIntelligentAntigravity(ctx, repo.account, "fake-access-token", body)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 429, resp.StatusCode)
	require.Equal(t, 1, transport.requests)
	require.Zero(t, repo.writes)
	require.Contains(t, sent, "draw the configured pelican")
	require.Contains(t, sent, `"maxOutputTokens":8192`)
	require.Equal(t, true, repo.account.Extra["anti_degradation"])
}
