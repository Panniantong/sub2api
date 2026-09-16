package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/stretchr/testify/require"
)

func intelligentAuthContext() context.Context {
	return context.WithValue(context.Background(), intelligentRunKey{}, &intelligentRunContext{capture: &intelligentCapture{}})
}

type intelligentAuthRepo struct {
	AccountRepository
	account       *Account
	reads, writes int
}

func (r *intelligentAuthRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	return r.account, nil
}
func (r *intelligentAuthRepo) Update(context.Context, *Account) error {
	r.writes++
	return nil
}

type intelligentAuthCache struct {
	GeminiTokenCache
	reads int
	token string
}

func (c *intelligentAuthCache) GetAccessToken(context.Context, string) (string, error) {
	c.reads++
	return c.token, nil
}

type intelligentAuthExecutor struct{ calls int }

func (e *intelligentAuthExecutor) CacheKey(*Account) string { e.calls++; return "test" }
func (e *intelligentAuthExecutor) CanRefresh(*Account) bool { e.calls++; return true }
func (e *intelligentAuthExecutor) NeedsRefresh(*Account, time.Duration) bool {
	e.calls++
	return true
}
func (e *intelligentAuthExecutor) Refresh(context.Context, *Account) (map[string]any, error) {
	e.calls++
	return map[string]any{"access_token": "rotated", "refresh_token": "rotated-refresh"}, nil
}

func TestIntelligentOAuthProvidersUseSnapshotWithoutCacheOrRotation(t *testing.T) {
	for _, platform := range []string{PlatformGemini, PlatformAnthropic, PlatformAntigravity, PlatformGrok} {
		for _, state := range []string{"valid-within-refresh-skew", "expired", "missing-expiry", "missing-access-token"} {
			t.Run(platform+"/"+state, func(t *testing.T) {
				account := &Account{ID: 87, Platform: platform, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{
					"access_token": "existing-access", "refresh_token": "existing-refresh",
					"expires_at": time.Now().Add(time.Minute).Format(time.RFC3339), "auto_detect_project_id": true,
				}}
				switch state {
				case "expired":
					account.Credentials["expires_at"] = time.Now().Add(-time.Minute).Format(time.RFC3339)
				case "missing-expiry":
					delete(account.Credentials, "expires_at")
				case "missing-access-token":
					delete(account.Credentials, "access_token")
				}
				before := shallowCopyMap(account.Credentials)
				repo := &intelligentAuthRepo{account: account}
				cache := &intelligentAuthCache{token: "newer-shared-cache-token"}
				executor := &intelligentAuthExecutor{}
				api := NewOAuthRefreshAPI(repo, cache)
				var get func(context.Context, *Account) (string, error)
				switch platform {
				case PlatformGemini:
					p := NewGeminiTokenProvider(repo, cache, &GeminiOAuthService{})
					p.SetRefreshAPI(api, executor)
					get = p.GetAccessToken
				case PlatformAnthropic:
					p := NewClaudeTokenProvider(repo, cache, &OAuthService{})
					p.SetRefreshAPI(api, executor)
					get = p.GetAccessToken
				case PlatformAntigravity:
					p := NewAntigravityTokenProvider(repo, cache, &AntigravityOAuthService{})
					p.SetRefreshAPI(api, executor)
					get = p.GetAccessToken
				case PlatformGrok:
					p := NewGrokTokenProvider(repo, cache)
					p.SetRefreshAPI(api, executor)
					get = p.GetAccessToken
				}
				token, err := get(intelligentAuthContext(), account)
				if state == "valid-within-refresh-skew" {
					require.NoError(t, err)
					require.Equal(t, "existing-access", token)
				} else {
					require.ErrorIs(t, err, errIntelligentCredentialRefreshRequired)
					require.Empty(t, token)
				}
				require.Equal(t, before, account.Credentials)
				require.Zero(t, cache.reads)
				require.Zero(t, executor.calls)
				require.Zero(t, repo.reads)
				require.Zero(t, repo.writes)
			})
		}
	}
}

