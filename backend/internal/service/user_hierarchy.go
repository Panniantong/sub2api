package service

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

type userManagementActorKey struct{}

// WithUserManagementActor carries the server-authenticated actor to the final
// repository transaction. It is never populated from request JSON.
func WithUserManagementActor(ctx context.Context, actorID int64) context.Context {
	return context.WithValue(ctx, userManagementActorKey{}, actorID)
}

func UserManagementActor(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(userManagementActorKey{}).(int64)
	return id, ok
}

func (s *adminServiceImpl) authorizeManagedUserResource(ctx context.Context, targetID int64) error {
	actorID, managed := UserManagementActor(ctx)
	if !managed {
		return nil
	} // Non-HTTP internal callers retain their own authority.
	if s.userRepo == nil || actorID <= 0 {
		return infraerrors.Forbidden("USER_RESOURCE_FORBIDDEN", "User resource authorization unavailable")
	}
	actor, err := s.userRepo.GetByID(ctx, actorID)
	if err != nil {
		return err
	}
	target, err := s.userRepo.GetByID(ctx, targetID)
	if err != nil {
		return err
	}
	if !actor.IsActive() || !actor.IsAdmin() || (target.IsAdmin() && !actor.IsSuperAdmin()) {
		return infraerrors.Forbidden("SUPER_ADMIN_REQUIRED", "只有超级管理员可以管理管理员账号的密钥和订阅")
	}
	return nil
}

// Only a live super administrator may grant roles or change another
// administrator. Actor identity comes from authenticated server context.
func (s *adminServiceImpl) authorizeUserRoleChange(ctx context.Context, actorID int64, target *User, requestedRole string) error {
	roleChange := target == nil && IsAdminRole(requestedRole) || target != nil && requestedRole != "" && requestedRole != target.Role
	protectedTarget := target != nil && target.IsAdmin()
	if !roleChange && !protectedTarget {
		return nil
	}
	if actorID <= 0 {
		return infraerrors.Forbidden("SUPER_ADMIN_REQUIRED", "只有超级管理员可以修改角色或管理管理员账号")
	}
	actor, err := s.userRepo.GetByID(ctx, actorID)
	if err != nil {
		return err
	}
	if !actor.IsActive() || !actor.IsSuperAdmin() {
		return infraerrors.Forbidden("SUPER_ADMIN_REQUIRED", "只有超级管理员可以修改角色或管理管理员账号")
	}
	if target != nil && target.ID == actorID && requestedRole != "" && requestedRole != actor.Role {
		return infraerrors.Forbidden("SELF_DEMOTION_FORBIDDEN", "不能修改自己的管理员角色")
	}
	return nil
}

func (s *adminServiceImpl) ensureNotLastSuperAdmin(ctx context.Context) error {
	noSubs := false
	_, result, err := s.userRepo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 1}, UserListFilters{Role: RoleSuperAdmin, IncludeSubscriptions: &noSubs})
	if err != nil {
		return err
	}
	if result == nil || result.Total <= 1 {
		return infraerrors.Forbidden("LAST_SUPER_ADMIN", "不能降级最后一个超级管理员")
	}
	return nil
}
