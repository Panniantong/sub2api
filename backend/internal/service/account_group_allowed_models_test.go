//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestNormalizeGroupAllowedModels(t *testing.T) {
	require.Nil(t, NormalizeGroupAllowedModels(nil))
	require.Nil(t, NormalizeGroupAllowedModels([]string{" ", ""}))
	require.Equal(t,
		[]string{"gpt-5.5", "claude-*"},
		NormalizeGroupAllowedModels([]string{" gpt-5.5 ", "", "claude-*", "gpt-5.5"}),
		"去掉空白、空项和重复项，保留填写顺序",
	)
}

func TestValidateGroupAllowedModels(t *testing.T) {
	require.NoError(t, ValidateGroupAllowedModels(nil))
	require.NoError(t, ValidateGroupAllowedModels(map[int64][]string{1: {"gpt-5.5"}, 2: nil}))

	require.Error(t, ValidateGroupAllowedModels(map[int64][]string{0: {"gpt-5.5"}}), "分组 ID 必须为正数")

	tooMany := make([]string, 0, maxGroupAllowedModels+1)
	for i := 0; i <= maxGroupAllowedModels; i++ {
		tooMany = append(tooMany, fmt.Sprintf("model-%d", i))
	}
	require.Error(t, ValidateGroupAllowedModels(map[int64][]string{1: tooMany}))

	require.Error(t, ValidateGroupAllowedModels(map[int64][]string{1: {strings.Repeat("m", maxGroupAllowedModelLength+1)}}))
}

func TestAccountIsModelAllowedInGroup(t *testing.T) {
	groupA, groupB := int64(1), int64(2)
	account := &Account{
		Platform: PlatformAnthropic,
		AccountGroups: []AccountGroup{
			{GroupID: groupA},
			{GroupID: groupB, AllowedModels: []string{"claude-sonnet-4-5-20250929", "claude-haiku-*"}},
		},
	}

	cases := []struct {
		name    string
		groupID *int64
		model   string
		want    bool
	}{
		{"没有分组上下文时不限制", nil, "claude-opus-4-8", true},
		{"分组没有设置限制时不限制", &groupA, "claude-opus-4-8", true},
		{"未绑定的分组不限制", func() *int64 { id := int64(3); return &id }(), "claude-opus-4-8", true},
		{"清单内的模型放行", &groupB, "claude-sonnet-4-5-20250929", true},
		{"末尾通配匹配", &groupB, "claude-haiku-4-5-20251001", true},
		{"客户端短别名按完整 ID 匹配", &groupB, "claude-sonnet-4-5", true},
		{"清单外的模型拒绝", &groupB, "claude-opus-4-8", false},
		{"没有指定模型时不限制", &groupB, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, account.IsModelAllowedInGroup(tc.groupID, tc.model))
		})
	}

	// 分组限制只能在账号自身支持的范围内收窄
	mapped := &Account{
		Platform:      PlatformAnthropic,
		Type:          AccountTypeAPIKey,
		Credentials:   map[string]any{"model_mapping": map[string]any{"claude-opus-4-8": "claude-opus-4-8"}},
		AccountGroups: []AccountGroup{{GroupID: groupB, AllowedModels: []string{"claude-opus-4-8", "claude-sonnet-4-6"}}},
	}
	require.True(t, mapped.IsModelSupportedInGroup(&groupB, "claude-opus-4-8"))
	require.False(t, mapped.IsModelSupportedInGroup(&groupB, "claude-sonnet-4-6"), "账号本身不支持的模型不会因为分组清单而放行")
}

func TestAccountsAllowedInGroupForModel(t *testing.T) {
	groupID := int64(5)
	accounts := []Account{
		{ID: 1, AccountGroups: []AccountGroup{{GroupID: groupID, AllowedModels: []string{"gpt-5.5"}}}},
		{ID: 2, AccountGroups: []AccountGroup{{GroupID: groupID}}},
	}

	same := accountsAllowedInGroupForModel(accounts, &groupID, "gpt-5.5")
	require.Len(t, same, 2)
	require.Same(t, &accounts[0], &same[0], "没有账号被排除时原样返回，不拷贝")

	filtered := accountsAllowedInGroupForModel(accounts, &groupID, "gpt-5.4")
	require.Len(t, filtered, 1)
	require.Equal(t, int64(2), filtered[0].ID)

	require.Len(t, accountsAllowedInGroupForModel(accounts, nil, "gpt-5.4"), 2)
}

// groupAllowedModelsFixture 构造走负载感知路径的 GatewayService：分组 10 下有两个账号，
// 优先级更高的账号 1 在本分组只允许 claude-sonnet-4-6，账号 2 不限制。
type groupAllowedModelsFixture struct {
	svc     *GatewayService
	ctx     context.Context
	groupID int64
	cache   *mockGatewayCacheForPlatform
	repo    *mockAccountRepoForPlatform
}

