package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type slowSpilloverLoadCache struct {
	schedulerTestConcurrencyCache
}

func (c slowSpilloverLoadCache) GetAccountsLoadBatch(ctx context.Context, _ []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestOpenAILoadSpilloverSlowReadKeepsOwner(t *testing.T) {
	group := int64(7)
	owner := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 20, GroupIDs: []int64{group}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, cache: &schedulerTestGatewayCache{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{owner}}, concurrencyService: NewConcurrencyService(slowSpilloverLoadCache{})}
	scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
	started := time.Now()
	selected, escaped, err := scheduler.selectBySessionHash(context.Background(), OpenAIAccountScheduleRequest{GroupID: &group, Platform: PlatformOpenAI, SessionHash: "session", StickyAccountID: 1})
	require.NoError(t, err)
	require.False(t, escaped)
	require.True(t, selected.Acquired)
	require.EqualValues(t, 1, selected.Account.ID)
	require.Less(t, time.Since(started), time.Second)
	selected.ReleaseFunc()
}

func TestOpenAILoadSpilloverBeforeFull(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		for _, batch := range []bool{false, true} {
			for _, scenario := range []string{"idle", "boundary", "similar", "below_threshold", "unknown", "read_error", "excluded", "disabled", "lost_slot"} {
				t.Run(fmt.Sprintf("%s/%t/%s", advanced, batch, scenario), func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
					group := int64(7)
					owner := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 20, GroupIDs: []int64{group}}
					other := owner
					other.ID = 2
					loads := map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: 14, LoadRate: 70}, 2: {AccountID: 2}}
					acquired, released := []int64{}, []int64{}
					cc := schedulerTestConcurrencyCache{loadMap: loads, acquiredIDs: &acquired, releasedIDs: &released}
					var excluded map[int64]struct{}
					expected := int64(1)
					switch scenario {
					case "idle":
						expected = 2
					case "boundary":
						loads[2].CurrentConcurrency, loads[2].LoadRate, expected = 8, 40, 2
					case "similar":
						loads[2].CurrentConcurrency, loads[2].LoadRate = 10, 50
					case "below_threshold":
						loads[1].CurrentConcurrency, loads[1].LoadRate = 13, 65
					case "unknown":
						delete(loads, 2)
						cc.skipDefaultLoad = true
					case "read_error":
						cc.loadBatchErr = errors.New("unavailable")
					case "excluded":
						excluded = map[int64]struct{}{2: {}}
					case "disabled":
						other.Schedulable = false
					case "lost_slot":
						cc.acquireResults = map[int64]bool{2: false}
					}
					cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session": 1}}
					cfg := &config.Config{}
					cfg.Gateway.Scheduling.LoadBatchEnabled = batch
					svc := &OpenAIGatewayService{cfg: cfg, cache: cache, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{owner, other}}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(cc)}
					selected, decision, err := svc.SelectAccountWithScheduler(context.Background(), &group, "", "session", "gpt-5.1", excluded, OpenAIUpstreamTransportAny, false)
					require.NoError(t, err)
					require.True(t, selected.Acquired)
					require.Nil(t, selected.WaitPlan)
					require.Equal(t, expected, selected.Account.ID)
					require.Equal(t, expected == 1, decision.StickySessionHit)
					require.EqualValues(t, 1, cache.sessionBindings["openai:session"])
					require.NotContains(t, excluded, int64(1), "caller exclusions must not be changed")
					selected.ReleaseFunc()
					require.Equal(t, []int64{expected}, released)
				})
			}
		}
	}
}

func TestOpenAILoadSpilloverPreservesProtectedOwner(t *testing.T) {
	for _, protection := range []string{"task", "guardian", "previous_response"} {
		t.Run(protection, func(t *testing.T) {
			owner := &Account{ID: 1, Platform: PlatformOpenAI, Concurrency: 20}
			svc := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(nil)}
			req := OpenAIAccountScheduleRequest{}
			switch protection {
			case "task":
				req.DisableStickyEscape = true
			case "guardian":
				req.PreserveStickyBinding = true
			case "previous_response":
				req.PreviousResponseID = "resp_owner"
			}
			require.Nil(t, svc.selectOpenAILessLoadedAlternative(context.Background(), req, owner))
		})
	}
}
