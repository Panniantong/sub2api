package service

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// Safe diagnostic metadata: never include credentials, identity seeds or URLs
// containing proxy authentication. This is a computed plan, not a handshake.
type ProtectionRuntimeState struct {
	IntegrityMode    string    `json:"integrity_mode"`
	RequestedModel   string    `json:"requested_model,omitempty"`
	Strategy         string    `json:"strategy"`
	PolicyVersion    int       `json:"policy_version"`
	IdentityMode     string    `json:"identity_mode"`
	ConfiguredTLS    string    `json:"configured_tls"`
	EffectiveTLS     string    `json:"effective_tls"`
	TLSReason        string    `json:"tls_reason,omitempty"`
	Observed         bool      `json:"observed"`
	Concurrency      int       `json:"concurrency"`
	ProxyID          *int64    `json:"proxy_id,omitempty"`
	ProxyMode        string    `json:"proxy_mode"`
	AccountRevision  time.Time `json:"account_revision"`
	TLSProfileDigest string    `json:"tls_profile_digest,omitempty"`
	PromptDigest     string    `json:"prompt_digest,omitempty"`
	Model            string    `json:"model,omitempty"`
}

func ResolveProtectionRuntime(a *Account, cfg *config.Config, plugins *PluginManager) *ProtectionRuntimeState {
	if a == nil {
		return nil
	}
	out := &ProtectionRuntimeState{Strategy: a.ProtectionMode(), PolicyVersion: mode1Int(mode1Marker(a)["policy_version"]), IdentityMode: string(a.GetCodexFingerprintMode()), ConfiguredTLS: "standard", EffectiveTLS: "standard", Concurrency: a.Mode1EffectiveConcurrency(), AccountRevision: a.UpdatedAt, ProxyMode: "fixed"}
	out.IntegrityMode = a.RequestIntegrityMode()
	if a.ProxyID != nil {
		id := *a.ProxyID
		out.ProxyID = &id
	}
	if a.IsRandomProxy() {
		out.ProxyMode = "random"
	}
	if a.IsTLSFingerprintEnabled() {
		out.ConfiguredTLS, _ = a.Extra["tls_fingerprint_builtin"].(string)
		if out.ConfiguredTLS == "" {
			out.ConfiguredTLS = "account_template"
		}
	}
	profile, err := resolveMode1TLSProfile(a)
	if err != nil {
		out.EffectiveTLS = "invalid"
		out.TLSReason = err.Error()
		return out
	}
	if profile != nil {
		out.TLSProfileDigest = profile.CacheKey()
		out.ConfiguredTLS = profile.Name
	}
	// Preserve stable registry identifiers rather than the profile display name.
	if p := antiDegradeStrategyProfile(antiDegradeMode(a)); a.AntiDegradationEnabled() && p.ID != "" {
		out.ConfiguredTLS = p.TLSProfile
	}
	if isMode1ProtectionEnabled(a) && mode1Int(mode1Marker(a)["policy_version"]) == 2 && a.IsTLSFingerprintEnabled() {
		out.ConfiguredTLS = "nodejs24"
	}
	out.EffectiveTLS = out.ConfiguredTLS
	if cfg == nil {
		out.EffectiveTLS = "unverified"
		out.TLSReason = "未提供运行配置，尚未确认有效传输"
	} else if !cfg.Gateway.TLSFingerprint.Enabled && out.ConfiguredTLS != "standard" {
		out.EffectiveTLS = "standard"
		out.TLSReason = "全局 TLS 指纹开关已关闭"
	}
	if plugins != nil && plugins.ShouldRouteOpenAIOAuth(a) {
		out.EffectiveTLS = "plugin"
		out.TLSReason = "HTTP 传输由插件接管，模板握手未观测"
	}
	return out
}

func protectionPromptDigest(prompt string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(prompt)))
}
