package repository

import (
	"context"
	"fmt"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const userHierarchyLockKey = "user-management:administrator-roles:v1"

// The supplied client must be transaction-bound. PostgreSQL owns the lock until
// commit/rollback, including an outer transaction; no process-local mutex can
// substitute for this across application replicas.
func lockUserHierarchy(ctx context.Context, client *dbent.Client) (func(), error) {
	if client.Driver().Dialect() == dialect.Postgres {
		if err := client.Driver().Exec(ctx, "SELECT pg_advisory_xact_lock($1)", []any{advisoryLockHash(userHierarchyLockKey)}, nil); err != nil {
			return nil, err
		}
		return func() {}, nil
	}
	return repositoryScopedKeyLocks.lock(userHierarchyLockKey), nil
}

func lockHierarchyUsers(ctx context.Context, client *dbent.Client, ids ...int64) (map[int64]*dbent.User, error) {
	query := client.User.Query().Where(dbuser.IDIn(ids...), dbuser.DeletedAtIsNil()).Order(dbent.Asc(dbuser.FieldID))
	if client.Driver().Dialect() == dialect.Postgres {
		query = query.ForUpdate()
	}
	rows, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	users := make(map[int64]*dbent.User, len(rows))
	for _, user := range rows {
		users[user.ID] = user
	}
	return users, nil
}

func verifyHierarchyActor(actor *dbent.User, needsSuper bool) error {
	if actor == nil || actor.Status != service.StatusActive || !service.IsAdminRole(actor.Role) || (needsSuper && actor.Role != service.RoleSuperAdmin) {
		return infraerrors.Forbidden("SUPER_ADMIN_REQUIRED", "操作者权限已变更或不足，请刷新后重试")
	}
	return nil
}

func guardUserHierarchyUpdate(ctx context.Context, client *dbent.Client, incoming *service.User, fields service.UserUpdateFields) (func(), error) {
	actorID, managed := service.UserManagementActor(ctx)
	if !fields.Role && !fields.Status && !managed {
		return func() {}, nil
	}
	release, err := lockUserHierarchy(ctx, client)
	if err != nil {
		return nil, err
	}
	check := func() error {
		users, err := lockHierarchyUsers(ctx, client, incoming.ID, actorID)
		if err != nil {
			return err
		}
		current := users[incoming.ID]
		if current == nil {
			return service.ErrUserNotFound
		}
		roleChange := fields.Role && incoming.Role != current.Role
		if managed || roleChange {
			if err := verifyHierarchyActor(users[actorID], roleChange || service.IsAdminRole(current.Role)); err != nil {
				return err
			}
			if roleChange && incoming.ID == actorID {
				return infraerrors.Forbidden("SELF_DEMOTION_FORBIDDEN", "不能修改自己的管理员角色")
			}
		}
		if fields.Role && incoming.Role != service.RoleUser && !service.IsAdminRole(incoming.Role) {
			return infraerrors.BadRequest("INVALID_USER_ROLE", "无效的用户角色")
		}
		if fields.Status && incoming.Status != service.StatusActive && service.IsAdminRole(current.Role) {
			return infraerrors.Forbidden("ADMIN_PROTECTED", "不能禁用管理员账号")
		}
		if !roleChange || !service.IsAdminRole(current.Role) {
			return nil
		}
		remaining := client.User.Query().Where(dbuser.IDNEQ(current.ID), dbuser.StatusEQ(service.StatusActive), dbuser.DeletedAtIsNil())
		if current.Role == service.RoleSuperAdmin {
			remaining = remaining.Where(dbuser.RoleEQ(service.RoleSuperAdmin))
		} else {
			remaining = remaining.Where(dbuser.RoleIn(service.RoleSuperAdmin, service.RoleAdmin))
		}
		exists, err := remaining.Exist(ctx)
		if err != nil {
			return err
		}
		if !exists {
			return infraerrors.Forbidden("LAST_SUPER_ADMIN", "不能降级最后一个有效管理员")
		}
		return nil
	}
	if err := check(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func guardUserHierarchyCreate(ctx context.Context, client *dbent.Client, user *service.User) (func(), error) {
	if !service.IsAdminRole(user.Role) {
		return func() {}, nil
	}
	release, err := lockUserHierarchy(ctx, client)
	if err != nil {
		return nil, err
	}
	if actorID, managed := service.UserManagementActor(ctx); managed {
		actors, err := lockHierarchyUsers(ctx, client, actorID)
		if err == nil {
			err = verifyHierarchyActor(actors[actorID], true)
		}
		if err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

func guardUserHierarchyDelete(ctx context.Context, client *dbent.Client, id int64) (func(), error) {
	release, err := lockUserHierarchy(ctx, client)
	if err != nil {
		return nil, err
	}
	actorID, managed := service.UserManagementActor(ctx)
	ids := []int64{id}
	if managed {
		ids = append(ids, actorID)
	}
	users, err := lockHierarchyUsers(ctx, client, ids...)
	if err == nil {
		if users[id] == nil {
			err = service.ErrUserNotFound
		} else if managed {
			err = verifyHierarchyActor(users[actorID], false)
		} else if service.IsAdminRole(users[id].Role) {
			err = infraerrors.Forbidden("ADMIN_PROTECTED", fmt.Sprintf("不能删除管理员账号 %d", id))
		}
		if err == nil && service.IsAdminRole(users[id].Role) {
			err = infraerrors.Forbidden("ADMIN_PROTECTED", fmt.Sprintf("不能删除管理员账号 %d", id))
		}
	}
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}
