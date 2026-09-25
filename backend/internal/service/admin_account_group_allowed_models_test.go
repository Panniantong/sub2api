//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// groupAllowedModelsRepoStub 记录 BindGroups 与 SetGroupAllowedModels 的调用顺序。
type groupAllowedModelsRepoStub struct {
	accountRepoStubForBulkUpdate
	calls              []string
	groupAllowedModels map[int64][]string
}

func (s *groupAllowedModelsRepoStub) BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	s.calls = append(s.calls, "bind_groups")
	return s.accountRepoStubForBulkUpdate.BindGroups(ctx, accountID, groupIDs)
}

func (s *groupAllowedModelsRepoStub) SetGroupAllowedModels(_ context.Context, _ int64, allowed map[int64][]string) error {
	s.calls = append(s.calls, "set_group_allowed_models")
	s.groupAllowedModels = allowed
	return nil
}

func newGroupAllowedModelsAdminService() (*adminServiceImpl, *groupAllowedModelsRepoStub) {
	repo := &groupAllowedModelsRepoStub{
		accountRepoStubForBulkUpdate: accountRepoStubForBulkUpdate{
			getByIDAccounts: map[int64]*Account{
				7: {ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Extra: map[string]any{}},
			},
		},
	}
	groupRepo := &groupRepoStubForAdmin{
		getByIDByID: map[int64]*Group{
			1: {ID: 1, Platform: PlatformOpenAI},
			2: {ID: 2, Platform: PlatformOpenAI},
		},
	}
	return &adminServiceImpl{accountRepo: repo, groupRepo: groupRepo}, repo
}

func TestAdminService_UpdateAccountWritesGroupAllowedModelsAfterBindingGroups(t *testing.T) {
	svc, repo := newGroupAllowedModelsAdminService()
	groupIDs := []int64{1, 2}

	_, err := svc.UpdateAccount(context.Background(), 7, &UpdateAccountInput{
		GroupIDs:              &groupIDs,
		GroupAllowedModels:    map[int64][]string{2: {"gpt-5.5"}},
		SkipMixedChannelCheck: true,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"bind_groups", "set_group_allowed_models"}, repo.calls, "限制只作用于最终绑定的分组，必须在绑定之后写")
	require.Equal(t, map[int64][]string{2: {"gpt-5.5"}}, repo.groupAllowedModels)
}

func TestAdminService_UpdateAccountLeavesGroupAllowedModelsWhenOmitted(t *testing.T) {
	svc, repo := newGroupAllowedModelsAdminService()
	groupIDs := []int64{1}

	_, err := svc.UpdateAccount(context.Background(), 7, &UpdateAccountInput{
		GroupIDs:              &groupIDs,
		SkipMixedChannelCheck: true,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"bind_groups"}, repo.calls, "请求没带该字段时不能动已有的限制")
}

func TestAdminService_UpdateAccountRejectsInvalidGroupAllowedModelsBeforeWriting(t *testing.T) {
	svc, repo := newGroupAllowedModelsAdminService()
	groupIDs := []int64{1}

	_, err := svc.UpdateAccount(context.Background(), 7, &UpdateAccountInput{
		GroupIDs:              &groupIDs,
		GroupAllowedModels:    map[int64][]string{-1: {"gpt-5.5"}},
		SkipMixedChannelCheck: true,
	})

	require.Error(t, err)
	require.Empty(t, repo.calls)
	require.Empty(t, repo.updatedAccounts, "校验失败时不应写入任何账号数据")
}
