package service

import (
	"errors"
	"strings"
	"time"
)

var errIntelligentCredentialRefreshRequired = errors.New("账号认证缺失、过期或需要更新；请先在账号管理中单独刷新认证，再运行测试。智能测试不会自动刷新 access_token 或注册认证任务")

// Capability observations use the authoritative account snapshot. Reading a
// shared provider cache or calling RefreshIfNeeded may otherwise auto-detect a
// project, change route metadata, quarantine the account or rotate a refresh
// token through a repository that is outside the runner's read-only wrapper.
// Block before the exchange: discarding an already rotated token is unsafe.
func intelligentExistingAccessToken(account *Account) (string, error) {
	if account == nil {
		return "", errIntelligentCredentialRefreshRequired
	}
	token := strings.TrimSpace(account.GetCredential("access_token"))
	expires := account.GetCredentialAsTime("expires_at")
	if token == "" || expires == nil || !time.Now().Before(*expires) {
		return "", errIntelligentCredentialRefreshRequired
	}
	return token, nil
}
