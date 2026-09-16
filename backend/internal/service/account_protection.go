package service

import (
	"context"
	"maps"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	AntiDegradationExtraKey = "anti_degradation"
	ProtectionScopeExtraKey = "protection_scope"
)

// AntiDegradationEnabled preserves the meaning of old rows. Absence of the new
// flag is never interpreted as permission to change an existing account.
func (a *Account) AntiDegradationEnabled() bool {
	if a == nil {
		return false
	}
	if enabled, ok := a.Extra[AntiDegradationExtraKey].(bool); ok {
		return enabled
	}
	return antiDegradeEnabled(a)
}

func (a *Account) ProtectionScope() string {
	if !a.AntiDegradationEnabled() {
		return "disabled"
	}
	if isMode1ProtectionEnabled(a) {
		return "codex_v3"
	}
	if a.Extra[ProtectionScopeExtraKey] == "generic_v1" {
		return "generic_v1"
	}
	return "legacy"
}

// ProtectionMode returns the concrete strategy currently persisted on the
// account. Unlike ProtectionScope (which is a coarse capability bucket), this
// value is suitable for admin UI/audit so operators can tell mode1, mode2 and
// the explicit sub2初代 legacy strategy apart.
func (a *Account) ProtectionMode() string {
	if a == nil || !a.AntiDegradationEnabled() {
		return "disabled"
	}
	if isMode1ProtectionEnabled(a) {
		return string(AntiDegradeMode1)
	}
	if isLegacyProtectionEnabled(a) {
		return string(AntiDegradeModeLegacy)
	}
	if marker, ok := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any); ok {
		if mode, ok := marker["mode"].(string); ok && mode != "" {
			return mode
		}
	}
	return string(AntiDegradeMode2)
}

// PrepareNewAccountProtection is the final, server-owned default for every
// creation path, including sync, import, duplicate and credential shadows.
// Unsupported providers retain their native protocol and TLS settings.
func PrepareNewAccountProtection(a *Account) {
	if a == nil {
		return
	}
	a.Extra = maps.Clone(a.Extra)
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	delete(a.Extra, AntiDegradeMarkerExtraKey)
	delete(a.Extra, AntiDegradationExtraKey)
	delete(a.Extra, ProtectionScopeExtraKey)
	delete(a.Extra, codexFingerprintSeedExtraKey)
	configureAccountProtection(a)
}

func configureAccountProtection(a *Account) {
	a.Extra = maps.Clone(a.Extra)
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	a.Extra[AntiDegradationExtraKey] = true
	cap := a.Concurrency
	if cap <= 0 {
		cap = AntiDegradeConcurrencyCap
	}
	prev := map[string]any{"concurrency": a.Concurrency}
	if (isOpenAIOAuthLike(a) || isAnthropicOAuthLike(a)) && !a.IsShadow() && !a.IsRandomProxy() {
		prev = snapshotAntiDegradeConfig(a)
		fingerprint, tlsProfile, _ := antiDegradeModeSettings(DefaultAntiDegradeMode)
		if isOpenAIOAuthLike(a) {
			a.Extra[codexFingerprintModeExtraKey] = string(fingerprint)
			a.Extra = prepareCodexFingerprintExtraForUpdate(a, a.Extra)
		}
		a.Extra["enable_tls_fingerprint"] = true
		a.Extra["tls_fingerprint_builtin"] = tlsProfile
		delete(a.Extra, "tls_fingerprint_profile_id")
		a.Extra[AntiDegradeMarkerExtraKey] = map[string]any{"enabled": true, "mode": string(DefaultAntiDegradeMode), "max_concurrency": cap, "prev": prev, "applied_at": time.Now().UTC().Format(time.RFC3339)}
		a.Extra[ProtectionScopeExtraKey] = "legacy"
	} else {
		// Generic protection is a persisted concurrency bound only. It must not
		// advertise Codex identity or semantic checks on unsupported protocols.
		a.Extra[ProtectionScopeExtraKey] = "generic_v1"
		a.Extra[AntiDegradeMarkerExtraKey] = map[string]any{"enabled": true, "mode": "generic", "max_concurrency": cap, "prev": prev, "applied_at": time.Now().UTC().Format(time.RFC3339)}
	}
	a.Concurrency = cap
}

