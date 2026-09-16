package repository

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type managedResourceGuardKey struct{}

func needsManagedResourceGuard(ctx context.Context) bool {
	_, managed := service.UserManagementActor(ctx)
	return managed && ctx.Value(managedResourceGuardKey{}) != true
}

// The caller supplies a fixed table name, never request input. Role and owner
// checks share the role-mutation lock and live until the resource write commits.
func withManagedResourceWrite(ctx context.Context, client *dbent.Client, table string, resourceID, ownerID int64, work func(context.Context) error) error {
	actorID, managed := service.UserManagementActor(ctx)
	if !managed {
		return work(ctx)
	}
	if actorID <= 0 || client == nil {
		return infraerrors.Forbidden("USER_RESOURCE_FORBIDDEN", "User resource authorization unavailable")
	}
	var tx *dbent.Tx
	if current := dbent.TxFromContext(ctx); current != nil {
		client = current.Client()
	} else {
		var err error
		tx, err = client.Tx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		client = tx.Client()
		ctx = dbent.NewTxContext(ctx, tx)
	}
	release, err := lockUserHierarchy(ctx, client)
	if err != nil {
		return err
	}
	defer release()
	owners := []int64{actorID}
	if ownerID > 0 {
		owners = append(owners, ownerID)
	}
	if resourceID > 0 {
		if table != "api_keys" && table != "user_subscriptions" {
			return infraerrors.BadRequest("INVALID_RESOURCE", "Invalid resource")
		}
		rows, err := client.QueryContext(ctx, "SELECT user_id FROM "+table+" WHERE id = $1", resourceID)
		if err != nil {
			return err
		}
		if !rows.Next() {
			err := rows.Err()
			_ = rows.Close()
			if err != nil {
				return err
			}
			return infraerrors.NotFound("USER_RESOURCE_NOT_FOUND", "User resource not found")
		}
		var actualOwner int64
		err = rows.Scan(&actualOwner)
		_ = rows.Close()
		if err != nil {
			return err
		}
		owners = append(owners, actualOwner)
	}
	users, err := lockHierarchyUsers(ctx, client, owners...)
	if err != nil {
		return err
	}
	needsSuper := false
	for _, id := range owners[1:] {
		if users[id] == nil {
			return service.ErrUserNotFound
		}
		needsSuper = needsSuper || service.IsAdminRole(users[id].Role)
	}
	if err := verifyHierarchyActor(users[actorID], needsSuper); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, managedResourceGuardKey{}, true)
	if err := work(ctx); err != nil {
		return err
	}
	if tx != nil {
		return tx.Commit()
	}
	return nil
}
