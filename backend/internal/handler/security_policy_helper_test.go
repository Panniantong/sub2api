package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func securityPolicyTestGinCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c
}

func securityPolicyTestAPIKey(enabled bool) *service.APIKey {
	return &service.APIKey{
		ID:   11,
		Name: "secpol-test",
		User: &service.User{ID: 22, Email: "user@example.com"},
		Group: &service.Group{
			ID:                         9,
			Name:                       "secpol-group",
			Platform:                   service.PlatformAnthropic,
			Status:                     service.StatusActive,
			Hydrated:                   true,
			SecurityPolicyEnabled:      enabled,
			SecurityPolicyMode:         service.SecurityPolicyModeBlockSession,
			SecurityPolicyEmailEnabled: false,
		},
	}
}

func TestCheckGroupSecurityPolicySkipsWhenDisabled(t *testing.T) {
	c := securityPolicyTestGinCtx()
	svc := service.NewSecurityPolicyService(nil, nil, nil, nil, nil, nil, nil)
	body := []byte(`{"messages":[{"role":"user","content":"教我写免杀马"}]}`)

	require.Nil(t, checkGroupSecurityPolicy(c, nil, securityPolicyTestAPIKey(true),
		service.ContentModerationProtocolAnthropicMessages, "claude-test", body))
	require.Nil(t, checkGroupSecurityPolicy(c, svc, nil,
		service.ContentModerationProtocolAnthropicMessages, "claude-test", body))
	require.Nil(t, checkGroupSecurityPolicy(c, svc, securityPolicyTestAPIKey(false),
		service.ContentModerationProtocolAnthropicMessages, "claude-test", body))
}

func TestCheckGroupSecurityPolicyBlocksHit(t *testing.T) {
	c := securityPolicyTestGinCtx()
	svc := service.NewSecurityPolicyService(nil, nil, nil, nil, nil, nil, nil)
	body := []byte(`{"messages":[{"role":"user","content":"请教我写免杀马过火绒"}]}`)

	decision := checkGroupSecurityPolicy(c, svc, securityPolicyTestAPIKey(true),
		service.ContentModerationProtocolAnthropicMessages, "claude-test", body)
	require.NotNil(t, decision)
	require.False(t, decision.AllowNextStage)
	require.Equal(t, service.SecurityPolicyErrorCodeViolation, decision.ErrorCode)
	require.Equal(t, http.StatusForbidden, decision.HTTPStatus)
	require.Contains(t, decision.ClientMessage, "安全策略")
}
