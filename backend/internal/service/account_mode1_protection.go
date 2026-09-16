package service

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const mode1PolicyVersion = 3

func mode1Marker(a *Account) map[string]any {
	if a == nil {
		return nil
	}
	m, _ := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any)
	return m
}

func mode1Int(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		if n == float64(int(n)) {
			return int(n)
		}
	}
	return 0
}

func isMode1ProtectionEnabled(a *Account) bool {
	m := mode1Marker(a)
	version := mode1Int(m["policy_version"])
	return m["enabled"] == true && m["mode"] == string(AntiDegradeMode1) && (version == 2 || version == mode1PolicyVersion)
}

// isLegacyProtectionEnabled identifies the explicitly selected sub2初代
// strategy. Legacy is intentionally separate from mode1 v3: it keeps the
// original session fingerprint + nodejs24 TLS behavior and must not be
// subjected to mode1-v3 integrity checks.
func isLegacyProtectionEnabled(a *Account) bool {
	m := mode1Marker(a)
	return m != nil && m["enabled"] == true && m["mode"] == string(AntiDegradeModeLegacy)
}

func isMode1ProtectionRequested(a *Account) bool {
	m := mode1Marker(a)
	_, versionPresent := m["policy_version"]
	// A versioned marker with a missing/unknown mode must not silently bypass
	// integrity checks. Mode 2 and the explicitly unversioned `legacy` policy
	// own their separate transports.
	return m["enabled"] != false && m["mode"] != string(AntiDegradeMode2) && versionPresent
}

func hasMode1ManagedUpdates(updates map[string]any) bool {
	for _, key := range mode1ManagedExtraKeys {
		if _, exists := updates[key]; exists {
			return true
		}
	}
	return false
}

func (a *Account) IsMode1ProtectionEnabled() bool { return isMode1ProtectionEnabled(a) }

// Mode1EffectiveConcurrency uses the editable account ceiling for every strategy.
func (a *Account) Mode1EffectiveConcurrency() int {
	if a == nil {
		return 0
	}
	if !a.AntiDegradationEnabled() || a.Concurrency > 0 {
		return a.Concurrency
	}
	return AntiDegradeConcurrencyCap
}

func mode1ConfigurationIssues(a *Account) []string {
	issues := []string{}
	if !isOpenAIOAuthLike(a) || a.IsShadow() {
		return []string{"模式一仅支持 OpenAI OAuth / Setup Token 账号"}
	}
	if _, ok := codexFingerprintSeed(a.Extra); !ok {
		issues = append(issues, "账号持久身份种子缺失或无效")
	}
	if a.GetCodexFingerprintMode() != codexFingerprintDevice {
		issues = append(issues, "模式一必须保留独立会话，仅固定设备身份")
	}
	if a.Extra["proxy_mode"] == "random" {
		issues = append(issues, "随机代理与模式一固定出口配置冲突")
	}
	if cap := mode1Int(mode1Marker(a)["max_concurrency"]); cap < 1 {
		issues = append(issues, "保护并发上限无效，请还原后重新配置")
	}
	return issues
}

// v3 uses the proven standard transport. v2 remains readable and retains its
// explicitly configured template while enabled globally; TLS is not a claim
// about model quality. Invalid identity/policy state still fails explicitly.
func resolveMode1TLSProfile(a *Account) (*tlsfingerprint.Profile, error) {
	if !isMode1ProtectionRequested(a) {
		if err := validateRegisteredProtection(a); err != nil {
			return nil, err
		}
	}
	if isLegacyProtectionEnabled(a) && (isOpenAIOAuthLike(a) || isAnthropicOAuthLike(a)) {
		if p := tlsfingerprint.BuiltinProfile("nodejs24"); p != nil {
			return p, nil
		}
		return nil, fmt.Errorf("legacy TLS profile unavailable")
	}
	if !isMode1ProtectionRequested(a) {
		// Diagnostic profiles can opt into a built-in TLS profile without
		// claiming mode1-v3 integrity semantics.
		if a != nil && a.AntiDegradationEnabled() {
			if raw, ok := a.Extra["tls_fingerprint_builtin"].(string); ok && raw != "" {
				if p := tlsfingerprint.BuiltinProfile(raw); p != nil {
					return p, nil
				}
				return nil, fmt.Errorf("TLS profile unavailable: %s", raw)
			}
		}
		return nil, nil
	}
	if !isMode1ProtectionEnabled(a) {
		return nil, infraerrors.BadRequest("MODE1_CONFIGURATION_INVALID", "模式一配置标记或版本无效，请检查配置")
	}
	if issues := mode1ConfigurationIssues(a); len(issues) > 0 {
		return nil, infraerrors.BadRequest("MODE1_CONFIGURATION_INVALID", strings.Join(issues, "; "))
	}
	if mode1Int(mode1Marker(a)["policy_version"]) == mode1PolicyVersion || !a.IsTLSFingerprintEnabled() {
		return nil, nil
	}
	p := tlsfingerprint.BuiltinProfile("nodejs24")
	if p == nil {
		return nil, fmt.Errorf("mode1 TLS profile unavailable")
	}
	return p, nil
}

