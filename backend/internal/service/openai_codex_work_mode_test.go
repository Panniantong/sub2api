package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResolveCodexWorkMode(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		disabled   bool
		wantBase   string
		wantEnable bool
		wantForce  bool
	}{
		{name: "alias defaults on", model: "gpt-5.6", wantBase: "gpt-5.6-sol", wantEnable: true},
		{name: "sol defaults on", model: "gpt-5.6-sol", wantBase: "gpt-5.6-sol", wantEnable: true},
		{name: "terra defaults on", model: "gpt-5.6-terra", wantBase: "gpt-5.6-terra", wantEnable: true},
		{name: "luna defaults on", model: "gpt-5.6-luna", wantBase: "gpt-5.6-luna", wantEnable: true},
		{name: "default can be disabled", model: "gpt-5.6-sol", disabled: true, wantBase: "gpt-5.6-sol"},
		{name: "alias explicit suffix overrides disable", model: "gpt-5.6-wm", disabled: true, wantBase: "gpt-5.6-sol", wantEnable: true, wantForce: true},
		{name: "terra explicit suffix overrides disable", model: "openai/gpt-5.6-terra-wm", disabled: true, wantBase: "gpt-5.6-terra", wantEnable: true, wantForce: true},
		{name: "non 5.6 untouched", model: "gpt-5.5", wantBase: "gpt-5.5"},
		{name: "image untouched", model: "gpt-image-1", wantBase: "gpt-image-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveCodexWorkMode(tt.model, tt.disabled)
			require.Equal(t, tt.wantBase, got.RoutingModel)
			require.Equal(t, tt.wantEnable, got.Enabled)
			require.Equal(t, tt.wantForce, got.Explicit)
		})
	}
}

func TestCodexWorkModeModelNormalizationAndBilling(t *testing.T) {
	require.Equal(t, "gpt-5.6-sol", normalizeCodexModel("gpt-5.6-wm"))
	require.Equal(t, "gpt-5.6-sol", normalizeCodexModel("gpt-5.6-sol-wm"))
	require.Equal(t, "gpt-5.6-terra", normalizeCodexModel("gpt-5.6-terra-wm"))
	require.Equal(t, "gpt-5.6-luna", normalizeCodexModel("gpt-5.6-luna-wm"))
	require.Contains(t, usageBillingModelCandidates("gpt-5.6-terra-wm"), "gpt-5.6-terra")
}

func TestNormalizeCodexWorkModeRequestForChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"gpt-5.6-sol-wm","messages":[{"role":"user","content":"hello"}]}`)

	decision, normalized := NormalizeCodexWorkModeRequest(c, body, "gpt-5.6-sol-wm", true)

	require.Equal(t, "gpt-5.6-sol", decision.RoutingModel)
	require.True(t, decision.Enabled)
	require.True(t, decision.Explicit)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(normalized, "model").String())
	require.True(t, isCodexWorkModeRequest(c))
	require.Equal(t, "gpt-5.6-sol-wm", codexWorkModeClientModel(c, decision.RoutingModel))
}

func TestNormalizeCodexWorkModeRequestRespectsDefaultDisable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"gpt-5.6-sol","messages":[]}`)

	decision, normalized := NormalizeCodexWorkModeRequest(c, body, "gpt-5.6-sol", true)

	require.Equal(t, "gpt-5.6-sol", decision.RoutingModel)
	require.False(t, decision.Enabled)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(normalized, "model").String())
	require.False(t, isCodexWorkModeRequest(c))
}

func TestNormalizeCodexWorkModeRequestDefaultsPlainChatModelOn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"gpt-5.6","messages":[]}`)

	decision, normalized := NormalizeCodexWorkModeRequest(c, body, "gpt-5.6", false)

	require.Equal(t, "gpt-5.6-sol", decision.RoutingModel)
	require.True(t, decision.Enabled)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(normalized, "model").String())
	require.True(t, isCodexWorkModeRequest(c))
	require.Equal(t, "gpt-5.6", codexWorkModeClientModel(c, decision.RoutingModel))
}

func TestApplyCodexWorkModeIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	BindCodexWorkMode(c, true)

	headers := http.Header{
		"User-Agent": {codexCLIUserAgent},
		"Originator": {"codex-tui"},
		"Version":    {codexCLIVersion},
	}
	applyCodexWorkModeIdentity(c, &Account{Type: AccountTypeOAuth}, headers)

	require.Equal(t, codexWorkModeOriginator, headers.Get("originator"))
	require.Equal(t, codexWorkModeClientName+"/"+codexCLIVersion+codexWorkModeUserAgentSuffix, headers.Get("user-agent"))
	require.Equal(t, codexCLIVersion, headers.Get("version"))
}

func TestApplyCodexWorkModeIdentityDoesNotAffectAPIKeyOrUnmarkedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	BindCodexWorkMode(c, true)

	for _, account := range []*Account{{Type: AccountTypeAPIKey}, {Type: AccountTypeOAuth}} {
		headers := http.Header{"User-Agent": {"client/1.0"}, "Originator": {"client"}, "Version": {"1.0"}}
		if account.Type == AccountTypeOAuth {
			BindCodexWorkMode(c, false)
		}
		applyCodexWorkModeIdentity(c, account, headers)
		require.Equal(t, "client/1.0", headers.Get("user-agent"))
		require.Equal(t, "client", headers.Get("originator"))
		require.Equal(t, "1.0", headers.Get("version"))
	}
}

func TestCodexWorkModeResponseKeepsExplicitClientModel(t *testing.T) {
	svc := &OpenAIGatewayService{}
	line := `data: {"type":"response.completed","response":{"model":"gpt-5.6-terra"}}`
	got := svc.replaceModelInSSELine(line, "gpt-5.6-terra", "gpt-5.6-terra-wm")
	require.Contains(t, got, `"model":"gpt-5.6-terra-wm"`)
}

func TestCodexWorkModeClientModelPreservesPlainAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	BindCodexWorkModeClientModel(c, "gpt-5.6")

	require.Equal(t, "gpt-5.6", codexWorkModeClientModel(c, "gpt-5.6-sol"))
	require.Equal(t, "gpt-5.6-sol", ResolveCodexWorkMode("gpt-5.6", false).RoutingModel)
	require.Contains(t, usageBillingModelCandidates("gpt-5.6"), "gpt-5.6-sol")
}
