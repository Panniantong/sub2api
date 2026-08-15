package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExcludeIneligibleCodexWorkModeSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, _ = service.NormalizeCodexWorkModeRequest(c, []byte(`{"model":"gpt-5.6-sol-wm"}`), "gpt-5.6-sol-wm", true)

	released := 0
	excluded := map[int64]struct{}{}
	apiKeySelection := &service.AccountSelectionResult{
		Account:     &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
		ReleaseFunc: func() { released++ },
	}
	require.True(t, excludeIneligibleCodexWorkModeSelection(c, apiKeySelection, excluded))
	require.Equal(t, 1, released)
	require.Contains(t, excluded, int64(41))
	require.True(t, codexWorkModeOAuthSelectionExhausted(c, nil))
	require.False(t, codexWorkModeOAuthSelectionExhausted(c, &service.UpstreamFailoverError{}))

	oauthSelection := &service.AccountSelectionResult{
		Account: &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
	}
	require.False(t, excludeIneligibleCodexWorkModeSelection(c, oauthSelection, excluded))
}

func TestPlainCodexWorkModeDoesNotExcludeAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, _ = service.NormalizeCodexWorkModeRequest(c, []byte(`{"model":"gpt-5.6-sol"}`), "gpt-5.6-sol", false)
	selection := &service.AccountSelectionResult{
		Account: &service.Account{ID: 43, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
	}
	require.False(t, excludeIneligibleCodexWorkModeSelection(c, selection, map[int64]struct{}{}))
}
