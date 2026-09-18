//go:build unit

package service

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mkBlob(prefix string, total int) string {
	return prefix + strings.Repeat("a", total-len(prefix))
}

// team 号 turn-state blob 比 292 长,放宽后必须被接受(鈊: team 不是 292 导致全挂)。
func TestCodexTicket_TeamLongerBlobAccepted(t *testing.T) {
	now := time.Now()

	teamBlob := mkBlob("gAAAAA", 380)
	tk := &openAICodexTicket{State: teamBlob, Length: len(teamBlob), CapturedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.True(t, tk.valid(now, 292), "team-length blob (380) must be accepted")

	plus := &openAICodexTicket{State: mkBlob("gAAAAA", 292), Length: 292, CapturedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.True(t, plus.valid(now, 292), "plus 292 must still be accepted")

	bad := &openAICodexTicket{State: mkBlob("XXXXXX", 380), Length: 380, CapturedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.False(t, bad.valid(now, 292), "wrong prefix must be rejected")

	short := &openAICodexTicket{State: mkBlob("gAAAAA", 106), Length: 106, CapturedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.False(t, short.valid(now, 292), "too-short blob must be rejected")

	over := &openAICodexTicket{State: mkBlob("gAAAAA", 600), Length: 600, CapturedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.False(t, over.valid(now, 292), "over-max blob must be rejected")
}
