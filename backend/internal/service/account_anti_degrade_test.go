package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubAntiDegradeStore struct {
	account *Account
	updated *UpdateAccountInput
}

func (s *stubAntiDegradeStore) GetAccount(context.Context, int64) (*Account, error) {
	cp := *s.account
	return &cp, nil
}

func (s *stubAntiDegradeStore) UpdateAccount(_ context.Context, _ int64, input *UpdateAccountInput) (*Account, error) {
	s.updated = input
	return s.account, nil
}

func TestPreviewAntiDegradeOpenAI(t *testing.T) {
	p := PreviewAntiDegrade(&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.True(t, p.Eligible)
	require.Len(t, p.Changes, 3) // session + Node.js 24 TLS + concurrency
	require.Equal(t, "extra.codex_fingerprint_mode", p.Changes[0].Key)
	require.Equal(t, "session", p.Changes[0].To)
}

func TestPreviewMode1Anthropic(t *testing.T) {
	p := PreviewAntiDegradeMode(&Account{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Concurrency: 8}, AntiDegradeMode1)
	require.False(t, p.Eligible)
	require.Contains(t, p.Reason, "OpenAI")
}

func TestPreviewAntiDegradeNothingToDo(t *testing.T) {
	p := PreviewAntiDegrade(&Account{
		ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8,
		Extra: map[string]any{"codex_fingerprint_mode": "session"},
	})
	require.True(t, p.Eligible, "session-only configuration still needs the legacy TLS template")
}

func TestPreviewAntiDegradeAlreadyEnabled(t *testing.T) {
	p := PreviewAntiDegradeMode(&Account{
		ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Extra: map[string]any{AntiDegradeMarkerExtraKey: map[string]any{"enabled": true}},
	}, AntiDegradeMode1)
	require.True(t, p.Enabled)
	require.False(t, p.Eligible)
}

func TestApplyRevertAntiDegrade(t *testing.T) {
	store := &stubAntiDegradeStore{account: &Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}
	svc := NewAntiDegradeService(store)
	_, err := svc.ApplyAntiDegrade(context.Background(), 5)
	require.NoError(t, err)
	require.NotNil(t, store.updated)
	require.Equal(t, "session", store.updated.Extra["codex_fingerprint_mode"])
	require.NotNil(t, store.updated.Concurrency)
	require.Equal(t, AntiDegradeConcurrencyCap, *store.updated.Concurrency)
	marker, ok := store.updated.Extra[AntiDegradeMarkerExtraKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, marker["enabled"])
	require.Equal(t, "legacy", marker["mode"])

	// 还原：marker 存在则恢复旧值
	store.account.Extra = store.updated.Extra
	_, err = svc.RevertAntiDegrade(context.Background(), 5)
	require.NoError(t, err)
	require.NotContains(t, store.updated.Extra, AntiDegradeMarkerExtraKey)
	require.NotContains(t, store.updated.Extra, "codex_fingerprint_mode")
	require.NotNil(t, store.updated.Concurrency)
	require.Equal(t, 0, *store.updated.Concurrency)
}

func TestApplyLegacyUsesInitialStrategy(t *testing.T) {
	store := &stubAntiDegradeStore{account: &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}
	svc := NewAntiDegradeService(store)
	_, err := svc.ApplyAntiDegradeMode(context.Background(), 7, AntiDegradeModeLegacy)
	require.NoError(t, err)
	require.Equal(t, "session", store.updated.Extra["codex_fingerprint_mode"])
	require.Equal(t, true, store.updated.Extra["enable_tls_fingerprint"])
	require.Equal(t, "nodejs24", store.updated.Extra["tls_fingerprint_builtin"])
	marker, ok := store.updated.Extra[AntiDegradeMarkerExtraKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "legacy", marker["mode"])
	require.NotContains(t, marker, "policy_version", "legacy must not be interpreted as mode1-v3")
}

func TestRevertAntiDegradeRejectsBadSnapshot(t *testing.T) {
	store := &stubAntiDegradeStore{account: &Account{
		ID: 6, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Extra: map[string]any{
			AntiDegradeMarkerExtraKey: map[string]any{
				"enabled": true,
				"prev":    map[string]any{"concurrency": "unlimited"},
			},
		},
	}}
	svc := NewAntiDegradeService(store)
	_, err := svc.RevertAntiDegrade(context.Background(), 6)
	require.Error(t, err)
	require.Nil(t, store.updated, "类型非法时不得写库")
}

func TestAntiDegradeDefaultDoesNotReinterpretStoredModes(t *testing.T) {
	require.Equal(t, AntiDegradeModeLegacy, normalizeAntiDegradeMode(""))
	for _, tt := range []struct {
		marker map[string]any
		want   AntiDegradeMode
	}{
		{map[string]any{"enabled": true}, AntiDegradeMode1},
		{map[string]any{"enabled": true, "mode": "mode1", "policy_version": 2}, AntiDegradeMode1},
		{map[string]any{"enabled": true, "mode": "generic"}, "generic"},
	} {
		require.Equal(t, tt.want, antiDegradeMode(&Account{Extra: map[string]any{AntiDegradeMarkerExtraKey: tt.marker}}))
	}
}

func TestAntiDegradeUnknownModeDoesNotApplyDefault(t *testing.T) {
	store := &stubAntiDegradeStore{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}
	svc := NewAntiDegradeService(store)
	_, err := svc.ApplyAntiDegradeMode(context.Background(), 1, "mode_typo")
	require.ErrorIs(t, err, ErrUnknownAntiDegradeMode)
	_, err = svc.PreviewMode(context.Background(), 1, "mode_typo")
	require.ErrorIs(t, err, ErrUnknownAntiDegradeMode)
	require.Nil(t, store.updated)
}
