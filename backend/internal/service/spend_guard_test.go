package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpendGuardBreach(t *testing.T) {
	cfg := SpendGuardSettings{Enabled: true, WindowMinutes: 5, TokensPerMinute: 1000, MinRequests: 5, MaxErrorRate: 0.8}

	// 样本不足
	ok, _ := spendGuardBreach(SpendGuardOffender{Requests: 3, Tokens: 100000}, cfg)
	require.False(t, ok)

	// 速率超阈
	ok, reason := spendGuardBreach(SpendGuardOffender{Requests: 10, Tokens: 10 * 1000 * 5, TokensPerMin: 10000}, cfg)
	require.True(t, ok)
	require.Equal(t, "token velocity", reason)

	// 错误率超阈
	ok, reason = spendGuardBreach(SpendGuardOffender{Requests: 2, Errors: 8, ErrRate: 0.8, TokensPerMin: 10}, cfg)
	require.True(t, ok)
	require.Equal(t, "error rate", reason)

	// 正常
	ok, _ = spendGuardBreach(SpendGuardOffender{Requests: 10, Tokens: 100, TokensPerMin: 20, Errors: 1, ErrRate: 0.09}, cfg)
	require.False(t, ok)
}

func TestNormalizeSpendGuardSettings(t *testing.T) {
	norm := normalizeSpendGuardSettings(SpendGuardSettings{})
	require.True(t, norm.WindowMinutes > 0)
	require.True(t, norm.MinRequests > 0)
	require.True(t, norm.MaxErrorRate > 0 && norm.MaxErrorRate <= 1)
	require.True(t, norm.IntervalSeconds >= 10)
}

func TestSpendGuardSettingsRoundtrip(t *testing.T) {
	repo := &stubHealthSettingRepo{}
	svc := NewSpendGuardService(nil, nil, repo)
	got := svc.GetSettings(context.Background())
	require.False(t, got.Enabled)

	updated, err := svc.UpdateSettings(context.Background(), SpendGuardSettings{Enabled: true})
	require.NoError(t, err)
	require.True(t, updated.Enabled)
	require.Contains(t, repo.values[SettingKeySpendGuardSettings], `"enabled":true`)
	require.Empty(t, svc.Events())
}
