package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// The account may still belong to group 2. Both HTTP and WS must count the
// request in its actual routing group (3), including its historical demand peak.
func TestCookieDegradedRequestDemandDoesNotEnterOriginalPool(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := repository.NewConcurrencyCache(client, 15, 900)
	demand := cache.(service.CookieGroupDemandCache)
	helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)
	ctx := context.Background()
	degraded := int64(3)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/responses")
	c.Set("api_key", &service.APIKey{ID: 77, GroupID: &degraded})
	httpRelease := helper.withAPIKeySlotFromGin(c, func() {})
	wsRelease, ok, err := helper.TryAcquireUserSlotForAPIKey(ctx, 202, 5, 77, &degraded)
	require.NoError(t, err)
	require.True(t, ok)
	defer httpRelease()
	defer wsRelease()
	normal, err := demand.ReadCookieGroupDemand(ctx, 2, 60)
	require.NoError(t, err)
	require.Equal(t, service.CookieGroupDemand{}, normal)
	actual, err := demand.ReadCookieGroupDemand(ctx, 3, 60)
	require.NoError(t, err)
	require.Equal(t, service.CookieGroupDemand{Current: 2, Peak: 2}, actual)
	httpRelease()
	wsRelease()
	actual, err = demand.ReadCookieGroupDemand(ctx, 3, 60)
	require.NoError(t, err)
	require.Equal(t, service.CookieGroupDemand{Current: 0, Peak: 2}, actual)
	normal, err = demand.ReadCookieGroupDemand(ctx, 2, 60)
	require.NoError(t, err)
	require.Equal(t, service.CookieGroupDemand{}, normal)
}
