package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthSnapshotGroupStreamOnlyRoundtrip(t *testing.T) {
	groupID := int64(51)
	apiKey := &APIKey{
		ID: 83, UserID: 41, GroupID: &groupID, Key: "sk-stream-only-roundtrip", Status: StatusActive,
		User: &User{ID: 41, Status: StatusActive, UserGroupDeniedModels: []string{"blocked-model"}},
		Group: &Group{
			ID: groupID, Name: "stream-only", Platform: PlatformAnthropic, Status: StatusActive,
			Hydrated: true, StreamOnly: true,
		},
	}
	svc := &APIKeyService{}

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))

	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.True(t, materialized.Group.StreamOnly)
	require.Equal(t, []string{"blocked-model"}, materialized.DeniedModelsInGroup())

	// 升级前缓存的快照没有 stream_only：版本号不同必须作废，不能当成「未开启」放行非流式请求。
	cached.Snapshot.Version = 25
	_, used, err = svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.False(t, used)
}
