package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func mode1TestAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 4,
		Extra: map[string]any{"codex_fingerprint_seed": newCodexFingerprintSeed(), "codex_fingerprint_mode": "device", "enable_tls_fingerprint": true, "tls_fingerprint_builtin": "nodejs24",
			"anti_degrade": map[string]any{"enabled": true, "mode": "mode1", "policy_version": 2, "max_concurrency": 4}}}
}

func TestMode1IdentityIsUniquePersistentAndDoesNotMergeThreads(t *testing.T) {
	ctx := context.Background()
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{}}
	admin := &adminServiceImpl{accountRepo: repo}
	svc := NewAntiDegradeService(admin)
	seen := map[string]bool{}
	for id := int64(1); id <= 32; id++ {
		repo.accounts[id] = &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 2, Extra: map[string]any{"custom": "keep"}}
		a, err := svc.ApplyAntiDegradeMode(ctx, id, AntiDegradeMode1)
		require.NoError(t, err)
		seed := requireValidCodexFingerprintSeed(t, a.Extra)
		require.False(t, seen[seed])
		seen[seed] = true
		// Simulate the JSON representation stored by the database and a new service instance.
		stored, err := json.Marshal(a)
		require.NoError(t, err)
		var loaded Account
		require.NoError(t, json.Unmarshal(stored, &loaded))
		repo.accounts[id] = &loaded
		updated, err := admin.UpdateAccount(ctx, id, &UpdateAccountInput{Name: "renamed", Extra: map[string]any{"custom": "edited"}})
		require.NoError(t, err)
		require.Equal(t, seed, requireValidCodexFingerprintSeed(t, updated.Extra))
		require.True(t, isMode1ProtectionEnabled(updated))
		for _, session := range []string{"session-a", "session-b"} {
			headers := http.Header{}
			headers.Set("session-id", session)
			headers.Set("thread-id", "thread-"+session)
			ids := resolveCodexFingerprintIDsFromRequest(updated, headers)
			applyCodexFingerprintHeaders(headers, ids)
			require.NotEmpty(t, headers.Get("x-codex-installation-id"))
			require.Equal(t, session, headers.Get("session-id"))
			require.Equal(t, "thread-"+session, headers.Get("thread-id"))
		}
		a, err = NewAntiDegradeService(admin).RevertAntiDegrade(ctx, id)
		require.NoError(t, err)
		require.False(t, antiDegradeEnabled(a))
		require.Equal(t, seed, requireValidCodexFingerprintSeed(t, a.Extra))
		require.Equal(t, "edited", a.Extra["custom"])
		a, err = svc.ApplyAntiDegradeMode(ctx, id, AntiDegradeMode1)
		require.NoError(t, err)
		require.Equal(t, seed, requireValidCodexFingerprintSeed(t, a.Extra))
	}
}

func TestMode1RuntimeRejectsConfigurationDrift(t *testing.T) {
	a := mode1TestAccount(1)
	p, err := resolveMode1TLSProfile(a)
	require.NoError(t, err)
	require.NotNil(t, p)
	a.Concurrency = 100
	require.Equal(t, 100, a.Mode1EffectiveConcurrency())
	a.Concurrency = 1
	require.Equal(t, 1, a.Mode1EffectiveConcurrency())
	a.Extra["enable_tls_fingerprint"] = false
	p, err = resolveMode1TLSProfile(a)
	require.NoError(t, err)
	require.Nil(t, p)
	a.Extra["enable_tls_fingerprint"] = true
	delete(a.Extra, "codex_fingerprint_seed")
	_, err = resolveMode1TLSProfile(a)
	require.Error(t, err)
}

func TestMode1InvalidMarkerDoesNotDowngrade(t *testing.T) {
	for _, version := range []any{"2", 999, nil} {
		a := mode1TestAccount(1)
		mode1Marker(a)["policy_version"] = version
		p, err := resolveMode1TLSProfile(a)
		require.Error(t, err)
		require.Nil(t, p)
	}
}

func TestMode1DifferentAccountsWithDuplicatedLegacyDeviceStillDiffer(t *testing.T) {
	a, b := mode1TestAccount(1), mode1TestAccount(2)
	a.Extra["openai_device_id"] = "legacy-shared"
	b.Extra["openai_device_id"] = "legacy-shared"
	require.Equal(t, a.GetOpenAIDeviceID(), b.GetOpenAIDeviceID())
	seedA, _ := codexFingerprintSeed(a.Extra)
	seedB, _ := codexFingerprintSeed(b.Extra)
	require.NotEqual(t, resolveConvergedInstallationID(a, seedA), resolveConvergedInstallationID(b, seedB))
	require.Equal(t, resolveConvergedInstallationID(a, seedA), resolveConvergedInstallationID(a, seedA))
}

func TestMode1PartialAndBulkUpdatesCannotDisablePolicy(t *testing.T) {
	a := mode1TestAccount(1)
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: a}}
	admin := &adminServiceImpl{accountRepo: repo}
	err := admin.UpdateAccountExtra(context.Background(), 1, map[string]any{"anti_degrade": nil})
	require.ErrorContains(t, err, "专用")
	_, err = admin.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: map[string]any{"anti_degrade": nil}})
	require.ErrorContains(t, err, "模式一")
	require.True(t, isMode1ProtectionEnabled(repo.accounts[1]))
	require.Empty(t, repo.bulkUpdates)
}

