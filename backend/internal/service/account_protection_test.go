package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAccountProtectionNewDefaultsCoverProvidersAndImports(t *testing.T) {
	for _, tt := range []struct {
		name, platform, kind, scope string
		random, shadow              bool
	}{
		{"oauth", PlatformOpenAI, AccountTypeOAuth, "legacy", false, false},
		{"setup", PlatformOpenAI, AccountTypeSetupToken, "legacy", false, false},
		{"api_key", PlatformOpenAI, AccountTypeAPIKey, "generic_v1", false, false},
		{"anthropic", PlatformAnthropic, AccountTypeOAuth, "legacy", false, false},
		{"anthropic_setup", PlatformAnthropic, AccountTypeSetupToken, "legacy", false, false},
		{"gemini", PlatformGemini, AccountTypeAPIKey, "generic_v1", false, false},
		{"random_proxy", PlatformOpenAI, AccountTypeOAuth, "generic_v1", true, false},
		{"shadow", PlatformOpenAI, AccountTypeOAuth, "generic_v1", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			extra := map[string]any{AntiDegradationExtraKey: false, "codex_fingerprint_seed": "22222222-2222-4222-8222-222222222222", "untouched": "value"}
			if tt.random {
				extra["proxy_mode"] = "random"
			}
			a := &Account{Platform: tt.platform, Type: tt.kind, Concurrency: 90, Extra: extra}
			if tt.shadow {
				id := int64(1)
				a.ParentAccountID = &id
				a.QuotaDimension = "spark"
			}
			PrepareNewAccountProtection(a)
			require.True(t, a.AntiDegradationEnabled())
			require.Equal(t, tt.scope, a.ProtectionScope())
			require.Equal(t, 90, a.Concurrency)
			require.Equal(t, false, extra[AntiDegradationExtraKey], "import input must not be mutated")
			require.Equal(t, "value", a.Extra["untouched"])
			require.NoError(t, ValidateAccountProtectionConfiguration(a))
			if tt.scope == "legacy" {
				require.Equal(t, "legacy", a.ProtectionMode())
				require.NotContains(t, mode1Marker(a), "policy_version")
				require.True(t, a.IsTLSFingerprintEnabled())
				require.Equal(t, "nodejs24", a.Extra["tls_fingerprint_builtin"])
				if tt.platform == PlatformOpenAI {
					require.Equal(t, codexFingerprintSession, a.GetCodexFingerprintMode())
					require.NotEqual(t, extra["codex_fingerprint_seed"], requireValidCodexFingerprintSeed(t, a.Extra))
				} else {
					require.NotContains(t, a.Extra, "codex_fingerprint_mode")
				}
			} else {
				require.False(t, a.IsMode1ProtectionEnabled())
				require.NotContains(t, a.Extra, "tls_fingerprint_builtin")
			}
		})
	}
}

func TestAccountProtectionOldAccountsAreNotSilentlyChanged(t *testing.T) {
	for _, tt := range []struct {
		extra map[string]any
		want  bool
	}{
		{nil, false},
		{map[string]any{AntiDegradeMarkerExtraKey: map[string]any{"enabled": false}}, false},
		{map[string]any{AntiDegradeMarkerExtraKey: map[string]any{"enabled": true}}, true},
		{map[string]any{AntiDegradationExtraKey: false, AntiDegradeMarkerExtraKey: map[string]any{"enabled": true}}, false},
	} {
		a := &Account{Extra: tt.extra}
		require.Equal(t, tt.want, a.AntiDegradationEnabled())
		require.Equal(t, tt.extra, a.Extra)
	}
}

func TestAccountProtectionOrdinaryUpdateCannotDisableOrErasePolicy(t *testing.T) {
	ctx := context.Background()
	for _, extra := range []map[string]any{{}, {AntiDegradationExtraKey: false}, {AntiDegradeMarkerExtraKey: nil}, {"codex_fingerprint_mode": "off", "proxy_mode": "random"}} {
		a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 4}
		PrepareNewAccountProtection(a)
		repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: a}}
		svc := &adminServiceImpl{accountRepo: repo}
		uncapped := 99
		updated, err := svc.UpdateAccount(ctx, 1, &UpdateAccountInput{Extra: extra, Concurrency: &uncapped})
		if extra["proxy_mode"] == "random" {
			require.ErrorIs(t, err, ErrProtectedProxyModeChange)
			continue
		}
		require.NoError(t, err)
		require.True(t, updated.AntiDegradationEnabled())
		require.Equal(t, uncapped, updated.Concurrency)
		require.NoError(t, ValidateAccountProtectionConfiguration(updated))
	}
}

