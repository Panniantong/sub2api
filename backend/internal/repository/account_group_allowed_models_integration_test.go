//go:build integration

package repository

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/ent/accountgroup"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *AccountRepoSuite) groupAllowedModelsByGroup(accountID int64) map[int64][]string {
	s.T().Helper()
	account, err := s.repo.GetByID(s.ctx, accountID)
	s.Require().NoError(err)
	out := make(map[int64][]string, len(account.AccountGroups))
	for _, ag := range account.AccountGroups {
		out[ag.GroupID] = ag.AllowedModels
	}
	return out
}

func (s *AccountRepoSuite) countAccountGroupsOutbox(accountID int64) int {
	s.T().Helper()
	var count int
	s.Require().NoError(scanSingleRow(
		s.ctx,
		s.repo.sql,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1 AND event_type = $2",
		[]any{accountID, service.SchedulerOutboxEventAccountGroupsChanged},
		&count,
	))
	return count
}

func (s *AccountRepoSuite) TestGroupAllowedModels_SetBindAndLoad() {
	g1 := mustCreateGroup(s.T(), s.client, &service.Group{Name: "allowed-models-g1"})
	g2 := mustCreateGroup(s.T(), s.client, &service.Group{Name: "allowed-models-g2"})
	g3 := mustCreateGroup(s.T(), s.client, &service.Group{Name: "allowed-models-g3"})
	account := mustCreateAccount(s.T(), s.client, &service.Account{Name: "allowed-models-acc"})
	s.Require().NoError(s.repo.BindGroups(s.ctx, account.ID, []int64{g1.ID, g2.ID}))

	// 归一化后写入，只作用于已绑定的分组，未绑定的 g3 被忽略
	before := s.countAccountGroupsOutbox(account.ID)
	s.Require().NoError(s.repo.SetGroupAllowedModels(s.ctx, account.ID, map[int64][]string{
		g2.ID: {" gpt-5.5 ", "gpt-5.5", "gpt-5.3-*"},
		g3.ID: {"ignored"},
	}))
	s.Require().Equal(map[int64][]string{g1.ID: nil, g2.ID: {"gpt-5.5", "gpt-5.3-*"}}, s.groupAllowedModelsByGroup(account.ID))
	s.Require().Equal(before+1, s.countAccountGroupsOutbox(account.ID), "有变化时要通知调度器刷新账号缓存")

	// 相同内容再写一次不产生变化，也不重复通知
	s.Require().NoError(s.repo.SetGroupAllowedModels(s.ctx, account.ID, map[int64][]string{g2.ID: {"gpt-5.5", "gpt-5.3-*"}}))
	s.Require().Equal(before+1, s.countAccountGroupsOutbox(account.ID))

	// 调整分组时整体删除重建，仍保留的 g2 沿用原有限制，新加入的 g3 不限制
	s.Require().NoError(s.repo.BindGroups(s.ctx, account.ID, []int64{g2.ID, g3.ID}))
	s.Require().Equal(map[int64][]string{g2.ID: {"gpt-5.5", "gpt-5.3-*"}, g3.ID: nil}, s.groupAllowedModelsByGroup(account.ID))

	// 覆盖写：没有列出的分组恢复为不限制，数据库里是 NULL 而不是空数组
	s.Require().NoError(s.repo.SetGroupAllowedModels(s.ctx, account.ID, map[int64][]string{g3.ID: {"gpt-5.4"}}))
	s.Require().Equal(map[int64][]string{g2.ID: nil, g3.ID: {"gpt-5.4"}}, s.groupAllowedModelsByGroup(account.ID))
	row, err := s.client.AccountGroup.Query().
		Where(accountgroup.AccountIDEQ(account.ID), accountgroup.GroupIDEQ(g2.ID)).
		Only(s.ctx)
	s.Require().NoError(err)
	s.Require().Nil(row.AllowedModels)

	// 从分组移除后，重新加入时不会带回旧限制
	s.Require().NoError(s.repo.BindGroups(s.ctx, account.ID, []int64{g2.ID}))
	s.Require().NoError(s.repo.BindGroups(s.ctx, account.ID, []int64{g2.ID, g3.ID}))
	s.Require().Equal(map[int64][]string{g2.ID: nil, g3.ID: nil}, s.groupAllowedModelsByGroup(account.ID))
}

func (s *AccountRepoSuite) TestGroupAllowedModels_CreateWithAccountGroups() {
	g1 := mustCreateGroup(s.T(), s.client, &service.Group{Name: "allowed-models-create-g1"})
	g2 := mustCreateGroup(s.T(), s.client, &service.Group{Name: "allowed-models-create-g2"})
	account := &service.Account{
		Name:        "allowed-models-create",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Credentials: map[string]any{},
		Extra:       map[string]any{},
	}
	s.Require().NoError(s.repo.CreateWithAccountGroups(s.ctx, account, []service.AccountGroup{
		{GroupID: g1.ID, Priority: 1},
		{GroupID: g2.ID, Priority: 2, AllowedModels: []string{"gpt-5.5", " "}},
	}))
	s.Require().Equal(map[int64][]string{g1.ID: nil, g2.ID: {"gpt-5.5"}}, s.groupAllowedModelsByGroup(account.ID))
}

func (s *GroupRepoSuite) TestCreateFromSourceCopiesGroupAllowedModels() {
	source := &service.Group{
		Name:             "allowed-models-source",
		Platform:         service.PlatformOpenAI,
		RateMultiplier:   1,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	s.Require().NoError(s.repo.Create(s.ctx, source))

	var accountID int64
	s.Require().NoError(scanSingleRow(
		s.ctx,
		s.tx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		[]any{"allowed-models-source-acc", service.PlatformOpenAI, service.AccountTypeOAuth},
		&accountID,
	))
	_, err := s.tx.ExecContext(
		s.ctx,
		`INSERT INTO account_groups (account_id, group_id, priority, allowed_models, created_at)
		 VALUES ($1, $2, 5, '["gpt-5.5"]'::jsonb, NOW())`,
		accountID,
		source.ID,
	)
	s.Require().NoError(err)

	duplicate := &service.Group{
		Name:                 "allowed-models-source (Copy)",
		Platform:             source.Platform,
		RateMultiplier:       source.RateMultiplier,
		Status:               "inactive",
		SubscriptionType:     source.SubscriptionType,
		DuplicateOperationID: strings.Repeat("b", 64),
	}
	s.Require().NoError(s.repo.CreateFromSource(s.ctx, duplicate, source.ID))

	var copied string
	s.Require().NoError(scanSingleRow(
		s.ctx,
		s.tx,
		"SELECT allowed_models::text FROM account_groups WHERE account_id = $1 AND group_id = $2",
		[]any{accountID, duplicate.ID},
		&copied,
	))
	s.Require().JSONEq(`["gpt-5.5"]`, copied, "复制分组时账号在该分组内的模型限制要一起复制")
}
