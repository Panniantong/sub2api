package repository

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestAffinityAtomicOwnerAndExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for id := int64(1); id <= 40; id++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			errs <- cache.PreserveSessionAccountID(ctx, 2, "s", "old", id, time.Minute, 30*time.Second, true)
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	owner, err := cache.GetSessionAccountID(ctx, 2, "s")
	require.NoError(t, err)
	legacy, err := cache.GetSessionAccountID(ctx, 2, "old")
	require.NoError(t, err)
	require.Equal(t, owner, legacy)
	server.FastForward(10 * time.Second)
	require.NoError(t, cache.PreserveSessionAccountID(ctx, 2, "s", "old", owner+100, time.Minute, 30*time.Second, true))
	require.Equal(t, 50*time.Second, server.TTL(buildSessionKey(2, "s")))
	require.NoError(t, cache.PreserveSessionAccountID(ctx, 2, "s", "old", owner, time.Minute, 30*time.Second, true))
	require.Equal(t, time.Minute, server.TTL(buildSessionKey(2, "s")))
	server.FastForward(61 * time.Second)
	require.NoError(t, cache.PreserveSessionAccountID(ctx, 2, "s", "old", 999, time.Minute, 30*time.Second, true))
	got, err := cache.GetSessionAccountID(ctx, 2, "s")
	require.NoError(t, err)
	require.Equal(t, int64(999), got)
	// An old-format owner is also protected before a primary key exists.
	require.NoError(t, cache.SetSessionAccountID(ctx, 2, "legacy", 7, time.Minute))
	require.NoError(t, cache.PreserveSessionAccountID(ctx, 2, "new", "legacy", 8, time.Minute, time.Minute, false))
	require.False(t, server.Exists(buildSessionKey(2, "new")))
}