type intelligentAuthCodeAssist struct {
	GeminiCliCodeAssistClient
	calls int
}

func (c *intelligentAuthCodeAssist) LoadCodeAssist(context.Context, string, string, *geminicli.LoadCodeAssistRequest) (*geminicli.LoadCodeAssistResponse, error) {
	c.calls++
	return &geminicli.LoadCodeAssistResponse{CloudAICompanionProject: "auto-detected-project"}, nil
}

func TestIntelligentGeminiRunnerDoesNotDiscoverProjectOrChangeRoute(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Contains(t, r.URL.Path, "/models/gemini-test:streamGenerateContent")
		require.Equal(t, "Bearer existing-access", r.Header.Get("Authorization"))
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.NotContains(t, payload, "project")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ANSWER: 12\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer server.Close()
	account := &Account{ID: 88, Platform: PlatformGemini, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{
		"access_token": "existing-access", "refresh_token": "existing-refresh", "auto_detect_project_id": true,
		"base_url": server.URL, "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
	}}
	before := shallowCopyMap(account.Credentials)
	repo := &intelligentAuthRepo{account: account}
	codeAssist := &intelligentAuthCodeAssist{}
	provider := NewGeminiTokenProvider(repo, nil, &GeminiOAuthService{codeAssist: codeAssist})
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := NewAccountTestService(repo, provider, nil, nil, nil, intelligentNetworkUpstream{client: server.Client()}, cfg, nil)
	record := &IntelligentTestRecord{AccountID: account.ID, Input: "Configured candy prompt", Model: "gemini-test", ConfigSnapshot: &IntelligentTestConfig{Model: "gemini-test"}}
	require.NoError(t, svc.RunIntelligentTest(context.Background(), record))
	require.Equal(t, "ANSWER: 12", record.Result)
	require.Equal(t, 1, requests)
	require.Zero(t, codeAssist.calls)
	require.Zero(t, repo.writes)
	require.Equal(t, before, account.Credentials)

	// Expired authentication is a recorded account error with no upstream work.
	account.Credentials["expires_at"] = time.Now().Add(-time.Hour).Format(time.RFC3339)
	record = &IntelligentTestRecord{AccountID: account.ID, Input: "test", Model: "gemini-test", ConfigSnapshot: &IntelligentTestConfig{}}
	require.Error(t, svc.RunIntelligentTest(context.Background(), record))
	require.Equal(t, "account_error", record.Status)
	require.Contains(t, record.ErrorMessage, "单独刷新认证")
	require.Equal(t, 1, requests)
	require.Zero(t, codeAssist.calls)
	require.Zero(t, repo.writes)
}

func TestIntelligentRefreshAPIBlocksBeforeExecutorAndRepository(t *testing.T) {
	repo := &intelligentAuthRepo{}
	cache := &intelligentAuthCache{}
	executor := &intelligentAuthExecutor{}
	api := NewOAuthRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(intelligentAuthContext(), &Account{ID: 90}, executor, time.Minute)
	require.ErrorIs(t, err, errIntelligentCredentialRefreshRequired)
	require.Nil(t, result)
	require.Zero(t, executor.calls)
	require.Zero(t, repo.reads)
	require.Zero(t, repo.writes)
	require.Zero(t, cache.reads)
}

func TestIntelligentGrokManualTestUsesExistingTokenAndHonorsProxy(t *testing.T) {
	account := &Account{ID: 91, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{
		"access_token": "existing", "expires_at": time.Now().Add(time.Minute).Format(time.RFC3339),
	}}
	provider := NewGrokTokenProvider(nil, nil)
	token, err := provider.GetAccessTokenForManualTest(intelligentAuthContext(), account)
	require.NoError(t, err)
	require.Equal(t, "existing", token)
	account.Credentials["expires_at"] = time.Now().Add(-time.Minute).Format(time.RFC3339)
	_, err = provider.GetAccessTokenForManualTest(intelligentAuthContext(), account)
	require.ErrorIs(t, err, errIntelligentCredentialRefreshRequired)
	proxyID := int64(1)
	account.ProxyID = &proxyID
	_, err = provider.GetAccessTokenForManualTest(intelligentAuthContext(), account)
	require.ErrorIs(t, err, errGrokOAuthConfiguredProxyMiss)
	_, err = provider.GetAccessToken(intelligentAuthContext(), account)
	require.ErrorIs(t, err, errGrokOAuthConfiguredProxyMiss)
}