func previewMode1(a *Account) AntiDegradePreview {
	out := AntiDegradePreview{Changes: []AntiDegradeChange{}, Issues: []string{}, TLSProfile: "standard"}
	if a == nil {
		out.Reason = "account not found"
		return out
	}
	out.AccountID = a.ID
	out.Enabled = antiDegradeEnabled(a)
	if out.Enabled {
		out.ActiveMode = string(antiDegradeMode(a))
		out.PolicyVersion = mode1Int(mode1Marker(a)["policy_version"])
		if out.PolicyVersion == 2 && a.IsTLSFingerprintEnabled() {
			out.TLSProfile = "nodejs24"
		}
	}
	_, out.IdentityReady = codexFingerprintSeed(a.Extra)
	if !isOpenAIOAuthLike(a) || a.IsShadow() {
		out.Reason = "模式一仅支持独立 OpenAI OAuth / Setup Token 账号"
		return out
	}
	if a.Extra["proxy_mode"] == "random" {
		out.Reason = "请先为账号选择固定代理或直连，再启用模式一"
		return out
	}
	if out.Enabled && !isMode1ProtectionEnabled(a) {
		if antiDegradeMode(a) == AntiDegradeMode1 {
			// A malformed/legacy marker without an explicit alternate mode is
			// still treated as enabled but not eligible for an implicit rewrite.
			out.Reason = "当前模式一配置无效，请先还原后重新配置"
			return out
		}
		// A different strategy is active. Allow the explicit mode-1 button to
		// perform a safe switch (the service restores the current snapshot before
		// applying mode 1) instead of reporting an unexplained hard stop.
		out.Eligible = true
		out.Reason = "可切换到兼容保护 v3；应用前会先还原当前策略快照"
		out.Changes = append(out.Changes, AntiDegradeChange{Key: "strategy", From: string(antiDegradeMode(a)), To: string(AntiDegradeMode1), Note: "安全切换策略"})
		return out
	}
	if isMode1ProtectionEnabled(a) && out.PolicyVersion == mode1PolicyVersion {
		out.Issues = mode1ConfigurationIssues(a)
		if len(out.Issues) == 0 {
			out.Reason = "已启用兼容保护 v3：稳定设备身份、会话隔离、并发上限和请求完整性检查；不保证上游模型质量"
		} else {
			out.Reason = "配置异常，请还原后重新配置"
		}
		return out
	}
	if isMode1ProtectionEnabled(a) && antiDegradePrev(a) == nil {
		out.Reason = "v2 原始配置快照缺失，无法安全升级；请先修复账号快照"
		return out
	}
	out.Eligible = true
	if out.PolicyVersion == 2 {
		out.Reason = "可升级至兼容保护 v3：改用初代标准传输，保留身份种子与原始还原快照"
		out.Changes = append(out.Changes, AntiDegradeChange{Key: "policy_version", From: 2, To: mode1PolicyVersion, Note: "显式升级；还原恢复首次启用前的配置"})
	}
	for _, ch := range []AntiDegradeChange{
		{Key: "extra.codex_fingerprint_mode", From: string(a.GetCodexFingerprintMode()), To: "device", Note: "稳定设备身份，保留不同对话的 session/thread"},
		{Key: "transport", From: a.Extra["tls_fingerprint_builtin"], To: "standard", Note: "沿用初代标准传输，不强制实验性 TLS ClientHello"},
		{Key: "concurrency", From: a.Concurrency, To: mode1InitialLimit(a), Note: "运行时持续执行上限，已有更低上限保留"},
	} {
		out.Changes = append(out.Changes, ch)
	}
	if !out.IdentityReady {
		out.Changes = append(out.Changes, AntiDegradeChange{Key: "identity", To: "生成账号独立随机 UUID 种子并持久保存"})
	}
	return out
}

func mode1InitialLimit(a *Account) int {
	if isMode1ProtectionEnabled(a) {
		return a.Mode1EffectiveConcurrency()
	}
	if a.Concurrency > 0 {
		return a.Concurrency
	}
	return AntiDegradeConcurrencyCap
}

var mode1ManagedExtraKeys = []string{AntiDegradeMarkerExtraKey, "codex_fingerprint_mode", "enable_tls_fingerprint", "tls_fingerprint_builtin", "tls_fingerprint_profile_id", "proxy_mode"}

type mode1ManagedWriteKey struct{}

func (s *AntiDegradeService) applyMode1(ctx context.Context, a *Account) (*Account, error) {
	p := previewMode1(a)
	if isMode1ProtectionEnabled(a) && p.PolicyVersion == mode1PolicyVersion && len(p.Issues) == 0 {
		return a, nil
	}
	if !p.Eligible {
		return nil, infraerrors.BadRequest("MODE1_NOT_ELIGIBLE", p.Reason)
	}
	extra := maps.Clone(a.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	prev := map[string]any{"concurrency": a.Concurrency}
	for _, key := range mode1ManagedExtraKeys {
		if key != AntiDegradeMarkerExtraKey {
			prev[key] = extra[key]
		}
	}
	if isMode1ProtectionEnabled(a) {
		prev = maps.Clone(antiDegradePrev(a))
	}
	extra["codex_fingerprint_mode"] = "device"
	extra["enable_tls_fingerprint"] = false
	delete(extra, "tls_fingerprint_builtin")
	delete(extra, "tls_fingerprint_profile_id")
	cap := mode1InitialLimit(a)
	extra[AntiDegradeMarkerExtraKey] = map[string]any{"enabled": true, "mode": "mode1", "policy_version": mode1PolicyVersion, "max_concurrency": cap, "applied_at": time.Now().UTC().Format(time.RFC3339), "prev": prev}
	extra[AntiDegradationExtraKey] = true
	extra[ProtectionScopeExtraKey] = "codex_v3"
	return s.admin.UpdateAccount(context.WithValue(ctx, mode1ManagedWriteKey{}, true), a.ID, &UpdateAccountInput{Extra: extra, Concurrency: &cap})
}

// Preserve server-managed protection fields during ordinary modal saves. The
// explicit apply/revert endpoints are the sole owners of policy transitions.
func preserveMode1ManagedExtra(ctx context.Context, a *Account, incoming map[string]any) map[string]any {
	return PreserveAccountProtection(ctx, a, incoming)
}
