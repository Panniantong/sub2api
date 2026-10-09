package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestCookiePoolMultiGroupAndUnlimitedDemand(t *testing.T) {
	now := time.Now()
	p := poolTestPolicy()
	p.GroupIDs = []int64{2, 4}
	p.DemandMode = "dynamic"
	p.MaxAccounts = 0
	g := &CookieRotationPoolGroup{DemandConcurrency: 350}
	cfg := poolTestConfig(p, g)
	accounts := make([]Account, 15)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("one.example", now.Add(time.Hour))
		accounts[i].ID = int64(i + 1)
		accounts[i].Concurrency = 35
	}
	reconcileCookiePool(g, p, accounts, cfg, now, map[string]bool{})
	require.Len(t, g.Members, 15)
	require.Equal(t, 525, g.TargetCapacity)
	g.DemandConcurrency = 70
	reconcileCookiePool(g, p, accounts, cfg, now.Add(time.Second), map[string]bool{})
	require.Equal(t, 105, g.ReadyCapacity)
	g.DemandUnavailable = true
	g.DemandConcurrency = 0
	reconcileCookiePool(g, p, accounts, cfg, now.Add(2*time.Second), map[string]bool{})
	require.Equal(t, 105, g.ReadyCapacity, "unavailable metrics cannot shrink the pool")
	a := accounts[0]
	a.GroupIDs = []int64{4}
	require.EqualValues(t, 4, cookiePoolPolicy(&a, cfg).GroupID)
	settings := cookieDegradedTestSettings()
	settings.defaultSubGroupReader = cookieDegradedGroupReader{groups: map[int64]*Group{2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive}, 4: {ID: 4, Platform: PlatformOpenAI, Status: StatusActive}}}
	cfg.RotationGroupIDs = []int64{2, 4}
	require.NoError(t, settings.validateCookieRotationPools(context.Background(), cfg))
	cfg.RotationPools = append(cfg.RotationPools, p)
	require.Error(t, settings.validateCookieRotationPools(context.Background(), cfg), "overlapping policies must fail validation")
}
