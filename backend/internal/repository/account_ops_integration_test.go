//go:build integration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestAccountOpsDurableCoalescingAndClaims(t *testing.T) {
	ctx := context.Background()
	var account int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('ops-fixture','openai','apikey','active',true) RETURNING id`).Scan(&account))
	defer func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account) }()
	repo := NewAccountOpsRepository(integrationDB)
	event := service.AccountOpsEvent{AccountID: account, AccountName: "ops-fixture", Kind: "balance_low", Signal: "balance_error_code", HTTPStatus: 402}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- repo.Record(ctx, event) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	items, err := repo.List(ctx, 0, 100)
	require.NoError(t, err)
	var found *service.AccountOpsEvent
	for i := range items {
		if items[i].AccountID == account {
			found = &items[i]
		}
	}
	require.NotNil(t, found)
	require.EqualValues(t, 12, found.Occurrences)
	claims := make(chan *service.AccountOpsEvent, 2)
	claimErrors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); e, err := repo.Claim(ctx); claims <- e; claimErrors <- err }()
	}
	wg.Wait()
	close(claims)
	close(claimErrors)
	for err := range claimErrors {
		require.NoError(t, err)
	}
	var claimed *service.AccountOpsEvent
	count := 0
	for e := range claims {
		if e != nil {
			count++
			claimed = e
		}
	}
	require.Equal(t, 1, count)
	require.NoError(t, repo.Complete(ctx, claimed, "sent", time.Hour))
	require.NoError(t, repo.Record(ctx, event))
	none, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, none, "cooldown survives a new repository instance")
	_, err = integrationDB.ExecContext(ctx, `UPDATE account_ops_alerts SET next_send_at=NOW()-INTERVAL '1 second' WHERE account_id=$1`, account)
	require.NoError(t, err)
	require.NoError(t, repo.Record(ctx, event))
	again, err := NewAccountOpsRepository(integrationDB).Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, again)
	require.NoError(t, repo.Complete(ctx, claimed, "failed", time.Minute)) // stale lease must not overwrite the new delivery
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM account_ops_alerts WHERE account_id=$1`, account).Scan(&state))
	require.Equal(t, "sending", state)
	require.NoError(t, repo.Complete(ctx, again, "failed", time.Minute))
	require.NoError(t, repo.SuppressDisabled(ctx, service.AccountOpsConfig{}))
	none, err = repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, none)
}
