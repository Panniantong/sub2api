package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type annTargetAnnouncementRepo struct {
	AnnouncementRepository
	items []Announcement
}

func (r *annTargetAnnouncementRepo) GetByID(_ context.Context, id int64) (*Announcement, error) {
	for i := range r.items {
		if r.items[i].ID == id {
			item := r.items[i]
			return &item, nil
		}
	}
	return nil, ErrAnnouncementNotFound
}

func (r *annTargetAnnouncementRepo) ListActive(context.Context, time.Time) ([]Announcement, error) {
	return append([]Announcement(nil), r.items...), nil
}

type annTargetReadRepo struct {
	AnnouncementReadRepository
	marked [][2]int64 // {announcementID, userID}
}

func (r *annTargetReadRepo) MarkRead(_ context.Context, announcementID, userID int64, _ time.Time) error {
	r.marked = append(r.marked, [2]int64{announcementID, userID})
	return nil
}

func (*annTargetReadRepo) GetReadMapByUser(context.Context, int64, []int64) (map[int64]time.Time, error) {
	return map[int64]time.Time{}, nil
}

func (*annTargetReadRepo) GetReadMapByUsers(context.Context, int64, []int64) (map[int64]time.Time, error) {
	return map[int64]time.Time{}, nil
}

type annTargetUserRepo struct {
	UserRepository
	users       []User
	listFilters []UserListFilters
}

func (r *annTargetUserRepo) GetByID(_ context.Context, id int64) (*User, error) {
	for i := range r.users {
		if r.users[i].ID == id {
			user := r.users[i]
			return &user, nil
		}
	}
	return nil, ErrUserNotFound
}

func (r *annTargetUserRepo) ListWithFilters(_ context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error) {
	r.listFilters = append(r.listFilters, filters)
	out := make([]User, 0, len(r.users))
	for _, user := range r.users {
		if len(filters.UserIDs) == 0 || slices.Contains(filters.UserIDs, user.ID) {
			out = append(out, user)
		}
	}
	return out, &pagination.PaginationResult{Total: int64(len(out)), Page: params.Page, PageSize: params.PageSize}, nil
}

type annTargetSubRepo struct {
	UserSubscriptionRepository
}

func (annTargetSubRepo) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	return nil, nil
}

func newUserTargetedAnnouncementService(t *testing.T) (*AnnouncementService, *annTargetUserRepo, *annTargetReadRepo) {
	t.Helper()
	warning := Announcement{
		ID: 1, Title: "使用警告", Content: "请遵守使用条例", Status: AnnouncementStatusActive, NotifyMode: AnnouncementNotifyModePopup,
		Targeting: AnnouncementTargeting{AnyOf: []AnnouncementConditionGroup{{AllOf: []AnnouncementCondition{
			{Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorIn, UserIDs: []int64{7}},
		}}}},
	}
	broadcast := Announcement{ID: 2, Title: "维护通知", Content: "今晚维护", Status: AnnouncementStatusActive, NotifyMode: AnnouncementNotifyModeSilent}
	users := &annTargetUserRepo{users: []User{{ID: 7, Email: "alice@example.com"}, {ID: 8, Email: "bob@example.com"}}}
	reads := &annTargetReadRepo{}
	svc := NewAnnouncementService(&annTargetAnnouncementRepo{items: []Announcement{warning, broadcast}}, reads, users, annTargetSubRepo{})
	return svc, users, reads
}

func announcementIDs(items []UserAnnouncement) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Announcement.ID)
	}
	return ids
}

func TestAnnouncementService_UserTargetedAnnouncementOnlyVisibleToThatUser(t *testing.T) {
	svc, _, _ := newUserTargetedAnnouncementService(t)

	alice, err := svc.ListForUser(context.Background(), 7, false)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 1}, announcementIDs(alice))

	bob, err := svc.ListForUser(context.Background(), 8, false)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, announcementIDs(bob), "其他用户看不到针对 alice 的警告")
}

func TestAnnouncementService_MarkReadRejectsUsersOutsideTheTarget(t *testing.T) {
	svc, _, reads := newUserTargetedAnnouncementService(t)

	require.ErrorIs(t, svc.MarkRead(context.Background(), 8, 1), ErrAnnouncementNotFound)
	require.NoError(t, svc.MarkRead(context.Background(), 7, 1))
	require.Equal(t, [][2]int64{{1, 7}}, reads.marked)
}

func TestAnnouncementService_ReadStatusListsOnlyTargetedUsers(t *testing.T) {
	svc, users, _ := newUserTargetedAnnouncementService(t)
	params := pagination.PaginationParams{Page: 1, PageSize: 20}

	statuses, page, err := svc.ListUserReadStatus(context.Background(), 1, params, "")
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Len(t, statuses, 1)
	require.Equal(t, int64(7), statuses[0].UserID)
	require.True(t, statuses[0].Eligible)
	require.Equal(t, []int64{7}, users.listFilters[0].UserIDs)

	// 面向全部用户的公告照旧列出所有用户
	statuses, _, err = svc.ListUserReadStatus(context.Background(), 2, params, "")
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	require.Nil(t, users.listFilters[1].UserIDs)
}
