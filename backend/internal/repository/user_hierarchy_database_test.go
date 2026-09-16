//go:build protectionintegration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func hierarchyTestDatabase(t *testing.T) (*dbent.Client, *sql.DB, *userRepository) {
	t.Helper()
	client, db := protectionTestDatabase(t)
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_allowed_groups(user_id BIGINT, group_id BIGINT,UNIQUE(user_id,group_id));
CREATE TABLE IF NOT EXISTS auth_identities(id BIGSERIAL PRIMARY KEY,user_id BIGINT,provider_type TEXT,provider_key TEXT,provider_subject TEXT,metadata JSONB,issuer TEXT,verified_at TIMESTAMPTZ,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW(),UNIQUE(provider_type,provider_key,provider_subject));
CREATE TABLE IF NOT EXISTS auth_identity_channels(id BIGSERIAL PRIMARY KEY,identity_id BIGINT,provider_type TEXT,provider_key TEXT,channel TEXT,channel_app_id TEXT,channel_subject TEXT,metadata JSONB,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW(),UNIQUE(provider_type,provider_key,channel,channel_app_id,channel_subject));
CREATE TABLE IF NOT EXISTS identity_adoption_decisions(id BIGSERIAL PRIMARY KEY,identity_id BIGINT);`)
	require.NoError(t, err)
	return client, db, newUserRepositoryWithSQL(client, db)
}

func createHierarchyUser(t *testing.T, client *dbent.Client, email, role string) *dbent.User {
	t.Helper()
	user, err := client.User.Create().SetEmail(email).SetPasswordHash("preserve-test-hash").SetRole(role).SetStatus(service.StatusActive).SetBalance(12.5).Save(context.Background())
	require.NoError(t, err)
	return user
}

func waitHierarchyLockWaiters(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	key := uint64(advisoryLockHash(userHierarchyLockKey))
	require.Eventually(t, func() bool {
		var waiting int
		err := db.QueryRow(`SELECT COUNT(*) FROM pg_locks WHERE locktype='advisory' AND classid=$1 AND objid=$2 AND NOT granted`, int64(key>>32), int64(key&0xffffffff)).Scan(&waiting)
		return err == nil && waiting >= count
	}, 10*time.Second, 10*time.Millisecond, "both role mutations must reach the database lock before it is released")
}

func TestUserHierarchyConcurrentMutualDemotionKeepsAdministrator(t *testing.T) {
	client, db, repo := hierarchyTestDatabase(t)
	a := createHierarchyUser(t, client, "a@example.invalid", service.RoleSuperAdmin)
	b := createHierarchyUser(t, client, "b@example.invalid", service.RoleSuperAdmin)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gate, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer gate.Rollback()
	_, err = gate.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockHash(userHierarchyLockKey))
	require.NoError(t, err)
	results := make(chan error, 2)
	for _, operation := range []struct{ actor, target int64 }{{a.ID, b.ID}, {b.ID, a.ID}} {
		go func(actor, target int64) {
			results <- repo.Update(service.WithUserManagementActor(ctx, actor), &service.User{ID: target, Role: service.RoleAdmin}, service.UserUpdateFields{Role: true})
		}(operation.actor, operation.target)
	}
	waitHierarchyLockWaiters(t, db, 2)
	require.NoError(t, gate.Commit())
	success := 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else {
			require.Equal(t, 403, infraerrors.Code(err), err)
		}
	}
	require.Equal(t, 1, success, "the late mutation must re-read its now-demoted actor")
	remaining, err := client.User.Query().Where(dbuser.RoleEQ(service.RoleSuperAdmin), dbuser.StatusEQ(service.StatusActive)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, remaining)
	for _, id := range []int64{a.ID, b.ID} {
		current, err := client.User.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, 12.5, current.Balance)
		require.Equal(t, "preserve-test-hash", current.PasswordHash)
	}
}

func TestUserHierarchyOuterTransactionRetainsLockAndRollback(t *testing.T) {
	client, db, repo := hierarchyTestDatabase(t)
	a := createHierarchyUser(t, client, "owner@example.invalid", service.RoleSuperAdmin)
	b := createHierarchyUser(t, client, "peer@example.invalid", service.RoleSuperAdmin)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	outer, err := client.Tx(ctx)
	require.NoError(t, err)
	defer outer.Rollback()
	txCtx := service.WithUserManagementActor(dbent.NewTxContext(ctx, outer), a.ID)
	require.NoError(t, repo.Update(txCtx, &service.User{ID: b.ID, Role: service.RoleAdmin}, service.UserUpdateFields{Role: true}))
	outside, err := client.User.Get(ctx, b.ID)
	require.NoError(t, err)
	require.Equal(t, service.RoleSuperAdmin, outside.Role, "Update must not commit its caller's transaction")
	done := make(chan error, 1)
	go func() {
		done <- repo.Update(service.WithUserManagementActor(ctx, b.ID), &service.User{ID: a.ID, Role: service.RoleAdmin}, service.UserUpdateFields{Role: true})
	}()
	waitHierarchyLockWaiters(t, db, 1)
	require.NoError(t, outer.Rollback())
	require.NoError(t, <-done, "rollback keeps the actor's original super-admin role")
	current, err := client.User.Get(ctx, b.ID)
	require.NoError(t, err)
	require.Equal(t, service.RoleSuperAdmin, current.Role)
}

func TestUserHierarchyFinalWriteChecksActorAndProtectsAdminState(t *testing.T) {
	client, _, repo := hierarchyTestDatabase(t)
	super := createHierarchyUser(t, client, "super@example.invalid", service.RoleSuperAdmin)
	admin := createHierarchyUser(t, client, "admin@example.invalid", service.RoleAdmin)
	user := createHierarchyUser(t, client, "user@example.invalid", service.RoleUser)
	ctx := context.Background()
	adminCtx := service.WithUserManagementActor(ctx, admin.ID)
	require.NoError(t, repo.Update(adminCtx, &service.User{ID: user.ID, Role: service.RoleUser, Notes: "normal edit"}, service.UserUpdateFields{Role: true, Notes: true}))
	err := repo.Update(adminCtx, &service.User{ID: super.ID, PasswordHash: "changed"}, service.UserUpdateFields{PasswordHash: true})
	require.Equal(t, 403, infraerrors.Code(err))
	err = repo.Update(service.WithUserManagementActor(ctx, super.ID), &service.User{ID: super.ID, Role: service.RoleAdmin}, service.UserUpdateFields{Role: true})
	require.Equal(t, "SELF_DEMOTION_FORBIDDEN", infraerrors.Reason(err))
	err = repo.Update(ctx, &service.User{ID: super.ID, Status: service.StatusDisabled}, service.UserUpdateFields{Status: true})
	require.Equal(t, "ADMIN_PROTECTED", infraerrors.Reason(err))
	err = repo.Delete(ctx, super.ID)
	require.Equal(t, "ADMIN_PROTECTED", infraerrors.Reason(err))
	err = repo.Create(adminCtx, &service.User{Email: "blocked@example.invalid", Role: service.RoleSuperAdmin, Status: service.StatusActive})
	require.Equal(t, 403, infraerrors.Code(err))

	// A role grant that was authorized before a concurrent demotion must not
	// apply with the actor's old permissions once it reaches the repository.
	newSuper := createHierarchyUser(t, client, "second@example.invalid", service.RoleSuperAdmin)
	staleActorCtx := service.WithUserManagementActor(ctx, newSuper.ID)
	require.NoError(t, repo.Update(service.WithUserManagementActor(ctx, super.ID), &service.User{ID: newSuper.ID, Role: service.RoleAdmin}, service.UserUpdateFields{Role: true}))
	err = repo.Update(staleActorCtx, &service.User{ID: user.ID, Role: service.RoleSuperAdmin}, service.UserUpdateFields{Role: true})
	require.Equal(t, 403, infraerrors.Code(err))
	err = repo.Create(staleActorCtx, &service.User{Email: "stale-grant@example.invalid", Role: service.RoleSuperAdmin, Status: service.StatusActive})
	require.Equal(t, 403, infraerrors.Code(err))
	// The same stale request context must not retain write authority after its
	// actor is demoted all the way to an ordinary user.
	require.NoError(t, repo.Update(service.WithUserManagementActor(ctx, super.ID), &service.User{ID: newSuper.ID, Role: service.RoleUser}, service.UserUpdateFields{Role: true}))
	err = repo.Delete(staleActorCtx, user.ID)
	require.Equal(t, 403, infraerrors.Code(err))
	current, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, service.RoleUser, current.Role)
	require.Equal(t, "normal edit", current.Notes)
}
