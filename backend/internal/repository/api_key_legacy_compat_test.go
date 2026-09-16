package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLegacyAPIKeyLookupAndInvalidationHandle(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "legacy-key@test.invalid")
	const raw = "sk-legacy-kept-by-client"
	digest := legacyAPIKeyDigest(raw)
	row, err := client.APIKey.Create().SetUserID(user.ID).SetName("Legacy").SetKeyHash(digest).SetKeyPrefix("sk-legac").Save(ctx)
	require.NoError(t, err)
	got, err := repo.GetByKeyForAuth(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, row.ID, got.ID)
	stored, err := repo.GetByID(ctx, row.ID)
	require.NoError(t, err)
	require.Empty(t, stored.Key, "a hash cannot recover the original key")
	require.Equal(t, digest, stored.KeyHash)
	handle, owner, err := repo.GetKeyAndOwnerID(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, user.ID, owner)
	require.Equal(t, stored.AuthCacheInvalidationKey(), handle)
	keys, err := repo.ListKeysByUserID(ctx, user.ID)
	require.NoError(t, err)
	require.Contains(t, keys, handle)
	_, err = repo.GetByKeyForAuth(ctx, handle)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound, "invalidation handles must never authenticate")
	fresh := &service.APIKey{UserID: user.ID, Name: "Fresh", Key: "sk-fresh-plain-key", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, fresh))
	freshRead, err := repo.GetByID(ctx, fresh.ID)
	require.NoError(t, err)
	require.Equal(t, fresh.Key, freshRead.Key)
	require.Empty(t, freshRead.KeyHash, "new keys must keep the official storage path")
}
