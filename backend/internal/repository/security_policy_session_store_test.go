package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newSecurityPolicySessionStoreTest(t *testing.T) *securityPolicySessionStore {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	return &securityPolicySessionStore{rdb: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
}

func TestSecurityPolicySessionStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newSecurityPolicySessionStoreTest(t)
	scope := "secpol:scope:9:11"

	found, err := store.FindBlockedSessionField(ctx, scope, []string{"k:aaa"})
	require.NoError(t, err)
	require.Empty(t, found)

	require.NoError(t, store.MarkSessionBlocked(ctx, scope, []string{"k:aaa", "k:bbb"}, time.Hour))
	found, err = store.FindBlockedSessionField(ctx, scope, []string{"k:zzz", "k:bbb"})
	require.NoError(t, err)
	require.Equal(t, "k:bbb", found)

	unblocked, err := store.UnblockSessionScope(ctx, scope)
	require.NoError(t, err)
	require.True(t, unblocked)
	found, err = store.FindBlockedSessionField(ctx, scope, []string{"k:aaa"})
	require.NoError(t, err)
	require.Empty(t, found)

	unblocked, err = store.UnblockSessionScope(ctx, scope)
	require.NoError(t, err)
	require.False(t, unblocked)
}

func TestSecurityPolicySessionStoreNilSafe(t *testing.T) {
	ctx := context.Background()
	var store *securityPolicySessionStore
	require.NoError(t, store.MarkSessionBlocked(ctx, "s", []string{"k"}, time.Hour))
	found, err := store.FindBlockedSessionField(ctx, "s", []string{"k"})
	require.NoError(t, err)
	require.Empty(t, found)
	unblocked, err := store.UnblockSessionScope(ctx, "s")
	require.NoError(t, err)
	require.False(t, unblocked)
}
