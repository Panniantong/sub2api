package service

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAccountProtectionIndependentIntegrityModes(t *testing.T) {
	a := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 4}
	PrepareNewAccountProtection(a)
	require.Equal(t, "legacy", a.ProtectionMode())
	require.Equal(t, "observe", a.RequestIntegrityMode())
	before := []byte(`{"input":[{"role":"user","content":"question"}],"tools":[{"type":"function","name":"important"}]}`)
	after := []byte(`{"input":[{"role":"user","content":"question"}]}`)
	c, _ := gin.CreateTestContext(nil)
	stageMode1Request(c, a, before)
	require.NoError(t, validateMode1StagedRequest(c, a, after), "observation must preserve the existing request path")
	flag, exists := c.Get("request_integrity_difference")
	require.True(t, exists)
	require.Equal(t, true, flag)
	require.Equal(t, "nodejs24", a.Extra["tls_fingerprint_builtin"])
	a.Extra[requestIntegrityModeKey] = "enforce"
	require.Error(t, validateMode1StagedRequest(c, a, after))
	a.Extra[requestIntegrityModeKey] = "off"
	require.NoError(t, validateMode1StagedRequest(c, a, after))
	a.Extra[AntiDegradeMarkerExtraKey] = map[string]any{"enabled": true, "mode": "mode1", "policy_version": 3}
	require.Equal(t, "off", a.RequestIntegrityMode(), "explicit administrator choice overrides the mode1 default")
	delete(a.Extra,requestIntegrityModeKey)
	require.Equal(t,"enforce",a.RequestIntegrityMode(),"existing mode1 defaults remain compatible when no override is saved")
	require.Error(t, validateRequestIntegrityExtra(map[string]any{requestIntegrityModeKey: "unknown"}))
	require.NoError(t, validateRequestIntegrityExtra(map[string]any{requestIntegrityModeKey: nil}))
}
