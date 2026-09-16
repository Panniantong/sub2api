//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUserHierarchyRoleChangesRequireLiveSuperAdmin(t *testing.T) {
	for _, tt := range []struct {
		actor   string
		active  bool
		wantErr bool
	}{
		{RoleUser, true, true}, {RoleAdmin, true, true}, {RoleSuperAdmin, false, true}, {RoleSuperAdmin, true, false},
	} {
		t.Run(tt.actor+map[bool]string{true: "_active", false: "_disabled"}[tt.active], func(t *testing.T) {
			status := StatusDisabled
			if tt.active {
				status = StatusActive
			}
			repo := &userRepoStub{usersByID: map[int64]*User{1: {ID: 1, Role: tt.actor, Status: status}, 2: {ID: 2, Role: RoleUser, Status: StatusActive}}}
			svc := &adminServiceImpl{userRepo: repo}
			_, err := svc.UpdateUser(context.Background(), 2, &UpdateUserInput{ActorAdminID: 1, Role: RoleSuperAdmin})
			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, repo.updated)
			} else {
				require.NoError(t, err)
				require.Equal(t, RoleSuperAdmin, repo.updated[0].Role)
			}
		})
	}
}

func TestUserHierarchyAdminCannotEditSuperAdminOrSelfPromote(t *testing.T) {
	for _, target := range []int64{1, 2} {
		repo := &userRepoStub{usersByID: map[int64]*User{1: {ID: 1, Role: RoleAdmin, Status: StatusActive}, 2: {ID: 2, Role: RoleSuperAdmin, Status: StatusActive}}}
		svc := &adminServiceImpl{userRepo: repo}
		_, err := svc.UpdateUser(context.Background(), target, &UpdateUserInput{ActorAdminID: 1, Role: RoleSuperAdmin, Password: "changed-password"})
		require.Error(t, err)
		require.Empty(t, repo.updated)
	}
}

func TestUserHierarchyCreationCannotGrantAdminWithoutSuperAdmin(t *testing.T) {
	repo := &userRepoStub{usersByID: map[int64]*User{1: {ID: 1, Role: RoleAdmin, Status: StatusActive}}}
	svc := &adminServiceImpl{userRepo: repo}
	for _, role := range []string{RoleAdmin, RoleSuperAdmin} {
		_, err := svc.CreateUser(context.Background(), &CreateUserInput{ActorAdminID: 1, Role: role, Email: "created@example.invalid", Password: "test-password"})
		require.Error(t, err)
		require.Empty(t, repo.created)
	}
	_, err := svc.CreateUser(context.Background(), &CreateUserInput{ActorAdminID: 1, Role: RoleUser, Email: "created@example.invalid", Password: "test-password"})
	require.NoError(t, err)
}
