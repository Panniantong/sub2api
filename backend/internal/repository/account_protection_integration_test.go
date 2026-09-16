//go:build protectionintegration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type protectionIntegrationStore struct{ repo *accountRepository }

func (s protectionIntegrationStore) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	return s.repo.GetByID(ctx, id)
}
func (s protectionIntegrationStore) UpdateAccount(ctx context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	a, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if input.Extra != nil {
		a.Extra = input.Extra
	}
	if input.Concurrency != nil {
		a.Concurrency = *input.Concurrency
	}
	err = s.repo.Update(ctx, a)
	if err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, id)
}

func TestAccountProtectionCreateImportAndStaleWrites(t *testing.T) {
	ctx := context.Background()
	client, protectionDB := protectionTestDatabase(t)
	repo := newAccountRepositoryWithSQL(client, protectionDB, nil)
	for _, kind := range []string{service.AccountTypeOAuth, service.AccountTypeAPIKey} {
		t.Run(kind, func(t *testing.T) {
			a := &service.Account{Name: fmt.Sprintf("protection-%s-%d", kind, time.Now().UnixNano()), Platform: service.PlatformOpenAI, Type: kind, Concurrency: 32, Status: service.StatusActive, Extra: map[string]any{"anti_degradation": false}}
			require.NoError(t, repo.Create(ctx, a))
			t.Cleanup(func() {
				_, _ = protectionDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
				_, _ = protectionDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id=$1", a.ID)
			})
			created, err := repo.GetByID(ctx, a.ID)
			require.NoError(t, err)
			require.True(t, created.AntiDegradationEnabled())
			require.Equal(t, 32, created.Concurrency)
			require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{"anti_degradation": false, "anti_degrade": nil, "untouched": "partial"}))
			bulkConcurrency := 100
			_, err = repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Concurrency: &bulkConcurrency, Extra: map[string]any{"anti_degradation": false, "anti_degrade": nil}})
			require.NoError(t, err)
			updated, err := repo.GetByID(ctx, a.ID)
			require.NoError(t, err)
			require.True(t, updated.AntiDegradationEnabled())
			require.Equal(t, bulkConcurrency, updated.Concurrency)
			require.Equal(t, "partial", updated.Extra["untouched"])
			svc := service.NewAntiDegradeService(protectionIntegrationStore{repo})
			_, err = svc.SetProtection(ctx, a.ID, false, false)
			require.Error(t, err)
			_, err = svc.SetProtection(ctx, a.ID, false, true)
			require.NoError(t, err)
			// A request loaded before disable must not re-enable it after a delay.
			created.Name += " stale-refresh"
			require.NoError(t, repo.Update(ctx, created))
			disabled, err := repo.GetByID(ctx, a.ID)
			require.NoError(t, err)
			require.False(t, disabled.AntiDegradationEnabled())
			_, err = svc.SetProtection(ctx, a.ID, true, false)
			require.NoError(t, err)
			// And a stale OFF import cannot undo the later explicit enable.
			disabled.Extra = map[string]any{"anti_degradation": false, "import": "kept"}
			require.NoError(t, repo.Update(ctx, disabled))
			final, err := repo.GetByID(ctx, a.ID)
			require.NoError(t, err)
			require.True(t, final.AntiDegradationEnabled())
			require.Equal(t, "kept", final.Extra["import"])
		})
	}
}
