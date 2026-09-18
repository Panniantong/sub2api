//go:build unit

package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 全链路顺序验证:在 buildUpstreamRequest 真实代码路径上,turn-state 覆写
// 必须活到最后(不被 guard / identity / fingerprint 任何一步盖掉)。
func TestTurnStateOverride_SurvivesFullBuildUpstreamRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-6-astra","input":"hi","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	// 客户端回带一个别的号的 turn-state(应被 guard 剥离),我们配的要胜出
	c.Request.Header.Set("x-codex-turn-state", "foreign-blob-from-other-account")

	acc := &Account{
		ID: 42, Name: "oauth-tts", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"access_token":        "test-token",
			"chatgpt_account_id":  "acct-123",
			"header_override_enabled": true,
			"header_overrides":    map[string]any{"x-codex-turn-state": "our-pinned-healthy-blob"},
		},
	}
	svc := &OpenAIGatewayService{}

	req, err := svc.buildUpstreamRequest(c.Request.Context(), c, acc, body, "tok", true, "", false)
	if err != nil {
		t.Fatalf("buildUpstreamRequest err: %v", err)
	}
	require.Equal(t, "our-pinned-healthy-blob", getHeaderRaw(req.Header, "x-codex-turn-state"),
		"pinned override must survive to final outbound request")
	// 身份头仍被 identity enforcement 收口,不被覆写污染
	require.NotEmpty(t, req.Header.Get("user-agent"))
	require.NotEmpty(t, req.Header.Get("originator"))
}
