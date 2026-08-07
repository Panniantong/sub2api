package service

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIModelTransient_FirstFailureDoesNotCreateLongBlock(t *testing.T) {
	state := newOpenAIAccountModelTransientState(128)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)

	decision := state.recordFailure(35, "gpt-5.5", now)

	assert.Equal(t, 1, decision.FailureStreak)
	assert.Zero(t, decision.Cooldown)
	assert.False(t, state.isBlocked(35, "gpt-5.5", now))
}

func TestOpenAIModelTransient_SecondFailureCreatesShortModelBlock(t *testing.T) {
	state := newOpenAIAccountModelTransientState(128)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	state.recordFailure(35, "gpt-5.5", now)

	decision := state.recordFailure(35, "gpt-5.5", now.Add(time.Second))

	assert.Equal(t, 2, decision.FailureStreak)
	assert.Equal(t, openAIModelTransientShortCooldown, decision.Cooldown)
	assert.True(t, state.isBlocked(35, "gpt-5.5", now.Add(2*time.Second)))
	assert.False(t, state.isBlocked(35, "gpt-5.5", now.Add(openAIModelTransientShortCooldown+2*time.Second)))
}

func TestOpenAIModelTransient_ThirdFailureCreatesFortyFiveSecondModelBlock(t *testing.T) {
	state := newOpenAIAccountModelTransientState(128)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	state.recordFailure(35, "gpt-5.5", now)
	state.recordFailure(35, "gpt-5.5", now.Add(time.Second))

	decision := state.recordFailure(35, "gpt-5.5", now.Add(2*time.Second))

	assert.Equal(t, 3, decision.FailureStreak)
	assert.Equal(t, 45*time.Second, decision.Cooldown)
	assert.True(t, state.isBlocked(35, "gpt-5.5", now.Add(40*time.Second)))
	assert.False(t, state.isBlocked(35, "gpt-5.5", now.Add(48*time.Second)))
}

func TestOpenAIModelTransient_BlockIsIsolatedByModel(t *testing.T) {
	state := newOpenAIAccountModelTransientState(128)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	state.recordFailure(35, "gpt-5.6-terra", now)
	state.recordFailure(35, "GPT-5.6-TERRA", now.Add(time.Second))

	assert.True(t, state.isBlocked(35, "gpt-5.6-terra", now.Add(2*time.Second)))
	assert.False(t, state.isBlocked(35, "gpt-5.5", now.Add(2*time.Second)))
	assert.False(t, state.isBlocked(47, "gpt-5.6-terra", now.Add(2*time.Second)))
}

func TestOpenAIModelTransient_SuccessClearsStreakAndBlock(t *testing.T) {
	state := newOpenAIAccountModelTransientState(128)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	state.recordFailure(35, "gpt-5.5", now)
	state.recordFailure(35, "gpt-5.5", now.Add(time.Second))
	require.True(t, state.isBlocked(35, "gpt-5.5", now.Add(2*time.Second)))

	state.recordSuccess(35, "gpt-5.5")

	assert.False(t, state.isBlocked(35, "gpt-5.5", now.Add(2*time.Second)))
	decision := state.recordFailure(35, "gpt-5.5", now.Add(3*time.Second))
	assert.Equal(t, 1, decision.FailureStreak)
	assert.Zero(t, decision.Cooldown)
}

func TestOpenAIModelTransient_StaleStreakExpires(t *testing.T) {
	state := newOpenAIAccountModelTransientState(128)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	state.recordFailure(35, "gpt-5.5", now)

	decision := state.recordFailure(35, "gpt-5.5", now.Add(openAIModelTransientFailureWindow+time.Second))

	assert.Equal(t, 1, decision.FailureStreak)
	assert.Zero(t, decision.Cooldown)
}

