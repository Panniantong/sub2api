package dto

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMode1SeedStaysServerManaged(t *testing.T) {
	a := &service.Account{ID: 1, Extra: map[string]any{"codex_fingerprint_seed": "11111111-1111-4111-8111-111111111111", "codex_fingerprint_mode": "device", "anti_degrade": map[string]any{"enabled": true}}}
	out := AccountFromService(a)
	require.NotContains(t, out.Extra, "codex_fingerprint_seed")
	require.Contains(t, a.Extra, "codex_fingerprint_seed")
	require.Equal(t, "device", out.Extra["codex_fingerprint_mode"])
}
