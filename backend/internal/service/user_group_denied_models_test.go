//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNormalizeUserGroupDeniedModels(t *testing.T) {
	models, err := NormalizeUserGroupDeniedModels(nil)
	require.NoError(t, err)
	require.Nil(t, models)

	models, err = NormalizeUserGroupDeniedModels([]string{" ", ""})
	require.NoError(t, err)
	require.Nil(t, models, "只有空白的清单等同于不限制")

	models, err = NormalizeUserGroupDeniedModels([]string{" gpt-6-luna ", "GPT-6-LUNA", "", "gpt-6-*"})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-6-luna", "gpt-6-*"}, models, "去掉空白与空项、按小写去重、保留填写顺序")

	_, err = NormalizeUserGroupDeniedModels([]string{"gpt-*-luna"})
	require.Error(t, err, "* 只能出现在末尾")

	_, err = NormalizeUserGroupDeniedModels([]string{strings.Repeat("m", maxUserGroupDeniedModelLength+1)})
	require.Error(t, err)

	tooMany := make([]string, 0, maxUserGroupDeniedModels+1)
	for i := 0; i <= maxUserGroupDeniedModels; i++ {
		tooMany = append(tooMany, fmt.Sprintf("model-%d", i))
	}
	_, err = NormalizeUserGroupDeniedModels(tooMany)
	require.Error(t, err)
}

func TestUserGroupDeniesModel(t *testing.T) {
	denied := []string{"gpt-6-luna", "claude-opus-*", "gemini-2.5-pro"}
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-6-luna", true},
		{"GPT-6-Luna", true},
		{"gpt-6-sol", false},
		{"claude-opus-4-8", true},
		{"claude-sonnet-4-6", false},
		{"models/gemini-2.5-pro", true},
		{"", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, UserGroupDeniesModel(denied, tc.model), tc.model)
	}
	require.False(t, UserGroupDeniesModel(nil, "gpt-6-luna"), "没有禁用清单时不拦截")

	require.Equal(t, "gpt-6-luna", FirstUserGroupDeniedModel(denied, []string{"gpt-6-sol", " gpt-6-luna "}))
	require.Equal(t, "", FirstUserGroupDeniedModel(denied, []string{"gpt-6-sol"}))

	require.Equal(t, []string{"gpt-6-sol"}, FilterUserGroupDeniedModelIDs([]string{"gpt-6-luna", "gpt-6-sol"}, denied))
	ids := []string{"gpt-6-luna"}
	require.Equal(t, ids, FilterUserGroupDeniedModelIDs(ids, nil))
}

// deniedModelsRateRepoStub 记录禁用模型的写入，并为认证快照提供查询结果。
type deniedModelsRateRepoStub struct {
	UserGroupRateRepository
	denied      map[int64][]string
	lookupErr   error
	lookups     int
	syncGroupID int64
	syncEntries []GroupUserDeniedModelsInput
	syncCalls   int
	cleared     []int64
}

func (s *deniedModelsRateRepoStub) GetDeniedModelsByUserAndGroup(_ context.Context, _, groupID int64) ([]string, error) {
	s.lookups++
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	return s.denied[groupID], nil
}

func (s *deniedModelsRateRepoStub) GetRPMOverrideByUserAndGroup(context.Context, int64, int64) (*int, error) {
	return nil, nil
}

func (s *deniedModelsRateRepoStub) SyncGroupDeniedModels(_ context.Context, groupID int64, entries []GroupUserDeniedModelsInput) error {
	s.syncCalls++
	s.syncGroupID = groupID
	s.syncEntries = entries
	return nil
}

func (s *deniedModelsRateRepoStub) ClearGroupDeniedModels(_ context.Context, groupID int64) error {
	s.cleared = append(s.cleared, groupID)
	return nil
}

func TestAdminService_BatchSetGroupUserDeniedModelsNormalizesAndInvalidatesAuthCache(t *testing.T) {
	repo := &deniedModelsRateRepoStub{}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{userGroupRateRepo: repo, authCacheInvalidator: invalidator}

	err := svc.BatchSetGroupUserDeniedModels(context.Background(), 7, []GroupUserDeniedModelsInput{
		{UserID: 1, DeniedModels: []string{" gpt-6-luna ", "GPT-6-LUNA", "gpt-6-*"}},
		{UserID: 2, DeniedModels: []string{" "}},
	})

	require.NoError(t, err)
	require.Equal(t, int64(7), repo.syncGroupID)
	require.Equal(t, []GroupUserDeniedModelsInput{
		{UserID: 1, DeniedModels: []string{"gpt-6-luna", "gpt-6-*"}},
		{UserID: 2, DeniedModels: nil},
	}, repo.syncEntries)
	require.Equal(t, []int64{7}, invalidator.groupIDs, "禁用模型嵌在认证快照里，写入后必须失效该分组的缓存")
}

