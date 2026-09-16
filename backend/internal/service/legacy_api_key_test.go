package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLegacyAPIKeyInvalidationUsesStoredDigestOnlyForInvalidation(t *testing.T) {
	cache := &authInvalidationCacheStub{}
	svc := &APIKeyService{cache: cache, authCfg: apiKeyAuthCacheConfig{l2TTL: time.Minute}}
	digest := svc.authCacheKey("sk-legacy-key")
	key := &APIKey{KeyHash: digest}
	handle := key.AuthCacheInvalidationKey()
	svc.InvalidateAuthCacheByKey(context.Background(), handle)
	require.Contains(t, cache.deleted, digest)
	require.NotEqual(t, digest, svc.authCacheKey(handle), "authentication must hash the actual credential")
	require.Error(t, svc.ValidateCustomKey(handle))
}
