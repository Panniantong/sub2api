//go:build protectionintegration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAccountProtectionEditableConcurrencySingleBulkAndTransitions(t *testing.T) {
	client, db := protectionTestDatabase(t)
	repo := newAccountRepositoryWithSQL(client, db, nil)
	ctx := context.Background()
	account := &service.Account{Name: "editable-concurrency", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 64, Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, account))
	protection := service.NewAntiDegradeService(protectionIntegrationStore{repo})
	for _, mode := range service.RegisteredProtectionModes() {
		current, err := protection.ApplyAntiDegradeMode(ctx, account.ID, service.AntiDegradeMode(mode))
		require.NoError(t, err)
		require.GreaterOrEqual(t, current.Mode1EffectiveConcurrency(), 64)
		wanted := 128
		_, err = repo.BulkUpdate(ctx, []int64{account.ID}, service.AccountBulkUpdate{Concurrency: &wanted})
		require.NoError(t, err)
		current, err = repo.GetByID(ctx, account.ID)
		require.NoError(t, err)
		require.Equal(t, wanted, current.Mode1EffectiveConcurrency())
		marker := current.Extra[service.AntiDegradeMarkerExtraKey].(map[string]any)
		require.EqualValues(t, wanted, marker["max_concurrency"])
		current.Concurrency = 256
		require.NoError(t, repo.Update(ctx, current))
		restored, err := protection.RevertAntiDegrade(ctx, account.ID)
		require.NoError(t, err)
		require.Equal(t, 256, restored.Concurrency)
	}
}
