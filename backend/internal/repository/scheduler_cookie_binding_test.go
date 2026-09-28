package repository

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSchedulerCookieBindingProjection(t *testing.T) {
	extra := map[string]any{
		"codex_cookie_host":                    "chat.gateway.unified-159.api.openai.com",
		"codex_cookie_host_binding_expires_at": "2026-09-28T10:04:07+08:00",
		"codex_cookie":                         "secret",
		"codex_cookie_host_cooldowns":          map[string]string{"host.example": "2099-01-01T00:00:00Z"},
		"codex_cookie_ws_host":                 "legacy.example",
		"codex_cookie_host_cooldown_until":     "2099-01-01T00:00:00Z",
	}
	projected := filterSchedulerExtra(extra)
	require.Equal(t, extra["codex_cookie_host"], projected["codex_cookie_host"])
	require.Equal(t, extra["codex_cookie_host_binding_expires_at"], projected["codex_cookie_host_binding_expires_at"])
	require.NotContains(t, projected, "codex_cookie")
	require.Equal(t, extra["codex_cookie_host_cooldowns"], projected["codex_cookie_host_cooldowns"])
	require.Equal(t, extra["codex_cookie_ws_host"], projected["codex_cookie_ws_host"])
	require.Equal(t, extra["codex_cookie_host_cooldown_until"], projected["codex_cookie_host_cooldown_until"])
	require.Empty(t, filterSchedulerExtra(map[string]any{"codex_cookie_host": nil}))
}
