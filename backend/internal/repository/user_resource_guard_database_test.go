//go:build protectionintegration

package repository

import (
	"context"
	"testing"
	"time"

	entschema "entgo.io/ent/dialect/sql/schema"
	"github.com/Wei-Shaw/sub2api/ent/migrate"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserResourceRolesAtFinalDatabaseWrite(t *testing.T) {
	client, db, _ := hierarchyTestDatabase(t)
	ctx := context.Background()
	require.NoError(t, migrate.Create(ctx, client.Schema, []*entschema.Table{migrate.UsersTable, migrate.GroupsTable, migrate.APIKeysTable, migrate.UserSubscriptionsTable}))
	admin := createHierarchyUser(t, client, "resource-admin@example.test", service.RoleAdmin)
	super := createHierarchyUser(t, client, "resource-super@example.test", service.RoleSuperAdmin)
	user := createHierarchyUser(t, client, "resource-user@example.test", service.RoleUser)
	group, err := client.Group.Create().SetName("subscription").SetSubscriptionType("subscription").Save(ctx)
	require.NoError(t, err)
	keys := &apiKeyRepository{client: client}
	subs := &userSubscriptionRepository{client: client}
	for _, target := range []struct {
		id   int64
		want int
	}{{super.ID, 403}, {admin.ID, 403}, {user.ID, 200}} {
		key, err := client.APIKey.Create().SetName("key").SetKey("test-only-resource-key-" + time.Now().Format("150405.000000000")).SetUserID(target.id).SetGroupID(group.ID).Save(ctx)
		require.NoError(t, err)
		adminCtx := service.WithUserManagementActor(ctx, admin.ID)
		err = keys.Update(adminCtx, &service.APIKey{ID: key.ID, GroupID: nil}, service.APIKeyUpdateFields{GroupID: true})
		if target.want == 403 {
			require.Equal(t, 403, infraerrors.Code(err))
		} else {
			require.NoError(t, err)
		}
		persisted, err := client.APIKey.Get(ctx, key.ID)
		require.NoError(t, err)
		if target.want == 403 {
			require.Equal(t, group.ID, *persisted.GroupID)
		} else {
			require.Nil(t, persisted.GroupID)
		}
		sub, err := client.UserSubscription.Create().SetUserID(target.id).SetGroupID(group.ID).SetStartsAt(time.Now()).SetExpiresAt(time.Now().Add(time.Hour)).Save(ctx)
		require.NoError(t, err)
		err = subs.Delete(adminCtx, sub.ID)
		if target.want == 403 {
			require.Equal(t, 403, infraerrors.Code(err))
		} else {
			require.NoError(t, err)
		}
		if target.want == 403 {
			require.NoError(t, keys.Update(service.WithUserManagementActor(ctx, super.ID), &service.APIKey{ID: key.ID}, service.APIKeyUpdateFields{GroupID: true}))
			require.NoError(t, subs.Delete(service.WithUserManagementActor(ctx, super.ID), sub.ID))
		}
	}
	// Authorization is re-read after waiting for a concurrent role transition.
	key, err := client.APIKey.Create().SetName("late").SetKey("test-only-late-resource-key").SetUserID(user.ID).SetGroupID(group.ID).Save(ctx)
	require.NoError(t, err)
	gate, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer gate.Rollback()
	_, err = gate.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockHash(userHierarchyLockKey))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		done <- keys.Update(service.WithUserManagementActor(ctx, admin.ID), &service.APIKey{ID: key.ID}, service.APIKeyUpdateFields{GroupID: true})
	}()
	waitHierarchyLockWaiters(t, db, 1)
	_, err = gate.ExecContext(ctx, `UPDATE users SET role='user' WHERE id=$1`, admin.ID)
	require.NoError(t, err)
	require.NoError(t, gate.Commit())
	require.Equal(t, 403, infraerrors.Code(<-done))
	persisted, err := client.APIKey.Get(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, group.ID, *persisted.GroupID)
}
