package service

import (
	"encoding/hex"
	"strings"
)

// This marker is an internal invalidation handle, never an authentication key.
// Custom API keys disallow ':', so it cannot collide with a created credential.
const legacyAPIKeyInvalidationPrefix = "legacy-sha256:"

func (k *APIKey) AuthCacheInvalidationKey() string {
	if k == nil {
		return ""
	}
	if k.Key != "" {
		return k.Key
	}
	if k.KeyHash != "" {
		return legacyAPIKeyInvalidationPrefix + k.KeyHash
	}
	return ""
}

func (s *APIKeyService) authInvalidationCacheKey(key string) string {
	if digest, ok := strings.CutPrefix(key, legacyAPIKeyInvalidationPrefix); ok && len(digest) == 64 {
		if _, err := hex.DecodeString(digest); err == nil {
			return digest
		}
	}
	return s.authCacheKey(key)
}
