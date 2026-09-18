//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 验证羊毛GPT方案核心:OpenAI OAuth 账号可覆写 x-codex-turn-state 并落到出站头。
func TestTurnStateOverride_OpenAIOAuthAppliesTurnStateHeader(t *testing.T) {
	acc := &Account{
		ID:       999,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"header_override_enabled": true,
			"header_overrides": map[string]any{
				"x-codex-turn-state": "test-turn-state-blob-abc123",
			},
		},
	}
	require.True(t, acc.IsHeaderOverrideEligible(), "OpenAI OAuth must be eligible after change")
	require.True(t, acc.IsHeaderOverrideEnabled())

	h := http.Header{}
	// 模拟客户端回带了一个属于别的号的 turn-state(会被 guard 剥离),然后覆写应补上我们的
	h.Set("X-Codex-Turn-State", "other-account-blob")
	acc.ApplyHeaderOverrides(h)
	require.Equal(t, "test-turn-state-blob-abc123", getHeaderRaw(h, "x-codex-turn-state"),
		"override must win over any pre-existing casing variant")

	// metadata 仍然被禁(本次不放开)
	acc2 := &Account{
		ID: 1000, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{
			"header_override_enabled": true,
			"header_overrides":        map[string]any{"x-codex-turn-metadata": "should-be-blocked"},
		},
	}
	h2 := http.Header{}
	acc2.ApplyHeaderOverrides(h2)
	require.Empty(t, getHeaderRaw(h2, "x-codex-turn-metadata"), "metadata must stay blocked")

	// 未开启开关的 OAuth 号不受影响
	acc3 := &Account{ID: 1001, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "tok"}}
	h3 := http.Header{}
	acc3.ApplyHeaderOverrides(h3)
	require.Empty(t, getHeaderRaw(h3, "x-codex-turn-state"), "no override configured -> no header")
}