func newGroupAllowedModelsFixture(t *testing.T, sessionBindings map[string]int64, routing map[string][]int64) *groupAllowedModelsFixture {
	t.Helper()

	groupID := int64(10)
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{
				ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Priority: 1,
				Status: StatusActive, Schedulable: true, Concurrency: 5,
				AccountGroups: []AccountGroup{{GroupID: groupID, AllowedModels: []string{"claude-sonnet-4-6"}}},
				GroupIDs:      []int64{groupID},
			},
			{
				ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Priority: 2,
				Status: StatusActive, Schedulable: true, Concurrency: 5,
				AccountGroups: []AccountGroup{{GroupID: groupID}},
				GroupIDs:      []int64{groupID},
			},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	group := &Group{
		ID:                  groupID,
		Platform:            PlatformAnthropic,
		Status:              StatusActive,
		Hydrated:            true,
		ModelRoutingEnabled: len(routing) > 0,
		ModelRouting:        routing,
	}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cache := &mockGatewayCacheForPlatform{sessionBindings: sessionBindings}

	svc := &GatewayService{
		accountRepo:        repo,
		groupRepo:          &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}},
		cache:              cache,
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(&mockConcurrencyCache{}),
	}
	return &groupAllowedModelsFixture{
		svc:     svc,
		ctx:     context.WithValue(context.Background(), ctxkey.Group, group),
		groupID: groupID,
		cache:   cache,
		repo:    repo,
	}
}

func TestSelectAccountWithLoadAwareness_SkipsAccountRestrictedInGroup(t *testing.T) {
	t.Parallel()

	f := newGroupAllowedModelsFixture(t, nil, nil)
	result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-opus-4-8", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(2), result.Account.ID, "模型不在账号的分组清单里时，优先级更高也要跳过")

	result, err = f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(1), result.Account.ID, "清单内的模型照常按优先级选中")
}

func TestSelectAccountWithLoadAwareness_GroupRestrictionIgnoresStickyAccount(t *testing.T) {
	t.Parallel()

	f := newGroupAllowedModelsFixture(t, map[string]int64{"sticky": 1}, nil)
	result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "sticky", "claude-opus-4-8", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(2), result.Account.ID, "粘性账号在本分组不允许该模型时不得沿用")
	require.Equal(t, int64(2), f.cache.sessionBindings["sticky"], "粘性会话应重新绑定到允许的账号")
}

func TestSelectAccountWithLoadAwareness_GroupRestrictionFiltersRoutedAccounts(t *testing.T) {
	t.Parallel()

	f := newGroupAllowedModelsFixture(t, nil, map[string][]int64{"claude-opus-4-8": {1}})
	result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-opus-4-8", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(2), result.Account.ID, "模型路由指向的账号被分组限制时应回退到允许的账号")
}

func TestSelectAccountWithLoadAwareness_RejectsWhenGroupAllowsNoAccount(t *testing.T) {
	t.Parallel()

	f := newGroupAllowedModelsFixture(t, nil, nil)
	f.repo.accounts[1].AccountGroups[0].AllowedModels = []string{"claude-sonnet-4-6"}

	result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-opus-4-8", nil, "", 0)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, result)
}

func TestSelectAccountForModelWithExclusions_SkipsAccountRestrictedInGroup(t *testing.T) {
	t.Parallel()

	f := newGroupAllowedModelsFixture(t, nil, nil)
	account, err := f.svc.SelectAccountForModelWithExclusions(f.ctx, &f.groupID, "", "claude-opus-4-8", nil)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(2), account.ID)
}

func TestOpenAIEligibility_RejectsModelNotAllowedInGroup(t *testing.T) {
	groupID := int64(20)
	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		AccountGroups: []AccountGroup{{GroupID: groupID, AllowedModels: []string{"gpt-5.5"}}},
	}
	ctx := context.Background()

	require.Equal(t, "", openAICompatibleAccountEligibilityFailureReason(ctx, account, &groupID, PlatformOpenAI, "gpt-5.5", false, ""))
	require.Equal(t, "model_not_allowed_in_group",
		openAICompatibleAccountEligibilityFailureReason(ctx, account, &groupID, PlatformOpenAI, "gpt-5.4", false, ""))
	require.Equal(t, "", openAICompatibleAccountEligibilityFailureReason(ctx, account, nil, PlatformOpenAI, "gpt-5.4", false, ""),
		"没有分组上下文时不套用分组限制")

	scheduler := &defaultOpenAIAccountScheduler{}
	ok, reason := scheduler.isAccountRequestCompatibleReason(ctx, account, OpenAIAccountScheduleRequest{
		GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.4",
	})
	require.False(t, ok)
	require.Equal(t, "model_not_allowed_in_group", reason)

	ok, _ = scheduler.isAccountRequestCompatibleReason(ctx, account, OpenAIAccountScheduleRequest{
		GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.5",
	})
	require.True(t, ok)
}

