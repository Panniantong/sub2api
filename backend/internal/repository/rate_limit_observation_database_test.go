//go:build protectionintegration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestRateLimitObservationCannotShortenConcurrentCooldown(t *testing.T) {
	client, db := protectionTestDatabase(t)
	repo := newAccountRepositoryWithSQL(client, db, nil)
	ctx := context.Background()
	a := &service.Account{Name: "cooldown-observation", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Concurrency: 4}
	require.NoError(t, repo.Create(ctx, a))
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, repo.SetRateLimited(ctx, a.ID, later))
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- repo.SetRateLimited(ctx, a.ID, time.Now().Add(5*time.Second)) }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	current, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, later.Equal(*current.RateLimitResetAt), "the longest cooldown instant must survive regardless of database timezone")
	require.NoError(t, repo.ClearRateLimit(ctx, a.ID), "explicit administrator recovery remains available")
	current, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Nil(t, current.RateLimitResetAt)
}