func TestAccountProtectionDisableRequiresConfirmationAndKeepsIdentity(t *testing.T) {
	ctx := context.Background()
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32}
	PrepareNewAccountProtection(a)
	seed := a.Extra[codexFingerprintSeedExtraKey]
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: a}}
	svc := NewAntiDegradeService(&adminServiceImpl{accountRepo: repo})
	_, err := svc.SetProtection(ctx, 1, false, false)
	require.ErrorContains(t, err, "明确确认")
	require.True(t, a.AntiDegradationEnabled())
	disabled, err := svc.SetProtection(ctx, 1, false, true)
	require.NoError(t, err)
	require.False(t, disabled.AntiDegradationEnabled())
	require.Equal(t, 32, disabled.Concurrency)
	updated, err := svc.SetProtection(ctx, 1, true, false)
	require.NoError(t, err)
	require.True(t, updated.AntiDegradationEnabled())
	require.Equal(t, "legacy", updated.ProtectionMode())
	require.Equal(t, codexFingerprintSession, updated.GetCodexFingerprintMode())
	require.Equal(t, "nodejs24", updated.Extra["tls_fingerprint_builtin"])
	require.Equal(t, seed, updated.Extra[codexFingerprintSeedExtraKey])
}

func TestDefaultProtectionKeepsEnabledExistingModes(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []AntiDegradeMode{AntiDegradeMode1, AntiDegradeMode2, AntiDegradeModeMinimal} {
		t.Run(string(mode), func(t *testing.T) {
			a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3}
			admin := &adminServiceImpl{accountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: a}}}
			svc := NewAntiDegradeService(admin)
			applied, err := svc.ApplyAntiDegradeMode(ctx, 1, mode)
			require.NoError(t, err)
			original := snapshotAntiDegradeConfig(applied)
			updated, err := svc.SetProtection(ctx, 1, true, false)
			require.NoError(t, err)
			require.Equal(t, string(mode), updated.ProtectionMode())
			require.Equal(t, original, snapshotAntiDegradeConfig(updated))
			require.Equal(t, 3, updated.Concurrency)
		})
	}
}

func TestProtectionApplyRevertRestoresTLSConfiguration(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []AntiDegradeMode{AntiDegradeModeLegacy, AntiDegradeModeMinimal, AntiDegradeModeSession, AntiDegradeModeTLSNode24, AntiDegradeModeLowConcurrency, AntiDegradeMode2} {
		t.Run(string(mode), func(t *testing.T) {
			a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32,
				Extra: map[string]any{"enable_tls_fingerprint": true, "tls_fingerprint_profile_id": 9, "codex_fingerprint_mode": "device"}}
			original := snapshotAntiDegradeConfig(a)
			admin := &adminServiceImpl{accountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: a}}}
			svc := NewAntiDegradeService(admin)
			_, err := svc.ApplyAntiDegradeMode(ctx, 1, mode)
			require.NoError(t, err)
			restored, err := svc.Revert(ctx, 1)
			require.NoError(t, err)
			require.Equal(t, original, snapshotAntiDegradeConfig(restored))
			require.True(t, restored.IsTLSFingerprintEnabled())
			require.EqualValues(t, 9, restored.GetTLSFingerprintProfileID())
		})
	}
}

func TestGenericProtectionRevertPreservesUnmanagedTLSAndCanSelectMode1(t *testing.T) {
	ctx := context.Background()
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 32,
		Extra: map[string]any{"proxy_mode": "random", "enable_tls_fingerprint": true, "tls_fingerprint_profile_id": 9}}
	PrepareNewAccountProtection(a)
	require.Equal(t, AntiDegradeMode("generic"), antiDegradeMode(a))
	admin := &adminServiceImpl{accountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: a}}}
	svc := NewAntiDegradeService(admin)
	_, err := admin.UpdateAccount(ctx, 1, &UpdateAccountInput{Extra: map[string]any{"enable_tls_fingerprint": true, "tls_fingerprint_profile_id": 9}})
	require.NoError(t, err)
	mode1, err := svc.ApplyAntiDegradeMode(ctx, 1, AntiDegradeMode1)
	require.NoError(t, err)
	require.True(t, mode1.IsMode1ProtectionEnabled())
	restored, err := svc.Revert(ctx, 1)
	require.NoError(t, err)
	require.True(t, restored.IsTLSFingerprintEnabled())
	require.EqualValues(t, 9, restored.GetTLSFingerprintProfileID())
	reenabled, err := svc.SetProtection(ctx, 1, true, false)
	require.NoError(t, err)
	require.Equal(t, "legacy", reenabled.ProtectionMode())
}
