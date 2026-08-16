package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexExhaustedResetAtFromExtraUpdates(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	reset5h := now.Add(4 * time.Hour)
	reset7d := now.Add(6 * 24 * time.Hour)

	got := codexExhaustedResetAtFromExtraUpdates(map[string]any{
		"codex_5h_used_percent": 100,
		"codex_5h_reset_at":     reset5h.Format(time.RFC3339),
		"codex_7d_used_percent": "100",
		"codex_7d_reset_at":     reset7d.Format(time.RFC3339),
	}, now)

	require.NotNil(t, got)
	require.Equal(t, reset7d, *got)
}

func TestCodexExhaustedResetAtFromExtraUpdatesIgnoresNonExhaustedOrExpired(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)

	got := codexExhaustedResetAtFromExtraUpdates(map[string]any{
		"codex_5h_used_percent": 99,
		"codex_5h_reset_at":     now.Add(time.Hour).Format(time.RFC3339),
		"codex_7d_used_percent": 100,
		"codex_7d_reset_at":     now.Add(-time.Second).Format(time.RFC3339),
	}, now)

	require.Nil(t, got)
}