type mode1HTTPRecorder struct {
	normal, tls int
	profile     *tlsfingerprint.Profile
	cap         int
	err         error
}

func (r *mode1HTTPRecorder) Do(_ *http.Request, _ string, _ int64, cap int) (*http.Response, error) {
	r.normal++
	r.cap = cap
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}
func (r *mode1HTTPRecorder) DoWithTLS(_ *http.Request, _ string, _ int64, cap int, p *tlsfingerprint.Profile) (*http.Response, error) {
	r.tls++
	r.profile = p
	r.cap = cap
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

func TestMode1HTTPAndAccountTestUseSameTLSWithoutFallback(t *testing.T) {
	ctx := context.Background()
	a := mode1TestAccount(1)
	a.Concurrency = 100
	u := &mode1HTTPRecorder{}
	gw := &OpenAIGatewayService{httpUpstream: u}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://localhost/responses", nil)
	r, err := gw.doOpenAIUpstream(req, "", a)
	require.NoError(t, err)
	r.Body.Close()
	require.Equal(t, 100, u.cap)
	require.NotNil(t, u.profile)
	profileKey := u.profile.CacheKey()
	tester := &AccountTestService{httpUpstream: u}
	r, err = tester.doOpenAIAccountTestUpstream(req, "", a, false)
	require.NoError(t, err)
	r.Body.Close()
	require.Equal(t, profileKey, u.profile.CacheKey())
	u.err = errors.New("TLS failed")
	_, err = gw.doOpenAIUpstream(req, "", a)
	require.Error(t, err)
	require.Zero(t, u.normal)
	gw.cfg = &config.Config{}
	u.err = nil
	r, err = gw.doOpenAIUpstream(req, "", a)
	require.NoError(t, err)
	r.Body.Close()
	require.Equal(t, 1, u.normal)
	require.Equal(t, 100, u.cap)
}

func TestMode1UsesExistingPluginRouting(t *testing.T) {
	manager := &PluginManager{}
	manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100, unavailable: "test"})
	u := &mode1HTTPRecorder{}
	gw := &OpenAIGatewayService{httpUpstream: u, pluginManager: manager}
	req, _ := http.NewRequest(http.MethodPost, "https://localhost/responses", nil)
	_, err := gw.doOpenAIUpstream(req, "", mode1TestAccount(1))
	require.Error(t, err) // the configured plugin is actually unavailable
	require.NotContains(t, err.Error(), "未声明支持模式一")
	require.Zero(t, u.tls)
	require.Zero(t, u.normal)
}

func TestMode1IntegrityPreservesReasoningToolsAndBudget(t *testing.T) {
	original := []byte(`{"model":"test-model","reasoning":{"effort":"high"},"input":[{"type":"reasoning","encrypted_content":"opaque"},{"type":"function_call_output","call_id":"call-1","output":"result"}],"max_output_tokens":2000,"tools":[{"type":"function","name":"read"}]}`)
	require.NoError(t, validateMode1RequestIntegrity(original, original))
	for _, field := range []string{"reasoning", "input", "max_output_tokens", "tools", "model"} {
		var payload map[string]any
		require.NoError(t, json.Unmarshal(original, &payload))
		delete(payload, field)
		out, _ := json.Marshal(payload)
		require.ErrorContains(t, validateMode1RequestIntegrity(original, out), field)
	}
}

type mode1ForwardRecorder struct {
	httpUpstreamRecorder
	template *tlsfingerprint.Profile
	ordinary int
}

func (r *mode1ForwardRecorder) Do(req *http.Request, p string, id int64, n int) (*http.Response, error) {
	r.ordinary++
	return r.httpUpstreamRecorder.Do(req, p, id, n)
}
func (r *mode1ForwardRecorder) DoWithTLS(req *http.Request, p string, id int64, n int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	r.template = profile
	return r.httpUpstreamRecorder.Do(req, p, id, n)
}

func TestMode1ForwardPreservesPayloadThroughNormalAndPassthrough(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := []byte(`{"model":"gpt-5.2","stream":true,"reasoning":{"effort":"high"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Return OK"}]}]}`)
			c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
			if !passthrough {
				c.Request.Header.Set("User-Agent", "local-test/1.0")
			}
			c.Request.Header.Set("session-id", "test-session")
			c.Request.Header.Set("thread-id", "test-thread")
			u := &mode1ForwardRecorder{httpUpstreamRecorder: httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"test\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}]}}\n\ndata: [DONE]\n\n"))}}}
			a := mode1TestAccount(1)
			a.Name = "mode1"
			a.Schedulable = true
			a.Credentials = map[string]any{"access_token": "local-dummy", "chatgpt_account_id": "dummy"}
			a.Extra["openai_passthrough"] = passthrough
			cfg := &config.Config{}
			cfg.Gateway.TLSFingerprint.Enabled = true
			gw := &OpenAIGatewayService{cfg: cfg, httpUpstream: u, toolCorrector: NewCodexToolCorrector()}
			_, err := gw.Forward(context.Background(), c, a, body)
			require.NoError(t, err)
			require.Zero(t, u.ordinary)
			require.NotNil(t, u.template)
			require.NoError(t, validateMode1RequestIntegrity(body, u.lastBody))
			require.NotEmpty(t, u.lastReq.Header.Get("x-codex-installation-id"))
			require.NotEmpty(t, u.lastReq.Header.Get("session-id"))
		})
	}
}
