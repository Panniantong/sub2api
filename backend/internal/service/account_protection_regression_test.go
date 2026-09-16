package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Regression coverage for policy transitions, metadata and real local WSS.
func protectionRegressionServices(a *Account) (*adminServiceImpl, *AntiDegradeService) {
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a}}
	admin := &adminServiceImpl{accountRepo: repo}
	return admin, NewAntiDegradeService(admin)
}

type protectionRegressionFailSecondWrite struct {
	AntiDegradeStore
	writes int
	failAt int
}

func (s *protectionRegressionFailSecondWrite) UpdateAccount(ctx context.Context, id int64, in *UpdateAccountInput) (*Account, error) {
	s.writes++
	if s.writes == s.failAt {
		return nil, errors.New("injected write failure")
	}
	return s.AntiDegradeStore.UpdateAccount(ctx, id, in)
}

func TestProtectionRegression(t *testing.T) {
	ctx := context.Background()
	t.Run("standard_strategy_revert_restores_tls_enabled", func(t *testing.T) {
		a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32,
			Extra: map[string]any{"enable_tls_fingerprint": true, "tls_fingerprint_profile_id": 9}}
		_, svc := protectionRegressionServices(a)
		applied, err := svc.ApplyAntiDegradeMode(ctx, 1, AntiDegradeModeMinimal)
		require.NoError(t, err)
		require.False(t, applied.IsTLSFingerprintEnabled())
		restored, err := svc.Revert(ctx, 1)
		require.NoError(t, err)
		require.True(t, restored.IsTLSFingerprintEnabled(), "restoring a true TLS snapshot must re-enable TLS")
	})
	t.Run("legacy_revert_preserves_custom_tls_profile", func(t *testing.T) {
		a := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32,
			Extra: map[string]any{"enable_tls_fingerprint": true, "tls_fingerprint_profile_id": 9}}
		_, svc := protectionRegressionServices(a)
		_, err := svc.ApplyAntiDegradeMode(ctx, 2, AntiDegradeModeLegacy)
		require.NoError(t, err)
		restored, err := svc.Revert(ctx, 2)
		require.NoError(t, err)
		require.EqualValues(t, 9, restored.GetTLSFingerprintProfileID(), "revert must not delete a pre-existing custom profile")
	})
	t.Run("switch_failure_keeps_original_policy", func(t *testing.T) {
		a := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32}
		PrepareNewAccountProtection(a)
		admin, _ := protectionRegressionServices(a)
		failing := &protectionRegressionFailSecondWrite{AntiDegradeStore: admin, failAt: 1}
		_, err := NewAntiDegradeService(failing).ApplyAntiDegradeMode(ctx, 3, AntiDegradeModeMinimal)
		require.ErrorContains(t, err, "write failure")
		current, err := admin.GetAccount(ctx, 3)
		require.NoError(t, err)
		require.True(t, current.AntiDegradationEnabled(), "failed switch already committed protection=false")
		successful := &protectionRegressionFailSecondWrite{AntiDegradeStore: admin}
		switched, err := NewAntiDegradeService(successful).ApplyAntiDegradeMode(ctx, 3, AntiDegradeModeMinimal)
		require.NoError(t, err)
		require.Equal(t, 1, successful.writes, "switch must publish only the final state")
		require.Equal(t, "minimal_compat", switched.ProtectionMode())
	})
	t.Run("mode2_ordinary_edit_cannot_disable_fingerprint", func(t *testing.T) {
		a := &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32}
		admin, svc := protectionRegressionServices(a)
		_, err := svc.ApplyAntiDegradeMode(ctx, 4, AntiDegradeMode2)
		require.NoError(t, err)
		updated, err := admin.UpdateAccount(ctx, 4, &UpdateAccountInput{Extra: map[string]any{"codex_fingerprint_mode": "off", "enable_tls_fingerprint": false}})
		require.NoError(t, err)
		require.True(t, updated.AntiDegradationEnabled())
		require.Equal(t, codexFingerprintFull, updated.GetCodexFingerprintMode(), "badge stays enabled while ordinary edit removes full identity policy")
	})
	t.Run("generic_can_upgrade_after_selecting_fixed_proxy", func(t *testing.T) {
		a := &Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32,
			Extra: map[string]any{"proxy_mode": "random"}}
		PrepareNewAccountProtection(a)
		require.Equal(t, "generic_v1", a.ProtectionScope())
		admin, svc := protectionRegressionServices(a)
		_, err := admin.UpdateAccount(ctx, 5, &UpdateAccountInput{Extra: map[string]any{"proxy_mode": "fixed"}})
		require.NoError(t, err)
		updated, err := svc.ApplyAntiDegradeMode(ctx, 5, AntiDegradeMode1)
		require.NoError(t, err, "generic is normalized to mode1, so an eligible upgrade is rejected as invalid mode1")
		require.True(t, updated.IsMode1ProtectionEnabled())
	})
	t.Run("openai_only_profile_rejects_gemini", func(t *testing.T) {
		a := &Account{ID: 6, Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 32}
		_, svc := protectionRegressionServices(a)
		_, err := svc.ApplyAntiDegradeMode(ctx, 6, AntiDegradeModeMinimal)
		require.Error(t, err, "registry requires OpenAI OAuth, but the service accepts Gemini and labels it protected")
	})
	t.Run("unknown_strategy_rejected", func(t *testing.T) {
		a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32}
		_, svc := protectionRegressionServices(a)
		_, err := svc.ApplyAntiDegradeMode(ctx, 7, AntiDegradeMode("mode_typo"))
		require.Error(t, err, "unknown mode silently applies mode1")
	})
	t.Run("preview_reports_global_tls_override", func(t *testing.T) {
		a := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32}
		_, svc := protectionRegressionServices(a)
		a, err := svc.ApplyAntiDegradeMode(ctx, 8, AntiDegradeModeLegacy)
		require.NoError(t, err)
		svc.cfg = &config.Config{}
		preview, err := svc.PreviewMode(ctx, 8, AntiDegradeModeLegacy)
		require.NoError(t, err)
		upstream := &mode1HTTPRecorder{}
		gw := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
		resp, err := gw.doOpenAIUpstream(httptest.NewRequest(http.MethodPost, "https://local.invalid/responses", nil), "", a)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, 1, upstream.normal)
		require.Zero(t, upstream.tls)
		require.Equal(t, "nodejs24", preview.Runtime.ConfiguredTLS)
		require.Equal(t, "standard", preview.Runtime.EffectiveTLS)
		require.NotEmpty(t, preview.Runtime.TLSReason)
		require.False(t, preview.Runtime.Observed)
	})
	t.Run("mode1_roundtrip_preserves_original_configuration_control", func(t *testing.T) {
		a := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32,
			Extra: map[string]any{"enable_tls_fingerprint": true, "tls_fingerprint_profile_id": 9}}
		_, svc := protectionRegressionServices(a)
		_, err := svc.ApplyAntiDegradeMode(ctx, 9, AntiDegradeMode1)
		require.NoError(t, err)
		restored, err := svc.Revert(ctx, 9)
		require.NoError(t, err)
		require.True(t, restored.IsTLSFingerprintEnabled())
		require.EqualValues(t, 9, restored.GetTLSFingerprintProfileID())
		require.Equal(t, 32, restored.Concurrency)
	})
}

func TestProtectionRegressionNativeWSSIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []AntiDegradeMode{AntiDegradeMode1, AntiDegradeMode2, AntiDegradeModeLegacy} {
		t.Run(string(mode), func(t *testing.T) {
			capturedHeaders := make(chan http.Header, 1)
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				capturedHeaders <- r.Header.Clone()
				for {
					kind, payload, err := conn.Read(r.Context())
					if err != nil {
						return
					}
					if conn.Write(r.Context(), kind, payload) != nil {
						return
					}
					terminal := []byte(`{"type":"response.completed","response":{"id":"resp_local","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}}`)
					if conn.Write(r.Context(), coderws.MessageText, terminal) != nil {
						return
					}
				}
			}))
			defer upstream.Close()
			dialer := &mode1V3StandardTestDialer{target: "wss" + strings.TrimPrefix(upstream.URL, "https"), client: upstream.Client(), protected: make(chan bool, 4)}
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.TLSFingerprint.Enabled = false
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			a := &Account{ID: 72, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 4,
				Credentials: map[string]any{"chatgpt_account_id": "00000000-0000-4000-8000-000000000072"}}
			_, adminSvc := protectionRegressionServices(a)
			a, err := adminSvc.ApplyAntiDegradeMode(context.Background(), 72, mode)
			require.NoError(t, err)
			a.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModePassthrough
			svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
			svc.openaiWSPassthroughDialer = dialer
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, a)
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.2","input":"Return OK","client_metadata":{"installation_id":"client-device","session_id":"client-session","thread_id":"client-thread"}}`)
			defer client.CloseNow()
			_, output, err := client.Read(ctx)
			require.NoError(t, err)
			_, _, err = client.Read(ctx)
			require.NoError(t, err)
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.2","input":"Second turn","client_metadata":{"installation_id":"client-device","session_id":"client-session","thread_id":"client-thread"}}`)))
			_, second, err := client.Read(ctx)
			require.NoError(t, err)
			_, _, err = client.Read(ctx)
			require.NoError(t, err)
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case <-serverErr:
			case <-ctx.Done():
				t.Fatal("local relay did not close")
			}
			headers := <-capturedHeaders
			expected := resolveCodexFingerprintIDs(a, "client-session", a.GetCodexFingerprintMode())
			require.NotNil(t, expected)
			t.Logf("strategy=%s header_installation=%q body_installation=%q expected=%q", mode, headers.Get("x-codex-installation-id"), gjson.GetBytes(output, "client_metadata.installation_id").String(), expected.installationID)
			require.Equal(t, expected.installationID, gjson.GetBytes(output, "client_metadata.installation_id").String(), "native WSS body must carry the same policy identity as HTTP")
			require.Equal(t, expected.installationID, headers.Get("x-codex-installation-id"))
			require.Equal(t, expected.installationID, gjson.GetBytes(second, "client_metadata.installation_id").String())
			require.Equal(t, gjson.GetBytes(output, "client_metadata.thread_id").String(), gjson.GetBytes(second, "client_metadata.thread_id").String())
		})
	}
}

func TestProtectionDeviceStrategiesKeepPoolConversationsSeparate(t *testing.T) {
	for _, mode := range []AntiDegradeMode{AntiDegradeModeMinimal, AntiDegradeModeTLSNode24} {
		t.Run(string(mode), func(t *testing.T) {
			_, svc := protectionRegressionServices(&Account{ID: 91, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 4})
			a, err := svc.ApplyAntiDegradeMode(context.Background(), 91, mode)
			require.NoError(t, err)
			first := http.Header{"X-Codex-Installation-Id": {"same-device"}, "Session-Id": {"session-a"}, "Thread-Id": {"thread-a"}}
			second := first.Clone()
			second.Set("session-id", "session-b")
			second.Set("thread-id", "thread-b")
			require.NotEqual(t, normalizeOpenAIWSHandshakeCompatibility(a, first), normalizeOpenAIWSHandshakeCompatibility(a, second))
		})
	}
}
