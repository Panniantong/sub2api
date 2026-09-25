package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestAnnouncementTargeting_Matches_EmptyMatchesAll(t *testing.T) {
	var targeting AnnouncementTargeting
	require.True(t, targeting.Matches(1, 0, nil))
	require.True(t, targeting.Matches(1, 123.45, map[int64]struct{}{1: {}}))
}

func TestAnnouncementTargeting_NormalizeAndValidate_RejectsEmptyGroup(t *testing.T) {
	targeting := AnnouncementTargeting{
		AnyOf: []AnnouncementConditionGroup{
			{AllOf: nil},
		},
	}
	_, err := targeting.NormalizeAndValidate()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAnnouncementInvalidTarget)
}

func TestAnnouncementTargeting_NormalizeAndValidate_RejectsInvalidCondition(t *testing.T) {
	targeting := AnnouncementTargeting{
		AnyOf: []AnnouncementConditionGroup{
			{
				AllOf: []AnnouncementCondition{
					{Type: "balance", Operator: "between", Value: 10},
				},
			},
		},
	}
	_, err := targeting.NormalizeAndValidate()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAnnouncementInvalidTarget)
}

func TestAnnouncementTargeting_Matches_AndOrSemantics(t *testing.T) {
	targeting := AnnouncementTargeting{
		AnyOf: []AnnouncementConditionGroup{
			{
				AllOf: []AnnouncementCondition{
					{Type: AnnouncementConditionTypeBalance, Operator: AnnouncementOperatorGTE, Value: 100},
					{Type: AnnouncementConditionTypeSubscription, Operator: AnnouncementOperatorIn, GroupIDs: []int64{10}},
				},
			},
			{
				AllOf: []AnnouncementCondition{
					{Type: AnnouncementConditionTypeBalance, Operator: AnnouncementOperatorLT, Value: 5},
				},
			},
		},
	}

	// 命中第 2 组（balance < 5）
	require.True(t, targeting.Matches(1, 4.99, nil))
	require.False(t, targeting.Matches(1, 5, nil))

	// 命中第 1 组（balance >= 100 AND 订阅 in [10]）
	require.False(t, targeting.Matches(1, 100, map[int64]struct{}{}))
	require.False(t, targeting.Matches(1, 99.9, map[int64]struct{}{10: {}}))
	require.True(t, targeting.Matches(1, 100, map[int64]struct{}{10: {}}))
}

func TestAnnouncementTargeting_Matches_UserCondition(t *testing.T) {
	onlyUsers := AnnouncementTargeting{
		AnyOf: []AnnouncementConditionGroup{
			{AllOf: []AnnouncementCondition{
				{Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorIn, UserIDs: []int64{7, 9}},
			}},
		},
	}
	require.True(t, onlyUsers.Matches(7, 0, nil))
	require.True(t, onlyUsers.Matches(9, 1000, map[int64]struct{}{10: {}}))
	require.False(t, onlyUsers.Matches(8, 1000, map[int64]struct{}{10: {}}), "未指定的用户看不到")
	require.False(t, onlyUsers.Matches(0, 0, nil), "没有用户上下文时不命中")

	// 与其他条件组合：组内 AND（指定用户且余额 < 5），组间 OR（或者订阅了套餐 10）
	combined := AnnouncementTargeting{
		AnyOf: []AnnouncementConditionGroup{
			{AllOf: []AnnouncementCondition{
				{Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorIn, UserIDs: []int64{7}},
				{Type: AnnouncementConditionTypeBalance, Operator: AnnouncementOperatorLT, Value: 5},
			}},
			{AllOf: []AnnouncementCondition{
				{Type: AnnouncementConditionTypeSubscription, Operator: AnnouncementOperatorIn, GroupIDs: []int64{10}},
			}},
		},
	}
	require.True(t, combined.Matches(7, 1, nil))
	require.False(t, combined.Matches(7, 5, nil))
	require.False(t, combined.Matches(8, 1, nil))
	require.True(t, combined.Matches(8, 1, map[int64]struct{}{10: {}}))

	wrongOperator := AnnouncementCondition{Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorEQ, UserIDs: []int64{7}}
	require.False(t, wrongOperator.Matches(7, 0, nil))
}

func TestAnnouncementTargeting_NormalizeAndValidate_UserCondition(t *testing.T) {
	userTargeting := func(cond AnnouncementCondition) AnnouncementTargeting {
		return AnnouncementTargeting{AnyOf: []AnnouncementConditionGroup{{AllOf: []AnnouncementCondition{cond}}}}
	}

	normalized, err := userTargeting(AnnouncementCondition{
		Type: " user ", Operator: " in ", UserIDs: []int64{9, 7, 9},
	}).NormalizeAndValidate()
	require.NoError(t, err)
	require.Equal(t, []AnnouncementCondition{
		{Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorIn, UserIDs: []int64{9, 7}},
	}, normalized.AnyOf[0].AllOf, "去重并保持顺序")

	tooMany := make([]int64, domain.MaxAnnouncementConditionUserIDs+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	for name, cond := range map[string]AnnouncementCondition{
		"empty users":    {Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorIn},
		"non-positive":   {Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorIn, UserIDs: []int64{7, 0}},
		"wrong operator": {Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorEQ, UserIDs: []int64{7}},
		"too many users": {Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorIn, UserIDs: tooMany},
	} {
		_, err := userTargeting(cond).NormalizeAndValidate()
		require.ErrorIs(t, err, ErrAnnouncementInvalidTarget, name)
	}

	// 其他条件上的 user_ids 没有意义，归一化时丢掉
	normalized, err = userTargeting(AnnouncementCondition{
		Type: AnnouncementConditionTypeBalance, Operator: AnnouncementOperatorGTE, Value: 1, UserIDs: []int64{7},
	}).NormalizeAndValidate()
	require.NoError(t, err)
	require.Nil(t, normalized.AnyOf[0].AllOf[0].UserIDs)
}

func TestAnnouncementTargeting_ExplicitUserIDs(t *testing.T) {
	userCond := func(ids ...int64) AnnouncementCondition {
		return AnnouncementCondition{Type: AnnouncementConditionTypeUser, Operator: AnnouncementOperatorIn, UserIDs: ids}
	}
	balanceCond := AnnouncementCondition{Type: AnnouncementConditionTypeBalance, Operator: AnnouncementOperatorLT, Value: 5}

	ids, ok := AnnouncementTargeting{}.ExplicitUserIDs()
	require.False(t, ok, "空规则是全部用户")
	require.Nil(t, ids)

	ids, ok = AnnouncementTargeting{AnyOf: []AnnouncementConditionGroup{
		{AllOf: []AnnouncementCondition{userCond(7, 9), balanceCond}},
		{AllOf: []AnnouncementCondition{userCond(9, 11)}},
	}}.ExplicitUserIDs()
	require.True(t, ok)
	require.Equal(t, []int64{7, 9, 11}, ids)

	_, ok = AnnouncementTargeting{AnyOf: []AnnouncementConditionGroup{
		{AllOf: []AnnouncementCondition{userCond(7)}},
		{AllOf: []AnnouncementCondition{balanceCond}},
	}}.ExplicitUserIDs()
	require.False(t, ok, "有一组不限定用户时，受众不局限于指定用户")
}