func TestIntelligentAgentIdentityNeverRegistersOrReplacesTask(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	account := &Account{ID: 92, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"auth_mode": OpenAIAuthModeAgentIdentity, "agent_runtime_id": key.runtimeID, "agent_private_key": privateKey, "task_id": key.taskID,
	}}
	repo := &intelligentAuthRepo{account: account}
	registerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerCalls++
		fmt.Fprint(w, `{"task_id":"new-task"}`)
	}))
	defer server.Close()
	oldBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = server.URL
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = oldBase })
	ctx := intelligentAuthContext()
	before := shallowCopyMap(account.Credentials)
	headers, err := buildAgentIdentityAuthenticationHeaders(ctx, repo, nil, &sync.Mutex{}, account)
	require.NoError(t, err)
	require.Contains(t, headers.Get("Authorization"), "AgentAssertion ")
	require.ErrorIs(t, ensureAgentIdentityTaskForAccount(ctx, repo, nil, &sync.Mutex{}, account, key.taskID), errIntelligentCredentialRefreshRequired)
	require.Equal(t, before, account.Credentials)
	delete(account.Credentials, "task_id")
	_, err = buildAgentIdentityAuthenticationHeaders(ctx, repo, nil, &sync.Mutex{}, account)
	require.ErrorIs(t, err, errIntelligentCredentialRefreshRequired)
	require.Zero(t, registerCalls)
	require.Zero(t, repo.writes)
}

func TestIntelligentVertexServiceAccountCacheRemainsUsable(t *testing.T) {
	for _, platform := range []string{PlatformGemini, PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			account := &Account{ID: 93, Platform: platform, Type: AccountTypeServiceAccount, Credentials: map[string]any{
				"service_account_json": `{"type":"service_account","project_id":"vertex-test","client_email":"test@example.iam.gserviceaccount.com","private_key":"test-pem"}`,
			}}
			before := shallowCopyMap(account.Credentials)
			cache := &intelligentAuthCache{token: "short-lived-vertex-token"}
			var token string
			var err error
			if platform == PlatformGemini {
				token, err = NewGeminiTokenProvider(nil, cache, nil).GetAccessToken(intelligentAuthContext(), account)
			} else {
				token, err = NewClaudeTokenProvider(nil, cache, nil).GetAccessToken(intelligentAuthContext(), account)
			}
			require.NoError(t, err)
			require.Equal(t, "short-lived-vertex-token", token)
			require.Equal(t, 1, cache.reads)
			require.Equal(t, before, account.Credentials)
		})
	}
}

func TestIntelligentVertexServiceAccountCanMintEphemeralToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	key := &vertexServiceAccountKey{ClientEmail: "test@example.iam.gserviceaccount.com", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}))}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.NoError(t, r.ParseForm())
		require.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", r.PostForm.Get("grant_type"))
		require.NotEmpty(t, r.PostForm.Get("assertion"))
		fmt.Fprint(w, `{"access_token":"ephemeral-token","token_type":"Bearer","expires_in":3600}`)
	}))
	defer server.Close()
	key.TokenURI = server.URL
	before := *key
	token, ttl, err := exchangeVertexServiceAccountToken(intelligentAuthContext(), key, "")
	require.NoError(t, err)
	require.Equal(t, "ephemeral-token", token)
	require.Equal(t, 55*time.Minute, ttl)
	require.Equal(t, 1, requests)
	require.Equal(t, before, *key)
}
