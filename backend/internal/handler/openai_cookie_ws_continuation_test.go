//go:build unit

package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponses_CookieWSIgnoresClientContinuationBeforeForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.RetryBackoffInitialMS = 1
	settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{}}, cfg)
	value, err := settings.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	value.WSEnabled = true
	require.NoError(t, settings.SetOpenAICookieSettings(ctx, value))
	_, err = settings.SetOpenAICodexCookieLibrary(ctx, []service.OpenAICodexCookieLibraryEntry{{Host: "host.example", Cookie: "bound=value", ExpiresAt: time.Now().Add(time.Hour)}})
	require.NoError(t, err)
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test", "chatgpt_account_id": "test"},
		Extra:       map[string]any{"codex_cookie_host": "host.example", "session_id": "test-session"}}
	repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
	concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	})
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, concurrency, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, settings, nil)
	t.Cleanup(gateway.CloseOpenAIWSPool)
	require.True(t, gateway.UsesServerManagedCookieWS(&account))
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	for _, body := range []string{
		`{"model":"gpt-6-astra","stream":false,"input":"hello","previous_response_id":"msg_client_value"}`,
		`{"model":"gpt-6-astra","stream":false,"input":"hello","previous_response_id":"resp_other_user"}`,
		`{"model":"gpt-6-astra","stream":false,"input":[{"type":"function_call_output","call_id":"call_test","output":"ok"}]}`,
	} {
		c, rec := newAstraProFailoverContext(t, body)
		c.Request.Header.Set("session_id", "client-session")
		h.Responses(c)
		// No pool was built deliberately: this must reach the WS acquire path
		// and fail as upstream unavailable, not reject an ignored client ID.
		require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
		require.NotContains(t, rec.Body.String(), "previous_response_id")
	}
}
