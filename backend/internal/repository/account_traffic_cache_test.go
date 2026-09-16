package repository

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func trafficFixture(t *testing.T) (*miniredis.Miniredis, *accountTrafficCache, service.AccountTrafficPlan) {
	t.Helper()
	server := miniredis.RunT(t)
	server.SetTime(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	p := service.DefaultAccountTrafficPolicy()
	p.StrictRPMEnabled = true
	p.RPM = 3
	p.Burst = 3
	return server, &accountTrafficCache{rdb: client}, service.AccountTrafficPlan{AccountID: 42, Revision: 1, Signature: "policy-1", HardLimit: 8, Policy: p}
}

func TestAccountTrafficAtomicRPMAndBurst(t *testing.T) {
	server, cache, plan := trafficFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		a, err := cache.Acquire(ctx, plan, fmt.Sprint(i))
		require.NoError(t, err)
		require.True(t, a.Allowed)
		require.NoError(t, cache.Finish(ctx, plan, fmt.Sprint(i), 200, 100))
	}
	blocked, err := cache.Acquire(ctx, plan, "fourth")
	require.NoError(t, err)
	require.False(t, blocked.Allowed)
	require.Equal(t, time.Minute, blocked.RetryAfter)
	server.SetTime(time.Date(2026, 9, 14, 12, 0, 20, 0, time.UTC))
	blocked, err = cache.Acquire(ctx, plan, "still-full")
	require.NoError(t, err)
	require.False(t, blocked.Allowed)
	require.Equal(t, 40*time.Second, blocked.RetryAfter)
	server.SetTime(time.Date(2026, 9, 14, 12, 1, 1, 0, time.UTC))
	accepted, err := cache.Acquire(ctx, plan, "new-window")
	require.NoError(t, err)
	require.True(t, accepted.Allowed)
	state, err := cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 1, state.RequestsLastMinute)
	require.EqualValues(t, 4, state.Accepted)
	// A burst smaller than RPM stops a simultaneous spike before the minute cap.
	plan.AccountID = 43
	plan.Policy.RPM = 60
	plan.Policy.Burst = 2
	for i := 0; i < 2; i++ {
		a, err := cache.Acquire(ctx, plan, fmt.Sprint(i))
		require.NoError(t, err)
		require.True(t, a.Allowed)
	}
	blocked, err = cache.Acquire(ctx, plan, "burst-full")
	require.NoError(t, err)
	require.False(t, blocked.Allowed)
	require.Equal(t, time.Second, blocked.RetryAfter)
}

func TestAccountTrafficConcurrentInstancesCannotExceedCeiling(t *testing.T) {
	server, cache, plan := trafficFixture(t)
	plan.Policy.RPM = 1
	plan.Policy.Burst = 1
	otherClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer otherClient.Close()
	other := &accountTrafficCache{rdb: otherClient}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			chosen := cache
			if i%2 == 1 {
				chosen = other
			}
			a, err := chosen.Acquire(context.Background(), plan, fmt.Sprint(i))
			if err != nil {
				failures <- err
			}
			if a.Allowed {
				admitted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, admitted.Load())
}

func TestAccountTrafficAdaptiveSuggestionAndSlowRecovery(t *testing.T) {
	server, cache, plan := trafficFixture(t)
	ctx := context.Background()
	plan.Policy.StrictRPMEnabled = false
	plan.Policy.AdaptiveEnabled = true
	plan.Policy.FailureThreshold = 2
	for i := 0; i < 2; i++ {
		id := fmt.Sprint(i)
		a, err := cache.Acquire(ctx, plan, id)
		require.NoError(t, err)
		require.True(t, a.Allowed)
		require.NoError(t, cache.Finish(ctx, plan, id, 429, 100))
	}
	state, err := cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 4, state.RecommendedConcurrency)
	require.Equal(t, 8, state.EffectiveConcurrency, "observe mode never lowers the actual cap")
	plan.Revision = 2
	plan.Signature = "automatic"
	plan.Policy.AdaptiveMode = "automatic"
	require.NoError(t, cache.Sync(ctx, plan))
	for i := 0; i < 4; i++ {
		a, err := cache.Acquire(ctx, plan, fmt.Sprint("active-", i))
		require.NoError(t, err)
		require.True(t, a.Allowed)
	}
	blocked, err := cache.Acquire(ctx, plan, "fifth")
	require.NoError(t, err)
	require.False(t, blocked.Allowed)
	require.Equal(t, 4, blocked.State.EffectiveConcurrency)
	for i := 0; i < 3; i++ {
		require.NoError(t, cache.Finish(ctx, plan, fmt.Sprint("active-", i), 200, 100))
	}
	state, err = cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 4, state.EffectiveConcurrency)
	server.SetTime(time.Date(2026, 9, 14, 12, 1, 1, 0, time.UTC))
	require.NoError(t, cache.Finish(ctx, plan, "active-3", 200, 100))
	state, err = cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 5, state.EffectiveConcurrency)
	require.Equal(t, 8, plan.HardLimit, "the saved administrator cap is not mutated")
	plan.Policy.AdaptiveEnabled = false
	plan.Revision = 3
	plan.Signature = "disabled"
	require.NoError(t, cache.Sync(ctx, plan))
	state, err = cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 8, state.EffectiveConcurrency)
}

func TestAccountTrafficStalePolicyAndOldCompletionCannotUndoChanges(t *testing.T) {
	_, cache, old := trafficFixture(t)
	ctx := context.Background()
	old.Policy.StrictRPMEnabled = false
	old.Policy.AdaptiveEnabled = true
	old.Policy.FailureThreshold = 1
	a, err := cache.Acquire(ctx, old, "old-request")
	require.NoError(t, err)
	require.True(t, a.Allowed)
	updated := old
	updated.Revision = 2
	updated.Signature = "turned-off"
	updated.Policy.AdaptiveEnabled = false
	require.NoError(t, cache.Sync(ctx, updated))
	blocked, err := cache.Acquire(ctx, old, "stale")
	require.NoError(t, err)
	require.True(t, blocked.Allowed)
	require.True(t, blocked.SkipObservation, "turning off observation must not block an old session")
	require.NoError(t, cache.Finish(ctx, old, "old-request", 429, 50))
	state, err := cache.Snapshot(ctx, updated)
	require.NoError(t, err)
	require.Equal(t, 8, state.EffectiveConcurrency)
	require.Equal(t, 8, state.RecommendedConcurrency)
	require.Zero(t, state.InFlight)
}