func TestGeminiSelectBestAccount_SkipsAccountRestrictedInGroup(t *testing.T) {
	groupID := int64(30)
	accounts := []Account{
		{
			ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey, Priority: 1, Status: StatusActive, Schedulable: true,
			AccountGroups: []AccountGroup{{GroupID: groupID, AllowedModels: []string{"gemini-2.5-flash"}}},
		},
		{
			ID: 2, Platform: PlatformGemini, Type: AccountTypeAPIKey, Priority: 2, Status: StatusActive, Schedulable: true,
			AccountGroups: []AccountGroup{{GroupID: groupID}},
		},
	}
	svc := &GeminiMessagesCompatService{}

	selected := svc.selectBestGeminiAccount(context.Background(), &groupID, accounts, "gemini-2.5-pro", nil, PlatformGemini, false)
	require.NotNil(t, selected)
	require.Equal(t, int64(2), selected.ID)

	selected = svc.selectBestGeminiAccount(context.Background(), &groupID, accounts, "gemini-2.5-flash", nil, PlatformGemini, false)
	require.NotNil(t, selected)
	require.Equal(t, int64(1), selected.ID)
}

func TestGetAvailableModels_AppliesGroupAllowedModels(t *testing.T) {
	groupID := int64(40)
	repo := &modelsListAccountRepoStub{
		byGroup: map[int64][]Account{
			groupID: {
				{
					ID:       1,
					Platform: PlatformAnthropic,
					Credentials: map[string]any{"model_mapping": map[string]any{
						"claude-opus-4-8":   "claude-opus-4-8",
						"claude-sonnet-4-6": "claude-sonnet-4-6",
					}},
					AccountGroups: []AccountGroup{{GroupID: groupID, AllowedModels: []string{"claude-sonnet-4-6"}}},
				},
				{
					// 没有映射（支持全部模型），但在本分组只允许一个模型
					ID:            2,
					Platform:      PlatformAnthropic,
					AccountGroups: []AccountGroup{{GroupID: groupID, AllowedModels: []string{"claude-haiku-4-5", "claude-3-*"}}},
				},
			},
		},
	}
	svc := &GatewayService{accountRepo: repo}

	models := svc.GetAvailableModels(context.Background(), &groupID, PlatformAnthropic)
	require.Equal(t, []string{"claude-haiku-4-5", "claude-sonnet-4-6"}, models, "只公布分组允许的模型，通配项不展开")
}

func TestOpenAIConfiguredCodexModelIDsForGroup_AppliesGroupAllowedModels(t *testing.T) {
	group := &Group{ID: 50, Platform: PlatformOpenAI}
	accounts := []Account{
		{
			ID:       1,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{"model_mapping": map[string]any{
				"gpt-5.5": "gpt-5.5",
				"gpt-5.4": "gpt-5.4",
			}},
			AccountGroups: []AccountGroup{{GroupID: group.ID, AllowedModels: []string{"gpt-5.5"}}},
		},
		{
			// 没有映射的 OpenAI 账号默认会补齐全部内置模型；在本分组被限制时只按清单公布
			ID:            2,
			Platform:      PlatformOpenAI,
			AccountGroups: []AccountGroup{{GroupID: group.ID, AllowedModels: []string{"gpt-5.3-codex"}}},
		},
	}

	require.Equal(t, []string{"gpt-5.3-codex", "gpt-5.5"}, openAIConfiguredCodexModelIDsForGroup(accounts, group))

	// 不带分组时保持原有行为：映射全部公布，未映射账号补齐内置模型
	unrestricted := openAIConfiguredCodexModelIDsForGroup(accounts, nil)
	require.Contains(t, unrestricted, "gpt-5.4")
	require.Greater(t, len(unrestricted), 3)
}

func TestProjectAccountModelsBody_FiltersUnmappedAccountByGroup(t *testing.T) {
	group := &Group{ID: 60, Platform: PlatformOpenAI}
	account := &Account{
		ID:            1,
		Platform:      PlatformOpenAI,
		AccountGroups: []AccountGroup{{GroupID: group.ID, AllowedModels: []string{"gpt-5.5"}}},
	}
	body := []byte(`{"object":"list","data":[{"id":"gpt-5.5","owned_by":"openai"},{"id":"gpt-5.4","owned_by":"openai"}]}`)

	projected, err := projectAccountModelsBody(body, account, group, false)
	require.NoError(t, err)
	var parsed struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(projected, &parsed))
	require.Equal(t, "list", parsed.Object)
	require.Len(t, parsed.Data, 1)
	require.Equal(t, "gpt-5.5", parsed.Data[0].ID)
	require.Equal(t, "openai", parsed.Data[0].OwnedBy, "保留条目的其他字段")

	unchanged, err := projectAccountModelsBody(body, account, nil, false)
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(unchanged), "没有分组时原样返回")
}
