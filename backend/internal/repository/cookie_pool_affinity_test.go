package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestCookiePoolAffinityBoundedIsolatedAndExpires(t *testing.T) {
	r := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: r.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	c := &gatewayCache{rdb: client}
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	for id := int64(1); id <= 4; id++ {
		r.SetTime(now.Add(time.Duration(id) * time.Second))
		require.NoError(t, c.RememberCookiePoolAffinity(ctx, 2, "openai:session", id))
	}
	ids, err := c.ReadCookiePoolAffinity(ctx, 2, "openai:session")
	require.NoError(t, err)
	require.Equal(t, []int64{4, 3, 2}, ids)
	for _, scope := range []struct {
		group   int64
		session string
	}{{3, "openai:session"}, {2, "openai:other"}} {
		ids, err = c.ReadCookiePoolAffinity(ctx, scope.group, scope.session)
		require.NoError(t, err)
		require.Empty(t, ids)
	}
	// A sticky hit refreshes only its own history entry; deleting the primary
	// during Host rotation preserves all still-recent candidates.
	require.NoError(t, c.SetSessionAccountID(ctx, 2, "openai:session", 2, time.Hour))
	r.SetTime(now.Add(500 * time.Second))
	require.NoError(t, c.RememberCookiePoolAffinity(ctx, 2, "openai:session", 0))
	require.NoError(t, c.DeleteSessionAccountID(ctx, 2, "openai:session"))
	ids, err = c.ReadCookiePoolAffinity(ctx, 2, "openai:session")
	require.NoError(t, err)
	require.Equal(t, []int64{2, 4, 3}, ids)
	r.SetTime(now.Add(605 * time.Second))
	ids, err = c.ReadCookiePoolAffinity(ctx, 2, "openai:session")
	require.NoError(t, err)
	require.Equal(t, []int64{2}, ids, "refreshing one account must not extend stale alternatives")
	r.FastForward(601 * time.Second)
	ids, err = c.ReadCookiePoolAffinity(ctx, 2, "openai:session")
	require.NoError(t, err)
	require.Empty(t, ids)
}
