//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// In-memory persistence exercises the real settings save and harvest loop.
// An empty account list guarantees these tests never make upstream requests.
type harvestWakeSettingsRepo struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
	reads  atomic.Int64
	allErr error
}

func (r *harvestWakeSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.values[key]
	if key == SettingKeyOpenAICodexTicketEnabled {
		r.reads.Add(1)
	}
	if !exists {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *harvestWakeSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string)
	for _, key := range keys {
		if value, exists := r.values[key]; exists {
			out[key] = value
		}
	}
	return out, nil
}

func (r *harvestWakeSettingsRepo) SetMultiple(_ context.Context, updates map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range updates {
		r.values[key] = value
	}
	return nil
}

func (r *harvestWakeSettingsRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string)
	for key, value := range r.values {
		out[key] = value
	}
	return out, r.allErr
}

func TestCodexHarvestWakeThroughSettingsSave(t *testing.T) {
	var rounds atomic.Int64
	repo := &harvestWakeSettingsRepo{values: map[string]string{
		SettingKeyOpenAICodexTicketEnabled: "false",
		SettingKeyOpenAICodexTicketModels:  `["gpt-6-astra"]`,
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{HarvestProbeIntervalSeconds: 180}, nil)
	svc.settingService = NewSettingService(repo, svc.cfg)
	svc.accountRepo = &codexTicketLifecycleRepo{list: func(context.Context) ([]Account, error) {
		rounds.Add(1)
		return nil, nil
	}}
	svc.StartOpenAICodexTicketHarvester()
	t.Cleanup(svc.StopOpenAICodexTicketHarvester)
	require.Eventually(t, func() bool { return repo.reads.Load() > 0 }, time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond) // let the disabled startup round arm its timer
	require.Zero(t, rounds.Load())
	settings := &SystemSettings{OpenAICodexTicketEnabled: true, OpenAICodexTicketModels: []string{"gpt-6-astra"}}
	started := time.Now()
	require.NoError(t, svc.settingService.UpdateSettings(context.Background(), settings))
	require.Eventually(t, func() bool { return rounds.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
	t.Logf("hot enable reached the next round in %s (period=180s)", time.Since(started))
	settings.OpenAICodexTicketModels = append(settings.OpenAICodexTicketModels, "gpt-5.6-sol")
	started = time.Now()
	require.NoError(t, svc.settingService.UpdateSettings(context.Background(), settings))
	require.Eventually(t, func() bool { return rounds.Load() == 2 }, 5*time.Second, 10*time.Millisecond)
	t.Logf("new model reached the next round in %s (period=180s)", time.Since(started))
	require.Len(t, svc.openAICodexTicketConfig().Models, 2)
}

func TestCodexHarvestWakePartialWriteReloadFailureStillInvalidates(t *testing.T) {
	repo := &harvestWakeSettingsRepo{values: map[string]string{SettingKeyOpenAICodexTicketEnabled: "false"},
		allErr: errors.New("reload failed after write")}
	s := NewSettingService(repo, &config.Config{})
	require.False(t, s.GetOpenAICodexTicketEnabled(context.Background(), false))
	// Any omitted key takes the partial-write reload path.
	err := s.UpdateSettingsOmitting(context.Background(), &SystemSettings{OpenAICodexTicketEnabled: true},
		OmittedSettingKeys{"site_name": {}})
	require.NoError(t, err)
	require.Len(t, s.codexHarvestWakeups(), 1)
	require.True(t, s.GetOpenAICodexTicketEnabled(context.Background(), false))
}

func TestCodexHarvestWakeOmittedSettingsDoNotNotify(t *testing.T) {
	s := NewSettingService(&harvestWakeSettingsRepo{values: map[string]string{}}, &config.Config{})
	settings := &SystemSettings{OpenAICodexTicketEnabled: true}
	updates, err := s.buildSystemSettingsUpdates(context.Background(), settings)
	require.NoError(t, err)
	omitted := make(OmittedSettingKeys)
	for key := range updates {
		if len(key) >= len("openai_codex_ticket_") && key[:len("openai_codex_ticket_")] == "openai_codex_ticket_" {
			omitted[key] = struct{}{}
		}
	}
	require.NoError(t, s.UpdateSettingsOmitting(context.Background(), settings, omitted))
	require.Empty(t, s.codexHarvestWakeups())
}
