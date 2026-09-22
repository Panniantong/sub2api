//go:build integration

package repository

import (
	"context"
	"testing"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Runs only against the repository harness's disposable PostgreSQL instance.
// The concurrent commit deliberately occurs BETWEEN the account and group reads.
func TestOpenAITurnAdmissionPrimarySnapshotIntegration(t *testing.T) {
	ctx := context.Background()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, integrationDB)))
	repo := &accountRepository{client: client}
	a, err := client.Account.Create().SetName("turn-admission-synthetic").
		SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeOAuth).
		SetCredentials(map[string]any{"access_token": "synthetic"}).
		SetStatus(service.StatusActive).SetSchedulable(true).Save(ctx)
	require.NoError(t, err)
	groupA := createEntGroup(t, ctx, client, "turn-admission-synthetic-a")
	groupB := createEntGroup(t, ctx, client, "turn-admission-synthetic-b")
	_, err = client.AccountGroup.Create().SetAccountID(a.ID).SetGroupID(groupA.ID).Save(ctx)
	require.NoError(t, err)

	changed := false
	client.Account.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			result, queryErr := next.Query(ctx, query)
			if queryErr == nil && !changed {
				changed = true
				// Different DB connection: commit a state+membership change while
				// the admission reader's read-only snapshot is still open.
				tx, err := integrationDB.BeginTx(ctx, nil)
				require.NoError(t, err)
				defer tx.Rollback()
				_, err = tx.ExecContext(ctx, `UPDATE accounts SET schedulable=false WHERE id=$1`, a.ID)
				require.NoError(t, err)
				_, err = tx.ExecContext(ctx, `UPDATE account_groups SET group_id=$1 WHERE account_id=$2`, groupB.ID, a.ID)
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
			}
			return result, queryErr
		})
	}))
	before, parent, err := repo.GetOpenAITurnAdmission(ctx, a.ID)
	require.NoError(t, err)
	require.Nil(t, parent)
	require.True(t, changed)
	require.True(t, before.Schedulable)
	require.Equal(t, []int64{groupA.ID}, before.GroupIDs, "must not combine old status with new membership")

	after, _, err := repo.GetOpenAITurnAdmission(ctx, a.ID)
	require.NoError(t, err)
	require.False(t, after.Schedulable)
	require.Equal(t, []int64{groupB.ID}, after.GroupIDs, "next read sees the committed primary state")
	require.NoError(t, client.Account.DeleteOneID(a.ID).Exec(ctx))
	deleted, _, err := repo.GetOpenAITurnAdmission(ctx, a.ID)
	require.ErrorIs(t, err, service.ErrAccountNotFound)
	require.Nil(t, deleted)
}
