//go:build unit

package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerTicketFullAccountBatchRedis(t *testing.T) {
	ctx := context.Background()
	cache, mr := newSchedulerCacheUnitWithRedis(t)
	cache.mgetChunkSize = 3 // Split account/last-used pairs across MGET chunks.
	bucket := service.SchedulerBucket{GroupID: 2, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	accounts := []service.Account{
		{
			ID: 101, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{2},
			Credentials: map[string]any{"email": "fixture@example.invalid", "chatgpt_account_id": "synthetic", "plan_type": "pro"},
			Extra:       map[string]any{"codex_turn_ticket:gpt-6-astra": map[string]any{"state": "synthetic-ticket", "identity": "synthetic-fingerprint"}},
		},
		{ID: 102, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
	}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, accounts))
	meta, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, meta, 2)
	for _, account := range meta {
		require.NotContains(t, account.Credentials, "email")
		require.NotContains(t, account.Credentials, "chatgpt_account_id")
		require.NotContains(t, account.Extra, "codex_turn_ticket:gpt-6-astra")
	}
	lastUsed := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, cache.UpdateLastUsed(ctx, map[int64]time.Time{101: lastUsed}))
	full, err := cache.GetAccounts(ctx, []int64{101, 102, 101, 999, -1})
	require.NoError(t, err)
	require.Len(t, full, 2)
	require.Equal(t, "synthetic", full[101].GetCredential("chatgpt_account_id"))
	require.Contains(t, full[101].Extra, "codex_turn_ticket:gpt-6-astra")
	require.Equal(t, lastUsed, *full[101].LastUsedAt)
	// Warm the real Redis snapshot through the service, without touching the DB.
	snapshot := service.NewSchedulerSnapshotService(cache, nil, nil, nil, nil)
	loaded, err := snapshot.GetAccounts(ctx, []int64{101, 102})
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	require.Contains(t, loaded[101].Extra, "codex_turn_ticket:gpt-6-astra")

	// A corrupt full snapshot cannot silently turn into an empty ticket set.
	mr.Set(schedulerAccountKey("101"), `{"ID":999}`)
	_, err = cache.GetAccounts(ctx, []int64{101})
	require.ErrorContains(t, err, "ID mismatch")
	mr.Set(schedulerAccountKey("101"), `{invalid`)
	_, err = cache.GetAccounts(ctx, []int64{101})
	require.Error(t, err)
}

type ticketRoutingRepo struct {
	service.AccountRepository
	account service.Account
}

func (r *ticketRoutingRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	if id != r.account.ID {
		return nil, service.ErrAccountNotFound
	}
	account := r.account
	return &account, nil
}

type ticketRoutingConcurrency struct{ service.ConcurrencyCache }

func (ticketRoutingConcurrency) GetAccountsLoadBatch(_ context.Context, accounts []service.AccountWithConcurrency) (map[int64]*service.AccountLoadInfo, error) {
	loads := make(map[int64]*service.AccountLoadInfo, len(accounts))
	for _, account := range accounts {
		loads[account.ID] = &service.AccountLoadInfo{AccountID: account.ID}
	}
	return loads, nil
}

func (ticketRoutingConcurrency) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}

func (ticketRoutingConcurrency) ReleaseAccountSlot(context.Context, int64, string) error {
	return nil
}

// Real Redis serialization -> metadata candidate -> full-account hydration ->
// public gateway selection. No upstream transport or harvester is running.
func TestSchedulerTicketRedisToGateway(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	groupID := int64(2)
	account := service.Account{
		ID: 201, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID}, Concurrency: 10,
		Credentials: map[string]any{"chatgpt_account_id": "synthetic", "email": "fixture@example.invalid", "plan_type": "pro"},
		Extra: map[string]any{"codex_turn_ticket:gpt-6-astra": map[string]any{
			"state": "gAAAAA" + strings.Repeat("B", 286), "length": 292,
			"identity":    fmt.Sprintf("%x", sha256.Sum256([]byte("synthetic\x00fixture@example.invalid"))),
			"captured_at": time.Now(), "issued_at": time.Now(), "expires_at": time.Now().Add(30 * time.Minute),
		}},
	}
	bucket := service.SchedulerBucket{GroupID: groupID, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{
		// Start disabled; stop/join the constructor's background worker before
		// enabling the local gate, so this test cannot trigger a model probe.
		Enabled: false, FailClosed: true, TargetLength: 292, Models: []string{"gpt-6-astra"},
	}
	repo := &ticketRoutingRepo{account: account}
	snapshot := service.NewSchedulerSnapshotService(cache, nil, repo, nil, cfg)
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, nil, cfg, snapshot,
		service.NewConcurrencyService(ticketRoutingConcurrency{}),
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	gateway.StopOpenAICodexTicketHarvester()
	cfg.Gateway.OpenAICodexTicket.Enabled = true

	selectAccount := func() error {
		result, _, err := gateway.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-6-astra", nil, service.OpenAIUpstreamTransportAny, false)
		if err != nil {
			return err
		}
		if result == nil || result.Account == nil || result.Account.ID != account.ID {
			return fmt.Errorf("unexpected selected account")
		}
		if result.ReleaseFunc != nil {
			result.ReleaseFunc()
		}
		return nil
	}
	require.NoError(t, selectAccount()) // cold memory
	require.NoError(t, selectAccount()) // warm memory
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- selectAccount()
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}

	// Keep stale membership in the bucket, but stop the current account.
	account.Schedulable = false
	repo.account = account
	require.NoError(t, cache.SetAccount(ctx, &account))
	require.Error(t, selectAccount())
}
