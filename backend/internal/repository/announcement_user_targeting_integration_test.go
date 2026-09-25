//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAnnouncementRepo_PersistsUserTargeting(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := NewAnnouncementRepository(client)
	user := mustCreateUser(t, client, &service.User{Email: "announcement-target@test.com"})

	targeting := service.AnnouncementTargeting{AnyOf: []service.AnnouncementConditionGroup{{AllOf: []service.AnnouncementCondition{
		{Type: service.AnnouncementConditionTypeUser, Operator: service.AnnouncementOperatorIn, UserIDs: []int64{user.ID}},
	}}}}
	a := &service.Announcement{
		Title: "使用警告", Content: "请遵守使用条例", Status: service.AnnouncementStatusActive,
		NotifyMode: service.AnnouncementNotifyModePopup, Targeting: targeting,
	}
	require.NoError(t, repo.Create(ctx, a))

	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, targeting, got.Targeting)
	require.True(t, got.Targeting.Matches(user.ID, 0, nil))
	require.False(t, got.Targeting.Matches(user.ID+1, 0, nil))
}

func TestUserRepo_ListWithFilters_UserIDs(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := NewUserRepository(client, integrationDB)

	alice := mustCreateUser(t, client, &service.User{Email: "ids-filter-alice@test.com"})
	bob := mustCreateUser(t, client, &service.User{Email: "ids-filter-bob@test.com"})
	mustCreateUser(t, client, &service.User{Email: "ids-filter-carol@test.com"})
	params := pagination.PaginationParams{Page: 1, PageSize: 50, SortBy: "email", SortOrder: "asc"}

	users, page, err := repo.ListWithFilters(ctx, params,
		service.UserListFilters{Search: "ids-filter-", UserIDs: []int64{bob.ID, alice.ID}})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total, "Count 必须与结果集行数一致")
	require.Len(t, users, 2)
	require.Equal(t, alice.ID, users[0].ID)
	require.Equal(t, bob.ID, users[1].ID)

	users, page, err = repo.ListWithFilters(ctx, params, service.UserListFilters{Search: "ids-filter-"})
	require.NoError(t, err)
	require.EqualValues(t, 3, page.Total, "不传 UserIDs 时不按用户过滤")
	require.Len(t, users, 3)
}
