package service

// Anti-degradation strategy profiles are deliberately data-driven.  The
// profile registry describes the runtime knobs that are safe to compare in a
// controlled account test; it does not grant additional upstream permissions
// or alter credentials.

type AntiDegradeStrategyProfile struct {
	ID             AntiDegradeMode `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Category       string          `json:"category"`
	IdentityMode   string          `json:"identity_mode"`
	TLSProfile     string          `json:"tls_profile"`
	MaxConcurrency int             `json:"max_concurrency"`
	Risk           string          `json:"risk"`
	ApplySupported bool            `json:"apply_supported"`
	RequiresOpenAI bool            `json:"requires_openai_oauth"`
	DiagnosticOnly bool            `json:"diagnostic_only"`
}

var antiDegradeStrategyProfiles = []AntiDegradeStrategyProfile{
	{ID: AntiDegradeModeNative, Name: "原生基线", Description: "不改写身份与传输策略，仅使用账号当前配置。", Category: "常用", IdentityMode: "off", TLSProfile: "account", MaxConcurrency: 0, Risk: "低", ApplySupported: false, DiagnosticOnly: true},
	{ID: AntiDegradeModeMinimal, Name: "最小兼容", Description: "仅固定账号设备身份，保留独立会话和线程。", Category: "常用", IdentityMode: "device", TLSProfile: "standard", MaxConcurrency: 8, Risk: "低", ApplySupported: true, RequiresOpenAI: true},
	{ID: AntiDegradeModeSession, Name: "会话兼容", Description: "固定设备与账号会话，按客户端会话派生线程。", Category: "诊断", IdentityMode: "session", TLSProfile: "standard", MaxConcurrency: 8, Risk: "中", ApplySupported: true, RequiresOpenAI: true, DiagnosticOnly: true},
	{ID: AntiDegradeModeLegacy, Name: "初代兼容", Description: "默认策略，复用 sub2 初代的 session 身份和 Node.js 24 传输。", Category: "常用", IdentityMode: "session", TLSProfile: "nodejs24", MaxConcurrency: 16, Risk: "中", ApplySupported: true},
	{ID: AntiDegradeMode1, Name: "兼容架构 v3", Description: "稳定设备身份、独立会话、标准传输和并发上限。", Category: "常用", IdentityMode: "device", TLSProfile: "standard", MaxConcurrency: 16, Risk: "中", ApplySupported: true, RequiresOpenAI: true},
	{ID: AntiDegradeMode2, Name: "完整收敛", Description: "设备、会话和线程全部收敛，仅用于强对照实验。", Category: "诊断", IdentityMode: "full", TLSProfile: "nodejs22", MaxConcurrency: 8, Risk: "高", ApplySupported: true, RequiresOpenAI: true, DiagnosticOnly: true},
	{ID: AntiDegradeModeTLSNode24, Name: "Node.js 24 对照", Description: "保持设备身份，单独对照 Node.js 24 TLS。", Category: "诊断", IdentityMode: "device", TLSProfile: "nodejs24", MaxConcurrency: 8, Risk: "中", ApplySupported: true, RequiresOpenAI: true, DiagnosticOnly: true},
	{ID: AntiDegradeModeLowConcurrency, Name: "低并发稳定", Description: "会话兼容配合低并发，用于排查限流和连接复用。", Category: "诊断", IdentityMode: "session", TLSProfile: "standard", MaxConcurrency: 4, Risk: "低", ApplySupported: true, RequiresOpenAI: true, DiagnosticOnly: true},
}

func ListAntiDegradeStrategyProfiles() []AntiDegradeStrategyProfile {
	profiles := make([]AntiDegradeStrategyProfile, len(antiDegradeStrategyProfiles))
	copy(profiles, antiDegradeStrategyProfiles)
	return profiles
}

func antiDegradeStrategyProfile(mode AntiDegradeMode) AntiDegradeStrategyProfile {
	for _, profile := range antiDegradeStrategyProfiles {
		if profile.ID == mode {
			return profile
		}
	}
	return AntiDegradeStrategyProfile{}
}
