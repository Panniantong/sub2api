package service

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexHarvestWakeCoalescesAndIsNilSafe(t *testing.T) {
	var absent *SettingService
	absent.NotifyCodexHarvest()
	require.Nil(t, absent.codexHarvestWakeups())
	s := &SettingService{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.NotifyCodexHarvest() }()
	}
	wg.Wait()
	require.Len(t, s.codexHarvestWakeups(), 1)
	<-s.codexHarvestWakeups()
	s.NotifyCodexHarvest()
	require.Len(t, s.codexHarvestWakeups(), 1)
}

func TestCodexHarvestWakeHotEnableAndDuringProbe(t *testing.T) {
	var enabled atomic.Bool
	var reads, rounds, inFlight, maxFlight atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	account := ticketTestAccount(41)
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: false, Models: []string{"gpt-6-astra"}, HarvestProxyURL: "http://proxy.example:8080",
		HarvestProbeIntervalSeconds: 180,
	}, &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		return codexTicketResponse(), nil
	}})
	svc.settingService = NewSettingService(&codexTicketLifecycleSettings{get: func(_ context.Context, key string) (string, error) {
		if key == SettingKeyOpenAICodexTicketEnabled {
			value := enabled.Load()
			reads.Add(1)
			return strconv.FormatBool(value), nil
		}
		return "", ErrSettingNotFound
	}}, svc.cfg)
	svc.accountRepo = &codexTicketLifecycleRepo{account: *account, list: func(ctx context.Context) ([]Account, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for old := maxFlight.Load(); n > old; old = maxFlight.Load() {
			if maxFlight.CompareAndSwap(old, n) {
				break
			}
		}
		if rounds.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return []Account{*account}, nil
	}}
	svc.StartOpenAICodexTicketHarvester()
	t.Cleanup(svc.StopOpenAICodexTicketHarvester)
	require.Eventually(t, func() bool { return reads.Load() > 0 }, time.Second, 10*time.Millisecond)
	enabled.Store(true)
	svc.settingService.InvalidateOpenAICodexTicketEnabledCache()
	svc.settingService.NotifyCodexHarvest()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("hot enable waited for periodic timer")
	}
	for i := 0; i < 50; i++ {
		svc.settingService.NotifyCodexHarvest()
	}
	closedAt := time.Now()
	close(release)
	require.Eventually(t, func() bool { return rounds.Load() >= 2 }, 5*time.Second, 10*time.Millisecond)
	require.GreaterOrEqual(t, time.Since(closedAt), openAICodexTicketWakeMinInterval)
	require.Equal(t, int64(1), maxFlight.Load())
	svc.StopOpenAICodexTicketHarvester()
	svc.settingService.NotifyCodexHarvest() // no close/send race after shutdown
}

func TestCodexHarvestWakeStaleReadCannotOverwriteInvalidation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int64
	s := NewSettingService(&codexTicketLifecycleSettings{get: func(ctx context.Context, key string) (string, error) {
		if reads.Add(1) == 1 {
			close(started)
			select {
			case <-release:
				return "false", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return "true", nil
	}}, &config.Config{})
	done := make(chan struct{})
	go func() { defer close(done); s.GetOpenAICodexTicketEnabled(context.Background(), false) }()
	<-started
	s.InvalidateOpenAICodexTicketEnabledCache()
	require.True(t, s.GetOpenAICodexTicketEnabled(context.Background(), false))
	close(release)
	<-done
	require.True(t, s.GetOpenAICodexTicketEnabled(context.Background(), false), "old read must not re-cache disabled")
	require.Equal(t, int64(2), reads.Load())
}
