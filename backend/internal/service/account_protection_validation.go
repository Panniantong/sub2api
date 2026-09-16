package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

func antiDegradeEligibilityIssue(a *Account, mode AntiDegradeMode) string {
	if a == nil {
		return "account not found"
	}
	p := antiDegradeStrategyProfile(mode)
	if p.ID == "" {
		return "未知的账号保护策略"
	}
	if mode == AntiDegradeModeNative {
		return ""
	}
	if a.IsShadow() {
		return "影子账号仅支持通用并发保护，请在母账号配置身份策略"
	}
	if a.IsRandomProxy() {
		return "请先选择固定代理或直连，再应用身份保护策略"
	}
	if p.RequiresOpenAI && !isOpenAIOAuthLike(a) {
		return "该策略仅支持 OpenAI OAuth / Setup Token 账号"
	}
	if mode == AntiDegradeModeLegacy && !isOpenAIOAuthLike(a) && !isAnthropicOAuthLike(a) {
		return "初代兼容仅支持 OpenAI / Anthropic OAuth 或 Setup Token；此账号请使用通用并发保护"
	}
	return ""
}

// Shared with SQL preservation so new registry strategies cannot accidentally
// lose protection through the key-level or bulk write paths.
func RegisteredProtectionModes() []string {
	var modes []string
	for _, p := range ListAntiDegradeStrategyProfiles() {
		if p.ApplySupported {
			modes = append(modes, string(p.ID))
		}
	}
	return modes
}

func validateRegisteredProtection(a *Account) error {
	if !antiDegradeEnabled(a) {
		return nil
	}
	m := mode1Marker(a)
	raw, _ := m["mode"].(string)
	// Retain legacy unversioned rows whose original policy was not explicit.
	if raw == "" || raw == "generic" {
		return nil
	}
	mode := AntiDegradeMode(raw)
	invalid := func(message string) error { return infraerrors.BadRequest("PROTECTION_CONFIGURATION_INVALID", message) }
	if reason := antiDegradeEligibilityIssue(a, mode); reason != "" {
		return invalid(reason)
	}
	fp, tls, cap := antiDegradeModeSettings(mode)
	if cap < 1 {
		return invalid("无效的保护策略")
	}
	if isOpenAIOAuthLike(a) {
		if a.GetCodexFingerprintMode() != fp {
			return invalid("保护身份配置与已选策略不一致")
		}
		if _, ok := codexFingerprintSeed(a.Extra); !ok {
			return invalid("账号持久身份种子缺失或无效")
		}
	}
	if tls == "standard" {
		if a.IsTLSFingerprintEnabled() || a.Extra["tls_fingerprint_builtin"] != nil || a.Extra["tls_fingerprint_profile_id"] != nil {
			return invalid("标准传输策略不能启用实验 TLS 模板")
		}
	} else if !a.IsTLSFingerprintEnabled() || a.Extra["tls_fingerprint_builtin"] != tls {
		return invalid("TLS 配置与已选策略不一致")
	}
	if limit := mode1Int(m["max_concurrency"]); limit < 1 {
		return invalid("保护并发上限无效")
	}
	return nil
}
