package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIPoolWaitUsesAnyRecoveredAccount(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		for _, batch := range []bool{false, true} {
			t.Run(fmt.Sprintf("advanced=%s/batch=%t", advanced, batch), func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				group := int64(7)
				accounts := []Account{
					{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{group}},
					{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{group}},
				}
				busy := map[int64]bool{1: false, 2: false}
				cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session": 1}}
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = batch
				cfg.Gateway.Scheduling.FallbackWaitTimeout = 3 * time.Second
				cfg.Gateway.Scheduling.FallbackMaxWaiting = 8
				cfg.Gateway.OpenAIWS.LBTopK = 1
				svc := &OpenAIGatewayService{cfg: cfg, cache: cache,
					accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
					rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService(advanced),
					concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: busy}),
				}
				selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &group, "", "session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.NotNil(t, selection.WaitPlan)
				require.True(t, selection.WaitPlan.RecheckPool)
				require.Equal(t, 3*time.Second, selection.WaitPlan.Timeout)
				busy[2] = true
				selection, _, err = svc.SelectAccountWithScheduler(context.Background(), &group, "", "session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.True(t, selection.Acquired)
				require.EqualValues(t, 2, selection.Account.ID)
				selection.ReleaseFunc()
				require.EqualValues(t, 1, cache.sessionBindings["openai:session"], "temporary capacity spillover must not migrate the conversation")
			})
		}
	}
}

func TestOpenAIPoolWaitPreservesTaskOwner(t *testing.T) {
	group := int64(7)
	a := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{group}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, cache: &schedulerTestGatewayCache{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a}}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false}})}
	scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
	selection, escaped, err := scheduler.selectBySessionHash(context.Background(), OpenAIAccountScheduleRequest{GroupID: &group, Platform: PlatformOpenAI, SessionHash: "session", StickyAccountID: 1, DisableStickyEscape: true})
	require.NoError(t, err)
	require.False(t, escaped)
	require.EqualValues(t, 1, selection.Account.ID)
	require.NotNil(t, selection.WaitPlan)
	require.False(t, selection.WaitPlan.RecheckPool)
}

func TestOpenAIPoolWaitKeepsStatefulContinuationOnOwner(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		t.Run(advanced, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			group := int64(9)
			owner := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{group}, Extra: map[string]any{"openai_apikey_responses_websockets_v2_enabled": true}}
			other := owner
			other.ID = 2
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			svc := &OpenAIGatewayService{cfg: cfg, cache: &schedulerTestGatewayCache{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{owner, other}}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false, 2: true}})}
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(context.Background(), group, "resp_owner", 1, time.Hour))
			selection, decision, err := svc.SelectAccountWithScheduler(context.Background(), &group, "resp_owner", "session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.EqualValues(t, 1, selection.Account.ID)
			require.Equal(t, openAIAccountScheduleLayerPreviousResponse, decision.Layer)
			require.False(t, selection.Acquired)
			if selection.WaitPlan != nil {
				require.False(t, selection.WaitPlan.RecheckPool)
			}
		})
	}
}
