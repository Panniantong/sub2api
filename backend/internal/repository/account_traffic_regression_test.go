package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountTrafficLoweredRPMWaitsUntilActualCapacity(t *testing.T) {
	server, cache, plan := trafficFixture(t)
	ctx := context.Background()
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		server.SetTime(start.Add(time.Duration(i) * 10 * time.Second))
		a, err := cache.Acquire(ctx, plan, string(rune('a'+i)))
		require.NoError(t, err)
		require.True(t, a.Allowed)
	}
	server.SetTime(start.Add(30 * time.Second))
	plan.Revision = 2
	plan.Signature = "lowered"
	plan.Policy.RPM = 1
	plan.Policy.Burst = 1
	require.NoError(t, cache.Sync(ctx, plan))
	denied, err := cache.Acquire(ctx, plan, "blocked")
	require.NoError(t, err)
	require.False(t, denied.Allowed)
	require.Equal(t, 50*time.Second, denied.RetryAfter)
	server.SetTime(start.Add(80 * time.Second))
	admitted, err := cache.Acquire(ctx, plan, "ready")
	require.NoError(t, err)
	require.True(t, admitted.Allowed)
}

func TestAccountTrafficStaleObservationCannotBypassNewHardPolicy(t *testing.T) {
	_, cache, old := trafficFixture(t)
	ctx := context.Background()
	old.Policy.StrictRPMEnabled = false
	old.Policy.AdaptiveEnabled = true
	next := old
	next.Revision = 2
	next.Signature = "new-observe-threshold"
	next.Policy.FailureThreshold = 5
	require.NoError(t, cache.Sync(ctx, next))
	result, err := cache.Acquire(ctx, old, "old-turn")
	require.NoError(t, err)
	require.True(t, result.Allowed)
	require.True(t, result.SkipObservation)
	state, err := cache.Snapshot(ctx, next)
	require.NoError(t, err)
	require.Zero(t, state.Accepted)
	require.Zero(t, state.InFlight)
	next.Revision = 3
	next.Signature = "strict-enabled"
	next.Policy.StrictRPMEnabled = true
	require.NoError(t, cache.Sync(ctx, next))
	result, err = cache.Acquire(ctx, old, "old-turn-after-enable")
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.False(t, result.SkipObservation)
}
