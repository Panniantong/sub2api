package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cookieAffinityTestCache struct {
	schedulerTestGatewayCache
	ids []int64
	err error
}

func (c *cookieAffinityTestCache) RememberCookiePoolAffinity(_ context.Context, _ int64, key string, id int64) error {
	if id == 0 {
		id = c.sessionBindings[key]
	}
	if id > 0 {
		next := []int64{id}
		for _, old := range c.ids {
			if old != id && len(next) < 3 {
				next = append(next, old)
			}
		}
		c.ids = next
	}
	return c.err
}
func (c *cookieAffinityTestCache) ReadCookiePoolAffinity(context.Context, int64, string) ([]int64, error) {
	return c.ids, c.err
}

func TestCookiePoolAffinityReachesBothSchedulersAndPreservesGates(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		for _, scenario := range []string{"recent", "sticky", "preparing", "expired_host", "quota", "disabled", "excluded", "cache_failure", "cold", "full", "load_failure", "no_concurrency"} {
			t.Run(advanced+"/"+scenario, func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				now := time.Now()
				a := cookieGuardTestAccount("one.example", now.Add(time.Hour))
				a.ID = 1
				b := cookieGuardTestAccount("one.example", now.Add(time.Hour))
				b.ID = 2
				a.Extra["codex_5h_used_percent"], b.Extra["codex_5h_used_percent"] = 20.0, 80.0
				p := poolTestPolicy()
				p.QuotaPriority = "used_desc"
				g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{1: {State: "active", Since: now}, 2: {State: "active", Since: now}}}
				cfg := poolTestConfig(p, g)
				cache := &cookieAffinityTestCache{schedulerTestGatewayCache: schedulerTestGatewayCache{sessionBindings: map[string]int64{}}, ids: []int64{99, 1}}
				expected := int64(2)
				var excluded map[int64]struct{}
				concurrency := schedulerTestConcurrencyCache{}
				switch scenario {
				case "recent":
					expected = 1
				case "sticky":
					cache.sessionBindings["openai:session"] = 2
				case "preparing":
					g.Members[1] = CookieRotationPoolMember{State: "preparing", Since: now}
				case "expired_host":
					a.Extra["codex_cookie_host_binding_expires_at"] = now.Add(-time.Second).Format(time.RFC3339Nano)
				case "quota":
					until := now.Add(time.Hour)
					a.RateLimitResetAt = &until
				case "disabled":
					a.Schedulable = false
				case "excluded":
					excluded = map[int64]struct{}{1: {}}
				case "cache_failure":
					cache.err = errors.New("cache unavailable")
				case "cold":
					cache.ids = nil
				case "full":
					concurrency.acquireResults = map[int64]bool{1: false}
				case "load_failure":
					concurrency.loadBatchErr = errors.New("load unavailable")
					expected = 1
				case "no_concurrency":
					expected = 1
				}
				raw, err := json.Marshal(cfg)
				require.NoError(t, err)
				repo := &cookiePoolRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{cookieSettingsKey: string(raw), SettingKeyOpenAICodexCookieLibrary: `[{"host":"one.example","cookie":"cookie","expires_at":"2099-01-01T00:00:00Z"}]`}}, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, settingService: &SettingService{settingRepo: repo}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a, b}}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(concurrency), cache: cache}
				svc.cfg.Gateway.Scheduling.LoadBatchEnabled = true
				if scenario == "no_concurrency" {
					svc.concurrencyService = nil
				}
				group := int64(2)
				selected, _, err := svc.SelectAccountWithScheduler(context.Background(), &group, "", "session", "gpt-5.1", excluded, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.NotNil(t, selected)
				require.Equal(t, expected, selected.Account.ID)
				if selected.ReleaseFunc != nil {
					selected.ReleaseFunc()
				}
			})
		}
	}
}
