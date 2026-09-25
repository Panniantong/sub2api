//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObserveOpenAICyberSessionIdentity(t *testing.T) {
	before := SnapshotOpenAICyberSessionIdentityMetrics()

	ObserveOpenAICyberSessionIdentity(OpenAIClientSessionIdentityMetadata{Status: OpenAIClientSessionIdentityResolved}, false, false, false)
	ObserveOpenAICyberSessionIdentity(OpenAIClientSessionIdentityMetadata{Status: OpenAIClientSessionIdentityMissing}, true, false, false)
	ObserveOpenAICyberSessionIdentity(OpenAIClientSessionIdentityMetadata{Status: OpenAIClientSessionIdentityConflict}, false, true, true)
	ObserveOpenAICyberSessionIdentity(OpenAIClientSessionIdentityMetadata{Status: OpenAIClientSessionIdentityInvalid}, false, true, false)

	after := SnapshotOpenAICyberSessionIdentityMetrics()
	require.Equal(t, before.Total+4, after.Total)
	require.Equal(t, before.Resolved+1, after.Resolved)
	require.Equal(t, before.Missing+1, after.Missing)
	require.Equal(t, before.Conflict+1, after.Conflict)
	require.Equal(t, before.Invalid+1, after.Invalid)
	require.Equal(t, before.Inherited+1, after.Inherited)
	require.Equal(t, before.StrictRejected+2, after.StrictRejected)
	require.Equal(t, before.WSIdentitySwap+1, after.WSIdentitySwap)
}
