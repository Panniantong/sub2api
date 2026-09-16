package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIntelligentProtectedResponsesUsesStableDeviceAndIndependentSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var headers []http.Header
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		headers = append(headers, r.Header.Clone())
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ANSWER: 12\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	transport := &intelligentRedirectUpstream{target: target, client: server.Client()}
	account := &Account{ID: 810, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 50,
		Credentials: map[string]any{"access_token": "test-oauth-access-token", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339), "chatgpt_account_id": "test-account"}}
	admin := &adminServiceImpl{accountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}}
	account, err = NewAntiDegradeService(admin).ApplyAntiDegradeMode(context.Background(), account.ID, AntiDegradeMode1)
	require.NoError(t, err)
	before, err := json.Marshal(account.Extra)
	require.NoError(t, err)
	repo := &intelligentRunnerRepo{account: account}
	svc := NewAccountTestService(repo, nil, nil, nil, nil, transport, &config.Config{}, nil)
	for range 2 {
		record := &IntelligentTestRecord{AccountID: account.ID, Input: "Please solve this candy question. Do not change it.", Model: "gpt-5.2", ConfigSnapshot: &IntelligentTestConfig{Model: "gpt-5.2"}}
		require.NoError(t, svc.RunIntelligentTest(context.Background(), record))
		require.True(t, record.AntiDegradation)
		require.NotNil(t, record.ConfigSnapshot.Execution)
		require.Equal(t, "mode1", record.ConfigSnapshot.Execution.Strategy)
		require.Equal(t, "standard", record.ConfigSnapshot.Execution.EffectiveTLS)
		require.Equal(t, "gpt-5.2", record.ConfigSnapshot.Execution.Model)
		require.Len(t, record.ConfigSnapshot.Execution.PromptDigest, 64)
		require.Contains(t, record.Result, "ANSWER: 12")
	}
	require.Len(t, headers, 2)
	expectedDevice := resolveCodexFingerprintIDs(account, "", codexFingerprintDevice).installationID
	for i, header := range headers {
		require.Equal(t, expectedDevice, header.Get("x-codex-installation-id"))
		require.NotEmpty(t, header.Get("session-id"))
		require.Equal(t, header.Get("session-id"), header.Get("session_id"))
		require.Equal(t, header.Get("session-id"), header.Get("conversation_id"))
		require.NotEmpty(t, header.Get("thread-id"))
		metadata := bodies[i]["client_metadata"].(map[string]any)
		require.Equal(t, expectedDevice, metadata["x-codex-installation-id"])
		require.Equal(t, header.Get("session-id"), metadata["session_id"])
		require.Equal(t, header.Get("thread-id"), metadata["thread_id"])
		encoded, err := json.Marshal(bodies[i])
		require.NoError(t, err)
		require.Contains(t, string(encoded), "Please solve this candy question. Do not change it.")
	}
	require.NotEqual(t, headers[0].Get("session-id"), headers[1].Get("session-id"))
	require.NotEqual(t, headers[0].Get("thread-id"), headers[1].Get("thread-id"))
	after, err := json.Marshal(account.Extra)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	require.Zero(t, repo.writes)

	// Ordinary connectivity probes must use the same protected identity too.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "gpt-5.2", "", AccountTestModeDefault))
	require.Len(t, headers, 3)
	require.Equal(t, expectedDevice, headers[2].Get("x-codex-installation-id"))
	require.Equal(t, expectedDevice, bodies[2]["client_metadata"].(map[string]any)["x-codex-installation-id"])
}

func TestIntelligentProtectionRejectsLostPrompt(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil).WithContext(context.WithValue(context.Background(), intelligentRunKey{}, &intelligentRunContext{prompt: "original"}))
	account := &Account{ID: 811, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	admin := &adminServiceImpl{accountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}}
	account, err := NewAntiDegradeService(admin).ApplyAntiDegradeMode(context.Background(), account.ID, AntiDegradeMode1)
	require.NoError(t, err)
	payload := createOpenAITestPayload("gpt-5.2", true)
	applyIntelligentPayloadPrompt(c.Request.Context(), payload)
	require.NoError(t, prepareIntelligentTestProtection(c, account, payload))
	payload["input"] = "changed by an incompatible transformation"
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	require.ErrorContains(t, applyIntelligentTestProtection(c, account, http.Header{}, body), "input")
}
