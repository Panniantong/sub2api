package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const codexWorkModeOAuthExcludedContextKey = "openai_codex_work_mode_oauth_excluded"

// excludeIneligibleCodexWorkModeSelection prevents an explicit -wm request
// from silently succeeding through an API-key account without Work identity.
func excludeIneligibleCodexWorkModeSelection(
	c *gin.Context,
	selection *service.AccountSelectionResult,
	excluded map[int64]struct{},
) bool {
	if selection == nil || service.CodexWorkModeAccountEligible(c, selection.Account) {
		return false
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	if selection.Account != nil && excluded != nil {
		excluded[selection.Account.ID] = struct{}{}
	}
	if c != nil {
		c.Set(codexWorkModeOAuthExcludedContextKey, true)
	}
	return true
}

func codexWorkModeOAuthSelectionExhausted(c *gin.Context, lastFailoverErr *service.UpstreamFailoverError) bool {
	if c == nil || lastFailoverErr != nil {
		return false
	}
	value, _ := c.Get(codexWorkModeOAuthExcludedContextKey)
	excluded, _ := value.(bool)
	return excluded
}
