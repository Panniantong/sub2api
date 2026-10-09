package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestCookieGroupDemandPeakWindowReleaseAndCrashExpiry(t *testing.T) {
	r := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: r.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	c := NewConcurrencyCache(client, 15, 900).(service.CookieGroupDemandCache)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	r.SetTime(now)
	require.NoError(t, c.TouchCookieGroupDemand(ctx, 2, "one"))
	require.NoError(t, c.TouchCookieGroupDemand(ctx, 2, "two"))
	require.NoError(t, c.TouchCookieGroupDemand(ctx, 2, "two"))
	value, err := c.ReadCookieGroupDemand(ctx, 2, 10)
	require.NoError(t, err)
	require.Equal(t, service.CookieGroupDemand{Current: 2, Peak: 2}, value)
	require.NoError(t, c.ReleaseCookieGroupDemand(ctx, 2, "one"))
	r.SetTime(now.Add(5 * time.Second))
	value, err = c.ReadCookieGroupDemand(ctx, 2, 10)
	require.NoError(t, err)
	require.Equal(t, service.CookieGroupDemand{Current: 1, Peak: 2}, value)
	r.SetTime(now.Add(11 * time.Second))
	value, err = c.ReadCookieGroupDemand(ctx, 2, 10)
	require.NoError(t, err)
	require.Equal(t, service.CookieGroupDemand{Current: 1, Peak: 1}, value)
	other, err := c.ReadCookieGroupDemand(ctx, 3, 10)
	require.NoError(t, err)
	require.Zero(t, other.Current)
	r.SetTime(now.Add(80 * time.Second))
	value, err = c.ReadCookieGroupDemand(ctx, 2, 10)
	require.NoError(t, err)
	require.Equal(t, service.CookieGroupDemand{}, value)
}
