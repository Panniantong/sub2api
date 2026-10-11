package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/singleflight"
)

func TestCookieLowHostDashboardCountsAndConstantReads(t *testing.T) {
	for _, n := range []int{80, 800} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s, r, accounts := cookieAdminFixture(n)
			var cfg OpenAICookieSettings
			require.NoError(t, json.Unmarshal([]byte(r.values[cookieSettingsKey]), &cfg))
			cfg.RotationPools[0].LowHostRotationEnabled = true
			cfg.RotationPools[0].LowHostThreshold = 5
			cfg.RotationPools[0].LowHostAccountSlots = 10
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			r.values[cookieSettingsKey] = string(raw)
			g := r.snapshot.Groups[2]
			now := time.Now()
			for i := range accounts {
				accounts[i].Concurrency = 35
				g.Members[accounts[i].ID] = CookieRotationPoolMember{State: "standby"}
			}
			g.Members[1] = CookieRotationPoolMember{State: "active", LowHostForced: true, LowHostBindingUntil: now.Add(time.Minute)}
			g.Members[2] = CookieRotationPoolMember{State: "preparing", Since: now, LowHostForced: true, KeepDegraded: true}
			g.Members[3] = CookieRotationPoolMember{State: "waiting", KeepDegraded: true}
			g.Members[4] = CookieRotationPoolMember{State: "waiting", RetryAt: now.Add(time.Minute)}
			g.Members[5] = CookieRotationPoolMember{State: "active", LowHostForced: true, LowHostBindingUntil: now.Add(-time.Second)}
			g.Members[6] = CookieRotationPoolMember{State: "active"}
			for _, i := range []int{1, 2, 3, 4} {
				accounts[i].Extra[openAICodexCookieHostExtraKey] = ""
			}
			gateway := &OpenAIGatewayService{settingService: s, accountRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}}
			for _, detail := range []bool{false, true} {
				r.batchReads, r.poolReads = 0, 0
				r.reads = map[string]int{}
				group := int64(0)
				if detail {
					group = 2
				}
				d, err := gateway.CookieDashboard(context.Background(), group)
				require.NoError(t, err)
				row := d.PoolGroups[0]
				require.Equal(t, CookieLowHostDashboard{SlotsUsed: 3, Preparing: 1, Active: 1, Retiring: 1, Waiting: 1, Blocked: 1, ReadyCapacity: 35, DegradedContinuing: 2}, row.LowHost)
				require.Equal(t, 70, row.ReadyCapacity)
				require.Equal(t, 1, r.batchReads)
				require.Len(t, r.emails, n/2)
				require.Equal(t, 1, r.poolReads)
				require.Equal(t, 1, r.reads[SettingKeyOpenAICodexCookieLibrary])
				require.LessOrEqual(t, r.reads[cookieSettingsKey], 1)
				if detail {
					require.Len(t, row.Accounts, n)
					require.Equal(t, "active", row.Accounts[0].LowHost.State)
					require.NotNil(t, row.Accounts[0].LowHost.BindingUntil)
					require.True(t, row.Accounts[1].LowHost.DegradedContinuing)
				} else {
					require.Empty(t, row.Accounts)
				}
			}
			r.fail = context.DeadlineExceeded
			_, err = gateway.CookieDashboard(context.Background())
			require.Error(t, err, "failed batch must not appear as an empty healthy dashboard")
		})
	}
}

type cookieDashboardBlockingRepo struct {
	cookieMonitorRepo
	started chan struct{}
	release chan struct{}
	reads   atomic.Int32
}

func (r *cookieDashboardBlockingRepo) ListByPlatform(ctx context.Context, platform string) ([]Account, error) {
	if r.reads.Add(1) == 1 {
		close(r.started)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.release:
		return r.cookieMonitorRepo.ListByPlatform(ctx, platform)
	}
}

func TestCookieDashboardConcurrentReadAndCanceledViewer(t *testing.T) {
	s, r, accounts := cookieAdminFixture(80)
	repo := &cookieDashboardBlockingRepo{cookieMonitorRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}, started: make(chan struct{}), release: make(chan struct{})}
	gateway := &OpenAIGatewayService{settingService: s, accountRepo: repo}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := gateway.CookieDashboard(ctx, 2); done <- err }()
	<-repo.started
	// Attach waiters to the exact flight before canceling its first viewer.
	waiters := make([]<-chan singleflight.Result, 20)
	for i := range waiters {
		waiters[i] = gateway.cookieDashboardReads.DoChan("2", func() (any, error) { panic("must join in-flight read") })
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	close(repo.release)
	for _, waiter := range waiters {
		select {
		case result := <-waiter:
			require.NoError(t, result.Err)
			require.True(t, result.Shared)
		case <-time.After(5 * time.Second):
			t.Fatal("shared read did not finish")
		}
	}
	require.EqualValues(t, 1, repo.reads.Load())
	require.Equal(t, 1, r.batchReads)
	require.Equal(t, 1, r.poolReads)
	_, err := gateway.CookieDashboard(context.Background(), 2)
	require.NoError(t, err)
	require.EqualValues(t, 2, repo.reads.Load(), "next refresh reads live data; no TTL cache")
}
