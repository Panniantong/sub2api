//go:build unit

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestSchedulerCachePreservesGroupAllowedModels 钉死调度快照的两份 payload
// （full + metadata）都保留账号在各分组内的模型限制。
//
// 候选过滤读的是 metadata 投影，而 filterSchedulerAccountGroups 是显式字段清单：
// 漏掉 AllowedModels 时，分组内的限制在选号阶段静默失效，被限制的账号会重新接到
// 不允许的模型。
func TestSchedulerCachePreservesGroupAllowedModels(t *testing.T) {
	account := service.Account{
		ID:          9101,
		Name:        "group-allowed-models",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		AccountGroups: []service.AccountGroup{
			{AccountID: 9101, GroupID: 1},
			{AccountID: 9101, GroupID: 2, AllowedModels: []string{"gpt-5.5", "gpt-5.3-*"}},
		},
		GroupIDs: []int64{1, 2},
	}

	meta := buildSchedulerMetadataAccount(account)
	require.Len(t, meta.AccountGroups, 2)
	require.Empty(t, meta.AccountGroups[0].AllowedModels)
	require.Equal(t, []string{"gpt-5.5", "gpt-5.3-*"}, meta.AccountGroups[1].AllowedModels)

	full, metaPayload, err := marshalSchedulerCacheAccount(account)
	require.NoError(t, err)
	for name, payload := range map[string][]byte{"full": full, "metadata": metaPayload} {
		decoded, decodeErr := decodeCachedAccount(payload)
		require.NoError(t, decodeErr, name)
		groupID := int64(2)
		require.Equal(t, []string{"gpt-5.5", "gpt-5.3-*"}, decoded.GroupAllowedModels(groupID), name)
		require.False(t, decoded.IsModelAllowedInGroup(&groupID, "gpt-5.4"), "%s payload 反序列化后分组限制必须仍然生效", name)
	}
}