func TestOpenAIPlanGatedTransient_UsesProgressiveCooldowns(t *testing.T) {
	state := newOpenAIAccountModelTransientStateWithPolicy(128, openAIPlanGatedTransientPolicy())
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)

	first := state.recordFailure(35, "gpt-5.6-sol", now)
	second := state.recordFailure(35, "gpt-5.6-sol", now.Add(3*time.Second))
	third := state.recordFailure(35, "gpt-5.6-sol", now.Add(34*time.Second))
	fourth := state.recordFailure(35, "gpt-5.6-sol", now.Add(5*time.Minute+35*time.Second))

	assert.Equal(t, 1, first.FailureStreak)
	assert.Equal(t, 5*time.Second, first.Cooldown)
	assert.Equal(t, 2, second.FailureStreak)
	assert.Equal(t, 30*time.Second, second.Cooldown)
	assert.Equal(t, 3, third.FailureStreak)
	assert.Equal(t, 5*time.Minute, third.Cooldown)
	assert.Equal(t, 4, fourth.FailureStreak)
	assert.Equal(t, 30*time.Minute, fourth.Cooldown)
	assert.True(t, state.isBlocked(35, "gpt-5.6-sol", now.Add(6*time.Minute)))
	assert.False(t, state.isBlocked(35, "gpt-5.6-terra", now.Add(6*time.Minute)))
}

func TestOpenAIPlanGatedTransient_DeduplicatesBurstFailures(t *testing.T) {
	state := newOpenAIAccountModelTransientStateWithPolicy(128, openAIPlanGatedTransientPolicy())
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	first := state.recordFailure(35, "gpt-5.6-sol", now)

	duplicate := state.recordFailure(35, "gpt-5.6-sol", now.Add(time.Second))

	assert.Equal(t, 1, duplicate.FailureStreak)
	assert.Equal(t, first.BlockUntil, duplicate.BlockUntil)
	assert.True(t, state.isBlocked(35, "gpt-5.6-sol", now.Add(4*time.Second)))
	assert.False(t, state.isBlocked(35, "gpt-5.6-sol", now.Add(6*time.Second)))
}

func TestOpenAIPlanGatedTransient_SuccessImmediatelyClearsCooldown(t *testing.T) {
	state := newOpenAIAccountModelTransientStateWithPolicy(128, openAIPlanGatedTransientPolicy())
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	state.recordFailure(35, "gpt-5.6-sol", now)
	require.True(t, state.isBlocked(35, "gpt-5.6-sol", now.Add(time.Second)))

	state.recordSuccess(35, "gpt-5.6-sol")

	assert.False(t, state.isBlocked(35, "gpt-5.6-sol", now.Add(time.Second)))
	decision := state.recordFailure(35, "gpt-5.6-sol", now.Add(2*time.Second))
	assert.Equal(t, 1, decision.FailureStreak)
	assert.Equal(t, 5*time.Second, decision.Cooldown)
}

func TestOpenAIModelTransient_IgnoresInvalidKeys(t *testing.T) {
	state := newOpenAIAccountModelTransientState(128)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)

	assert.Zero(t, state.recordFailure(0, "gpt-5.5", now).FailureStreak)
	assert.Zero(t, state.recordFailure(35, " ", now).FailureStreak)
	assert.False(t, state.isBlocked(0, "gpt-5.5", now))
	assert.False(t, state.isBlocked(35, "", now))
	assert.Equal(t, 0, state.size())
}

func TestOpenAIModelTransient_IgnoresOversizedModelKey(t *testing.T) {
	state := newOpenAIAccountModelTransientState(128)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	model := strings.Repeat("m", openAIModelTransientMaxModelBytes+1)

	decision := state.recordFailure(35, model, now)

	assert.Zero(t, decision.FailureStreak)
	assert.False(t, state.isBlocked(35, model, now))
	assert.Equal(t, 0, state.size())
}

func TestOpenAIModelTransient_StateIsBoundedAndConcurrencySafe(t *testing.T) {
	const maxEntries = 16
	state := newOpenAIAccountModelTransientState(maxEntries)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup

	for i := 0; i < 128; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			model := fmt.Sprintf("gpt-test-%d", i)
			state.recordFailure(int64(i+1), model, now.Add(time.Duration(i)*time.Millisecond))
			_ = state.isBlocked(int64(i+1), model, now.Add(time.Second))
		}(i)
	}
	wg.Wait()

	assert.LessOrEqual(t, state.size(), maxEntries)
}
