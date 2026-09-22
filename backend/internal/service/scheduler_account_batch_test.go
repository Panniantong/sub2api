package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type schedulerBatchCacheStub struct {
	SchedulerCache
	accounts map[int64]*Account
	batches  [][]int64
	err      error
}

func (c *schedulerBatchCacheStub) GetAccounts(_ context.Context, ids []int64) (map[int64]*Account, error) {
	c.batches = append(c.batches, append([]int64(nil), ids...))
	out := make(map[int64]*Account)
	for _, id := range ids {
		if account := c.accounts[id]; account != nil {
			out[id] = account
		}
	}
	return out, c.err
}

type schedulerBatchRepoStub struct {
	AccountRepository
	accounts map[int64]*Account
	batches  [][]int64
	err      error
}

func (r *schedulerBatchRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	r.batches = append(r.batches, append([]int64(nil), ids...))
	out := make([]*Account, 0, len(ids))
	for _, id := range ids {
		if account := r.accounts[id]; account != nil {
			out = append(out, account)
		}
	}
	return out, r.err
}

func TestSchedulerFullAccountBatch(t *testing.T) {
	t.Run("bounded_deduplicated_cache_reads", func(t *testing.T) {
		cache := &schedulerBatchCacheStub{accounts: make(map[int64]*Account)}
		ids := []int64{-1, 0}
		for id := int64(1); id <= 300; id++ {
			ids = append(ids, id, id)
			cache.accounts[id] = &Account{ID: id}
		}
		svc := &SchedulerSnapshotService{cache: cache}
		got, err := svc.GetAccounts(context.Background(), ids)
		require.NoError(t, err)
		require.Len(t, got, 300)
		require.Len(t, cache.batches, 3)
		for _, batch := range cache.batches {
			require.LessOrEqual(t, len(batch), schedulerAccountReadBatchSize)
		}
	})
	t.Run("only_misses_use_one_database_batch", func(t *testing.T) {
		cache := &schedulerBatchCacheStub{accounts: map[int64]*Account{1: {ID: 1}}}
		repo := &schedulerBatchRepoStub{accounts: map[int64]*Account{2: {ID: 2}, 3: {ID: 3}}}
		svc := &SchedulerSnapshotService{cache: cache, accountRepo: repo}
		got, err := svc.GetAccounts(context.Background(), []int64{1, 2, 3, 4})
		require.NoError(t, err)
		require.Len(t, got, 3) // Missing/deleted 4 must not become a fabricated account.
		require.Equal(t, [][]int64{{2, 3, 4}}, repo.batches)
	})
	t.Run("cache_failure_obeys_disabled_database_fallback", func(t *testing.T) {
		cache := &schedulerBatchCacheStub{err: errors.New("cache unavailable")}
		repo := &schedulerBatchRepoStub{}
		svc := &SchedulerSnapshotService{cache: cache, accountRepo: repo, cfg: &config.Config{}}
		got, err := svc.GetAccounts(context.Background(), []int64{1})
		require.ErrorIs(t, err, ErrSchedulerCacheNotReady)
		require.Nil(t, got)
		require.Empty(t, repo.batches)
	})
	t.Run("database_failure_is_not_empty_success", func(t *testing.T) {
		want := errors.New("database unavailable")
		svc := &SchedulerSnapshotService{accountRepo: &schedulerBatchRepoStub{err: want}}
		got, err := svc.GetAccounts(context.Background(), []int64{1})
		require.ErrorIs(t, err, want)
		require.Nil(t, got)
	})
	t.Run("mismatched_cached_account_is_not_trusted", func(t *testing.T) {
		cache := &schedulerBatchCacheStub{accounts: map[int64]*Account{1: {ID: 99}}}
		repo := &schedulerBatchRepoStub{accounts: map[int64]*Account{1: {ID: 1}}}
		svc := &SchedulerSnapshotService{cache: cache, accountRepo: repo}
		got, err := svc.GetAccounts(context.Background(), []int64{1})
		require.NoError(t, err)
		require.Equal(t, int64(1), got[1].ID)
		require.Len(t, repo.batches, 1)
	})
	t.Run("cancellation_does_not_read_storage", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		cache := &schedulerBatchCacheStub{}
		svc := &SchedulerSnapshotService{cache: cache}
		_, err := svc.GetAccounts(ctx, []int64{1})
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, cache.batches)
	})
}
