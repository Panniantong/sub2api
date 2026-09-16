package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAllProtectionStrategiesKeepEditableConcurrency(t *testing.T) {
	for _, raw := range RegisteredProtectionModes() {
		t.Run(raw, func(t *testing.T) {
			a := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 64}
			PrepareNewAccountProtection(a)
			require.Equal(t, 64, a.Concurrency)
			admin, protection := protectionRegressionServices(a)
			configured, err := protection.ApplyAntiDegradeMode(context.Background(), 42, AntiDegradeMode(raw))
			require.NoError(t, err)
			require.Equal(t, 64, configured.Mode1EffectiveConcurrency(), "selecting a strategy does not restore its preset concurrency")
			wanted := 128
			updated, err := admin.UpdateAccount(context.Background(), 42, &UpdateAccountInput{Concurrency: &wanted})
			require.NoError(t, err)
			require.Equal(t, wanted, updated.Concurrency)
			require.Equal(t, wanted, updated.Mode1EffectiveConcurrency())
			require.NoError(t, ValidateAccountProtectionConfiguration(updated))
			restored, err := protection.RevertAntiDegrade(context.Background(), 42)
			require.NoError(t, err)
			require.Equal(t, wanted, restored.Concurrency)
		})
	}
}
