package service

import (
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"strings"
)

var ErrProtectedProxyModeChange = infraerrors.BadRequest("PROTECTED_PROXY_MODE_CHANGE", "当前身份保护策略要求固定代理或直连；请先关闭或切换保护策略，再启用随机代理。原代理未更改。")

// ProtectedProxyModeConflict is also checked under the repository row lock.
func ProtectedProxyModeConflict(a *Account, extra map[string]any) bool {
	mode, _ := extra[ProxyModeExtraKey].(string)
	if a == nil || !a.AntiDegradationEnabled() || strings.ToLower(strings.TrimSpace(mode)) != ProxyModeRandom {
		return false
	}
	for _, key := range ProtectionManagedKeys(a) {
		if key == ProxyModeExtraKey {
			return true
		}
	}
	return false
}
