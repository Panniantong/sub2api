package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMode1SchedulerProjectionRetainsPolicyAndProxy(t *testing.T) {
	a := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 100, Extra: map[string]any{
		"codex_fingerprint_mode": "device", "codex_fingerprint_seed": "11111111-1111-4111-8111-111111111111", "enable_tls_fingerprint": true, "tls_fingerprint_builtin": "nodejs24", "proxy_mode": "random",
		"anti_degrade": map[string]any{"enabled": true, "mode": "mode1", "policy_version": 2, "max_concurrency": 4},
	}}
	projected := buildSchedulerMetadataAccount(a)
	for _, key := range []string{"anti_degrade", "proxy_mode", "enable_tls_fingerprint", "tls_fingerprint_builtin", "codex_fingerprint_seed"} {
		require.Equal(t, a.Extra[key], projected.Extra[key], key)
	}
	require.Equal(t, 100, projected.Mode1EffectiveConcurrency())
}

func TestAccountProtectionSchedulerRetainsIndependentIntegrityMode(t *testing.T) {
	for _, mode := range []string{"off", "observe", "enforce"} {
		a := service.Account{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{"anti_degradation": true, "request_integrity_mode": mode, "anti_degrade": map[string]any{"enabled": true, "mode": "legacy"}}}
		projected := buildSchedulerMetadataAccount(a)
		require.Equal(t, mode, projected.RequestIntegrityMode())
	}
}

func TestMode1ConcurrencySlotsRemainBoundedAndRelease(t *testing.T) {
	r := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: r.Addr()})
	t.Cleanup(func() { client.Close() })
	cache := NewConcurrencyCache(client, 15, 900)
	a := &service.Account{ID: 1, Concurrency: 4, Extra: map[string]any{"anti_degrade": map[string]any{"enabled": true, "mode": "mode1", "policy_version": 2, "max_concurrency": 99}}}
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		ok, err := cache.AcquireAccountSlot(ctx, a.ID, a.Mode1EffectiveConcurrency(), fmt.Sprint(i))
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err := cache.AcquireAccountSlot(ctx, a.ID, a.Mode1EffectiveConcurrency(), "excess")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, cache.ReleaseAccountSlot(ctx, a.ID, "0"))
	ok, err = cache.AcquireAccountSlot(ctx, a.ID, a.Mode1EffectiveConcurrency(), "replacement")
	require.NoError(t, err)
	require.True(t, ok)
}