func TestAdminService_BatchSetGroupUserDeniedModelsRejectsInvalidInputBeforeWriting(t *testing.T) {
	cases := map[string][]GroupUserDeniedModelsInput{
		"非法用户":   {{UserID: 0, DeniedModels: []string{"gpt-6-luna"}}},
		"重复用户":   {{UserID: 1, DeniedModels: []string{"a"}}, {UserID: 1, DeniedModels: []string{"b"}}},
		"通配不在末尾": {{UserID: 1, DeniedModels: []string{"gpt-*-luna"}}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &deniedModelsRateRepoStub{}
			invalidator := &authCacheInvalidatorStub{}
			svc := &adminServiceImpl{userGroupRateRepo: repo, authCacheInvalidator: invalidator}

			require.Error(t, svc.BatchSetGroupUserDeniedModels(context.Background(), 7, entries))
			require.Zero(t, repo.syncCalls)
			require.Empty(t, invalidator.groupIDs)
		})
	}
}

func TestAdminService_ClearGroupUserDeniedModelsInvalidatesAuthCache(t *testing.T) {
	repo := &deniedModelsRateRepoStub{}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{userGroupRateRepo: repo, authCacheInvalidator: invalidator}

	require.NoError(t, svc.ClearGroupUserDeniedModels(context.Background(), 7))
	require.Equal(t, []int64{7}, repo.cleared)
	require.Equal(t, []int64{7}, invalidator.groupIDs)
}

func TestAdminService_UserDeniedModelsRejectedInSimpleMode(t *testing.T) {
	repo := &deniedModelsRateRepoStub{}
	svc := &adminServiceImpl{userGroupRateRepo: repo, cfg: &config.Config{RunMode: config.RunModeSimple}}

	require.Error(t, svc.BatchSetGroupUserDeniedModels(context.Background(), 7, []GroupUserDeniedModelsInput{{UserID: 1, DeniedModels: []string{"gpt-6-luna"}}}))
	require.Error(t, svc.ClearGroupUserDeniedModels(context.Background(), 7))
	require.Zero(t, repo.syncCalls)
	require.Empty(t, repo.cleared)
}

func deniedModelsAuthTestService(rateRepo UserGroupRateRepository, cache *authCacheStub) *APIKeyService {
	groupID := int64(9)
	repo := &authRepoStub{
		getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
			return &APIKey{
				ID: 1, UserID: 2, GroupID: &groupID, Status: StatusActive,
				User: &User{ID: 2, Status: StatusActive, Role: RoleUser, Balance: 10, Concurrency: 3},
				Group: &Group{
					ID: groupID, Name: "g", Platform: PlatformOpenAI, Status: StatusActive,
					SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 1,
				},
			}, nil
		},
	}
	cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60, NegativeTTLSeconds: 30}}
	return NewAPIKeyService(repo, nil, nil, nil, rateRepo, cache, cfg)
}

func TestAPIKeyService_GetByKeyLoadsUserGroupDeniedModels(t *testing.T) {
	rateRepo := &deniedModelsRateRepoStub{denied: map[int64][]string{9: {"gpt-6-luna"}}}
	cache := &authCacheStub{}
	svc := deniedModelsAuthTestService(rateRepo, cache)

	apiKey, err := svc.GetByKey(context.Background(), "k-denied")
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-6-luna"}, apiKey.DeniedModelsInGroup())
	require.Len(t, cache.setAuthKeys, 1)

	// 缓存命中路径从快照恢复，禁用模型必须随快照一起保留
	roundTrip := svc.snapshotToAPIKey("k-denied", svc.snapshotFromAPIKey(context.Background(), apiKey))
	require.Equal(t, []string{"gpt-6-luna"}, roundTrip.DeniedModelsInGroup())
	require.Equal(t, apiKeyAuthSnapshotVersion, svc.snapshotFromAPIKey(context.Background(), apiKey).Version)
}

func TestAPIKeyService_GetByKeyFailsClosedWhenDeniedModelsLookupFails(t *testing.T) {
	rateRepo := &deniedModelsRateRepoStub{lookupErr: errors.New("db down")}
	cache := &authCacheStub{}
	svc := deniedModelsAuthTestService(rateRepo, cache)

	_, err := svc.GetByKey(context.Background(), "k-denied")
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrAPIKeyNotFound), "查询失败不能被当成 Key 无效")
	require.Empty(t, cache.setAuthKeys, "查询失败时不能把缺少禁用模型的快照写进缓存")
}
