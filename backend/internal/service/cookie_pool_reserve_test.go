package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCookiePoolFixedReserveAndModeSwitch(t *testing.T) {
	p := poolTestPolicy()
	p.RedundancyConcurrency = 25
	require.Equal(t, 105, cookiePoolTargetCapacity(p, 70), "legacy policy remains percentage based")
	p.RedundancyMode = "fixed"
	require.Equal(t, 95, cookiePoolTargetCapacity(p, 70))
	require.Equal(t, 25, cookiePoolTargetCapacity(p, 0), "fixed reserve remains warm without traffic")
	g := &CookieRotationPoolGroup{}
	reconcileCookiePool(g, p, nil, poolTestConfig(p, g), time.Now(), map[string]bool{})
	require.Equal(t, 95, g.TargetCapacity)
	p.DemandMode = "dynamic"
	g.DemandConcurrency = 100
	reconcileCookiePool(g, p, nil, poolTestConfig(p, g), time.Now(), map[string]bool{})
	require.Equal(t, 125, g.TargetCapacity)
	p.RedundancyMode = "percent"
	require.Equal(t, 105, cookiePoolTargetCapacity(p, 70))
	require.Equal(t, 262, cookiePoolTargetCapacity(CookieRotationPoolPolicy{RedundancyPercent: 40}, 187))
	for _, invalid := range []CookieRotationPoolPolicy{
		{GroupID: 2, RedundancyMode: "both"},
		{GroupID: 2, RedundancyConcurrency: -1},
		{GroupID: 2, RedundancyConcurrency: 1000001},
	} {
		require.Error(t, (&SettingService{}).validateCookieRotationPools(context.Background(), &OpenAICookieSettings{RotationPools: []CookieRotationPoolPolicy{invalid}}))
	}
}

type ticketUnavailableRepo struct{ SettingRepository }

func (ticketUnavailableRepo) GetValue(context.Context, string) (string, error) {
	return "", errors.New("database unavailable")
}

func TestCodexTicketDisabledOnSettingsFailure(t *testing.T) {
	static := &config.Config{Gateway: config.GatewayConfig{OpenAICodexTicket: config.OpenAICodexTicketConfig{Enabled: true, OverrideTurnState: true, RelayEnabled: true, RelayAllowMint: true}}}
	for _, repo := range []SettingRepository{
		ticketUnavailableRepo{},
		&cookieTestRepo{values: map[string]string{SettingKeyOpenAICodexTicketSettings: "invalid json"}},
		&cookieTestRepo{values: map[string]string{SettingKeyOpenAICodexTicketSettings: `{"enabled":false,"override_turn_state":false,"relay_enabled":false}`}},
	} {
		svc := &OpenAIGatewayService{cfg: static, settingService: &SettingService{settingRepo: repo, cfg: static}}
		cfg := svc.codexTicketConfig()
		require.False(t, cfg.Enabled)
		require.False(t, cfg.OverrideTurnState)
		require.False(t, cfg.RelayEnabled)
		a := &Account{ID: 2007}
		stale := OpenAICodexTicketConfigDefaults(static.Gateway.OpenAICodexTicket)
		// No upstream/repository is installed: disabled probes must return before either is called.
		svc.probeOpenAICodexTicket(context.Background(), a, stale)
		require.Nil(t, svc.storeOpenAICodexTicket(a, "gAAAAA"+strings.Repeat("x", 774), time.Now(), stale))
		require.Empty(t, a.Extra)
	}
}

type degradedCountingCache struct {
	schedulerTestConcurrencyCache
	counts map[int64]int
}

func (c *degradedCountingCache) AcquireAccountSlot(_ context.Context, id int64, limit int, _ string) (bool, error) {
	if c.counts[id] >= limit {
		return false, nil
	}
	c.counts[id]++
	return true, nil
}
func (c *degradedCountingCache) ReleaseAccountSlot(_ context.Context, id int64, _ string) error {
	c.counts[id]--
	return nil
}
func (c *degradedCountingCache) GetAccountConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	result := map[int64]int{}
	for _, id := range ids {
		result[id] = c.counts[id]
	}
	return result, nil
}

func TestCookieDegradedRequestsHoldAccountConcurrency(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		t.Run(advanced, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			a := cookieGuardTestAccount("", time.Time{})
			a.Concurrency = 1
			exhaustCookieTestCandidates(&a)
			cache := &degradedCountingCache{counts: map[int64]int{}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a}}, settingService: cookieDegradedTestSettings(), rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(cache), cache: &schedulerTestGatewayCache{}}
			group := int64(3)
			selectAccount := func() *AccountSelectionResult {
				selected, _, err := svc.SelectAccountWithScheduler(context.Background(), &group, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				return selected
			}
			selected := selectAccount()
			require.True(t, selected.Account.openaiCookieDegraded)
			require.True(t, selected.Acquired)
			counts, err := svc.concurrencyService.GetAccountConcurrencyBatch(context.Background(), []int64{a.ID})
			require.NoError(t, err)
			require.Equal(t, 1, counts[a.ID], "the account-list counter must include degraded requests")
			second := selectAccount()
			require.False(t, second.Acquired, "degraded traffic cannot exceed account concurrency")
			require.NotNil(t, second.WaitPlan)
			selected.ReleaseFunc()
			require.Zero(t, cache.counts[a.ID])
		})
	}
}
