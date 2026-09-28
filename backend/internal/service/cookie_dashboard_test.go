package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookieDashboardCountsLiveTasksAndDegradation(t *testing.T) {
	a := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
	b := cookieGuardTestAccount("healthy.example", time.Now().Add(time.Hour))
	b.ID = a.ID + 1
	b.Extra[openAICodexCookieRotationStatusExtraKey] = "running" // stale persisted marker
	c := cookieGuardTestAccount("", time.Time{})
	c.ID = b.ID + 1
	c.GroupIDs = []int64{4}
	s := &OpenAIGatewayService{settingService: cookieDegradedTestSettings(), accountRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{a, b, c}}}}
	s.cookieHarvestRuntime.accounts = map[int64]bool{a.ID: true}
	s.cookieHarvestRuntime.jobs = map[int64]string{a.ID: "explore"}
	s.openaiCookieRotationRunning.Store(a.ID, true)
	result, err := s.CookieDashboard(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Harvest["total"])
	require.Equal(t, 1, result.Harvest["explore"])
	require.Equal(t, 2, result.RotationAccounts)
	require.Equal(t, 1, result.RotationRunning, "stale persisted running state must not count")
	require.Equal(t, 1, result.DegradedAccounts)
	s.openaiCookieRotationRunning.Delete(a.ID)
	result, err = s.CookieDashboard(context.Background())
	require.NoError(t, err)
	require.Zero(t, result.RotationRunning)
}
