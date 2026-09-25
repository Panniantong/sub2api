package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 账号编辑页从 account_groups[].allowed_models 读取分组内的模型限制；
// 没有限制的绑定不输出该字段，保持原有响应结构。
func TestAccountFromServiceExposesGroupAllowedModels(t *testing.T) {
	out := AccountFromService(&service.Account{
		ID: 1,
		AccountGroups: []service.AccountGroup{
			{AccountID: 1, GroupID: 3},
			{AccountID: 1, GroupID: 5, AllowedModels: []string{"gpt-5.5"}},
		},
	})
	require.NotNil(t, out)
	require.Len(t, out.AccountGroups, 2)
	require.Equal(t, []string{"gpt-5.5"}, out.AccountGroups[1].AllowedModels)

	body, err := json.Marshal(out.AccountGroups)
	require.NoError(t, err)
	var groups []map[string]any
	require.NoError(t, json.Unmarshal(body, &groups))
	require.NotContains(t, groups[0], "allowed_models")
	require.Equal(t, []any{"gpt-5.5"}, groups[1]["allowed_models"])
}