// ProtectionManagedWrite is true only for a server-internal context created by
// a dedicated protection operation. No request JSON can supply this value.
func ProtectionManagedWrite(ctx context.Context) bool {
	return ctx.Value(mode1ManagedWriteKey{}) == true
}

func ProtectionManagedKeys(a *Account) []string {
	keys := []string{AntiDegradationExtraKey, ProtectionScopeExtraKey, AntiDegradeMarkerExtraKey}
	// Both the v3 mode-1 policy and the explicitly selected sub2初代 policy
	// own the fingerprint/TLS fields they write.  Preserve those fields when
	// an ordinary account edit submits a stale/partial extra object; otherwise
	// saving the modal could silently turn the legacy strategy off.
	if isMode1ProtectionRequested(a) || (antiDegradeEnabled(a) && antiDegradeStrategyProfile(antiDegradeMode(a)).ApplySupported) {
		keys = append(keys, mode1ManagedExtraKeys[1:]...)
	}
	return keys
}

// PreserveAccountProtection is also invoked under the repository row lock, so
// a stale form or refresh cannot undo a concurrently confirmed protection change.
func PreserveAccountProtection(ctx context.Context, current *Account, incoming map[string]any) map[string]any {
	if ProtectionManagedWrite(ctx) {
		return incoming
	}
	result := maps.Clone(incoming)
	if result == nil {
		result = map[string]any{}
	}
	for _, key := range ProtectionManagedKeys(current) {
		if value, exists := current.Extra[key]; exists {
			result[key] = value
		} else {
			delete(result, key)
		}
	}
	// Full-object imports and stale refreshes may omit the identity seed. Keep
	// the committed seed; unlike policy snapshots it never belongs to a caller.
	if seed, ok := codexFingerprintSeed(current.Extra); ok {
		result[codexFingerprintSeedExtraKey] = seed
	}
	return result
}

func BoundAccountProtectionConcurrency(a *Account) {
	if a == nil || !a.AntiDegradationEnabled() {
		return
	}
	if a.Concurrency <= 0 {
		a.Concurrency = AntiDegradeConcurrencyCap
	}
	// The account field is the administrator's editable ceiling. The marker
	// mirrors it for historical displays; strategy presets never clamp it.
	if marker := mode1Marker(a); marker != nil {
		a.Extra = maps.Clone(a.Extra)
		copy := maps.Clone(marker)
		copy["max_concurrency"] = a.Concurrency
		a.Extra[AntiDegradeMarkerExtraKey] = copy
	}
}

func ValidateAccountProtectionConfiguration(a *Account) error {
	if a != nil {
		if _, err := AccountTrafficPlanFor(a); err != nil {
			return err
		}
	}
	if a != nil {
		if err := validateRequestIntegrityExtra(a.Extra); err != nil {
			return err
		}
	}
	if !isMode1ProtectionRequested(a) {
		return validateRegisteredProtection(a)
	}
	if !isMode1ProtectionEnabled(a) {
		return infraerrors.BadRequest("MODE1_CONFIGURATION_INVALID", "账号保护配置不完整，请通过专用保护入口修复")
	}
	if issues := mode1ConfigurationIssues(a); len(issues) > 0 {
		return infraerrors.BadRequest("MODE1_CONFIGURATION_INVALID", issues[0])
	}
	return nil
}

// SetProtection enables the available policy or explicitly disables protection.
// Caller must independently authenticate an administrator. Confirmation is
// checked here as well as at the HTTP boundary.
func (s *AntiDegradeService) SetProtection(ctx context.Context, id int64, enabled, confirmDisable bool) (*Account, error) {
	return s.transition(ctx, id, func(planner *AntiDegradeService) error {
		_, err := planner.setProtection(ctx, id, enabled, confirmDisable)
		return err
	})
}

func (s *AntiDegradeService) setProtection(ctx context.Context, id int64, enabled, confirmDisable bool) (*Account, error) {
	if !enabled && !confirmDisable {
		return nil, infraerrors.BadRequest("PROTECTION_CONFIRM_REQUIRED", "关闭防降智模式需要管理员明确确认")
	}
	a, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.AntiDegradationEnabled() == enabled {
		return a, nil
	}
	if !enabled {
		return s.revertAntiDegrade(ctx, id)
	}
	configureAccountProtection(a)
	return s.admin.UpdateAccount(context.WithValue(ctx, mode1ManagedWriteKey{}, true), id, &UpdateAccountInput{Extra: a.Extra, Concurrency: &a.Concurrency})
}
