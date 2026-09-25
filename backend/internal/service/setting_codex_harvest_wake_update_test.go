//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexHarvestWakeAfterSuccessfulSettingsWrite(t *testing.T) {
	for _, withAuth := range []bool{false, true} {
		t.Run(map[bool]string{false: "settings", true: "auth-defaults"}[withAuth], func(t *testing.T) {
			repo := &settingUpdateRepoStub{}
			svc := NewSettingService(repo, &config.Config{})
			settings := &SystemSettings{OpenAICodexTicketEnabled: true}
			update := func() error {
				if withAuth {
					return svc.UpdateSettingsWithAuthSourceDefaults(context.Background(), settings, nil)
				}
				return svc.UpdateSettings(context.Background(), settings)
			}
			require.NoError(t, update())
			require.Len(t, svc.codexHarvestWakeups(), 1)
			<-svc.codexHarvestWakeups()
			require.NoError(t, update())
			require.Empty(t, svc.codexHarvestWakeups(), "unchanged settings must not wake")
			settings.OpenAICodexTicketFailClosed = true
			require.NoError(t, update())
			require.Len(t, svc.codexHarvestWakeups(), 1)
			<-svc.codexHarvestWakeups()
			settings.OpenAICodexTicketEnabled = false
			repo.setMultipleErr = errors.New("write failed")
			require.Error(t, update())
			require.Empty(t, svc.codexHarvestWakeups(), "failed persistence must not wake")
		})
	}
}

func TestCodexHarvestWakeSettingsChangeFilter(t *testing.T) {
	svc := NewSettingService(&settingUpdateRepoStub{updates: map[string]string{
		SettingKeyOpenAICodexTicketEnabled: "true",
	}}, &config.Config{})
	require.False(t, svc.codexHarvestSettingsChanged(context.Background(), map[string]string{"site_name": "renamed"}))
	require.False(t, svc.codexHarvestSettingsChanged(context.Background(), map[string]string{SettingKeyOpenAICodexTicketEnabled: "true"}))
	for _, key := range []string{SettingKeyOpenAICodexTicketEnabled, SettingKeyOpenAICodexTicketFailClosed,
		SettingKeyOpenAICodexTicketModels, SettingKeyOpenAICodexTicketHarvestProxyURL, SettingKeyOpenAICodexTicketHarvestScope} {
		require.True(t, svc.codexHarvestSettingsChanged(context.Background(), map[string]string{key: "changed"}))
	}
}
