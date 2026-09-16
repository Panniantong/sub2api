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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMode1V3MigrationPreservesOriginalSnapshotAndIdentity(t *testing.T) {
	a := mode1TestAccount(1)
	prev := map[string]any{"concurrency": 32, "codex_fingerprint_mode": "session", "enable_tls_fingerprint": true, "tls_fingerprint_builtin": "nodejs22", "tls_fingerprint_profile_id": 9}
	mode1Marker(a)["prev"] = prev
	a.Concurrency = 99 // identity migration preserves the editable account ceiling
	seed, _ := codexFingerprintSeed(a.Extra)
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: a}}
	svc := NewAntiDegradeService(&adminServiceImpl{accountRepo: repo})
	p, err := svc.PreviewMode(context.Background(), 1, AntiDegradeMode1)
	require.NoError(t, err)
	require.True(t, p.Eligible)
	require.Equal(t, 2, p.PolicyVersion)
	require.Contains(t, p.Reason, "升级")
	updated, err := svc.ApplyAntiDegradeMode(context.Background(), 1, AntiDegradeMode1)
	require.NoError(t, err)
	require.Equal(t, 3, mode1Int(mode1Marker(updated)["policy_version"]))
	require.Equal(t, prev, antiDegradePrev(updated))
	require.Equal(t, seed, requireValidCodexFingerprintSeed(t, updated.Extra))
	require.False(t, updated.IsTLSFingerprintEnabled())
	require.Equal(t, 99, updated.Mode1EffectiveConcurrency())
	profile, err := resolveMode1TLSProfile(updated)
	require.NoError(t, err)
	require.Nil(t, profile)
	again, err := svc.ApplyAntiDegradeMode(context.Background(), 1, AntiDegradeMode1)
	require.NoError(t, err)
	require.Equal(t, prev, antiDegradePrev(again))
	restored, err := svc.Revert(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 99, restored.Concurrency)
	require.Equal(t, "session", restored.Extra["codex_fingerprint_mode"])
	require.Equal(t, "nodejs22", restored.Extra["tls_fingerprint_builtin"])
	require.Equal(t, 9, restored.Extra["tls_fingerprint_profile_id"])
	require.Equal(t, seed, requireValidCodexFingerprintSeed(t, restored.Extra))
	require.False(t, antiDegradeEnabled(restored))
}

func TestMode1InvalidVersionCanBeExplicitlyRestored(t *testing.T) {
	a := mode1TestAccount(1)
	mode1Marker(a)["policy_version"] = 999
	mode1Marker(a)["prev"] = map[string]any{"concurrency": 7, "codex_fingerprint_mode": "off"}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: a}}
	svc := NewAntiDegradeService(&adminServiceImpl{accountRepo: repo})
	_, err := resolveMode1TLSProfile(a)
	require.ErrorContains(t, err, "版本")
	p, err := svc.PreviewMode(context.Background(), 1, AntiDegradeMode1)
	require.NoError(t, err)
	require.False(t, p.Eligible)
	restored, err := svc.Revert(context.Background(), 1)
	require.NoError(t, err)
	require.False(t, antiDegradeEnabled(restored))
	require.Equal(t, 4, restored.Concurrency)
}

func TestMode1DamagedModeDoesNotSilentlyDisableProtection(t *testing.T) {
	for _, mode := range []any{nil, "unknown"} {
		a := mode1TestAccount(1)
		mode1Marker(a)["mode"] = mode
		_, err := resolveMode1TLSProfile(a)
		require.ErrorContains(t, err, "标记")
	}
}

func TestMode1V3StandardTransportWithTLSGloballyDisabled(t *testing.T) {
	a := mode1TestAccount(1)
	mode1Marker(a)["policy_version"] = 3
	a.Concurrency = 99
	u := &mode1HTTPRecorder{}
	// Installing/configuring a manager is not an unsupported-transport error.
	manager := &PluginManager{}
	gw := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: u, pluginManager: manager}
	req := httptest.NewRequest(http.MethodPost, "https://localhost/responses", nil)
	r, err := gw.doOpenAIUpstream(req, "", a)
	require.NoError(t, err)
	r.Body.Close()
	tester := &AccountTestService{cfg: &config.Config{}, httpUpstream: u, pluginManager: manager}
	r, err = tester.doOpenAIAccountTestUpstream(req, "", a, true)
	require.NoError(t, err)
	r.Body.Close()
	require.Equal(t, 2, u.normal)
	require.Zero(t, u.tls)
	require.Equal(t, 99, u.cap)
}

// These are the actual request shapes that failed before any upstream dial in
// v2, plus multi-turn/tool/reasoning variants. Exercise the real Forward path.
func TestMode1ForwardRegressionV2AndV3(t *testing.T) {
	bodies := []string{
		`{"model":"gpt-5.2","input":"Return OK","stream":true}`,
		`{"model":"gpt-5.2","input":[{"role":"user","content":[{"type":"input_text","text":"Return OK"}]}],"max_output_tokens":100,"stream":true}`,
		`{"model":"gpt-5.4-high","input":"Return OK","reasoning":{"effort":"minimal"},"stream":true}`,
		`{"model":"gpt-5.2","input":[{"role":"system","content":"Keep context"},{"role":"user","content":"one"},{"role":"assistant","content":"two"},{"type":"reasoning","id":"rs_old","encrypted_content":"opaque"},{"role":"user","content":"continue"}],"reasoning":{"effort":"high"},"stream":true}`,
		`{"model":"gpt-5.2","input":[{"type":"function_call","call_id":"call_123","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_123","output":"answer"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"stream":true}`,
	}
	for _, version := range []int{2, 3} {
		for _, passthrough := range []bool{false, true} {
			for index, raw := range bodies {
				t.Run(fmt.Sprintf("v%d/passthrough=%t/body=%d", version, passthrough, index), func(t *testing.T) {
					gin.SetMode(gin.TestMode)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					body := []byte(raw)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
					c.Request.Header.Set("User-Agent", "local-test/1.0")
					c.Request.Header.Set("session-id", "regression-session")
					u := &mode1ForwardRecorder{httpUpstreamRecorder: httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"test\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}]}}\n\ndata: [DONE]\n\n"))}}}
					a := mode1TestAccount(1)
					mode1Marker(a)["policy_version"] = version
					a.Schedulable = true
					a.Credentials = map[string]any{"access_token": "local-dummy", "chatgpt_account_id": "dummy"}
					a.Extra["openai_passthrough"] = passthrough
					gw := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: u, toolCorrector: NewCodexToolCorrector()}
					_, err := gw.Forward(context.Background(), c, a, body)
					require.NoError(t, err)
					require.Equal(t, 1, u.ordinary, "must reach the proven standard upstream transport")
					require.NoError(t, validateMode1RequestIntegrityForAccount(a, body, u.lastBody))
				})
			}
		}
	}
}
