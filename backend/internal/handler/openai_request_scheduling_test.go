package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type requestSchedulingSettingsRepo struct{ service.SettingRepository }

func TestRequestSchedulingLegacyGroupSkipsBudget(t *testing.T) {
	s := service.NewSettingService(&requestSchedulingSettingsRepo{}, &config.Config{})
	require.NoError(t, s.UpdateSettings(context.Background(), &service.SystemSettings{OpenAIAdvancedSchedulerEnabled: true, OpenAIAdvancedSchedulerPreOutputSeconds: "25", OpenAIAdvancedSchedulerMaxSwitches: "5", OpenAIAdvancedSchedulerGroupPolicies: `[{"group_id":5,"mode":"legacy"}]`}))
	t.Cleanup(func() { require.NoError(t, s.UpdateSettings(context.Background(), &service.SystemSettings{})) })
	for _, group := range []int64{2, 5} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group})
		writer := c.Writer
		h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}, retryBudgetSeconds: 25, maxAccountSwitches: 9}
		b, finish := h.startRetryBudget(c)
		if group == 5 {
			require.Nil(t, b)
			require.Same(t, writer, c.Writer)
			require.Nil(t, c.Request.Context().Done())
			require.False(t, service.OpenAIRequestSchedulingFromContext(c.Request.Context()).Enabled)
		} else {
			require.NotNil(t, b)
			require.NotNil(t, b.budget)
			require.Equal(t, 5, b.switchLimit())
		}
		finish()
	}
}

func (*requestSchedulingSettingsRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func setRequestSchedulingTestPolicy(t *testing.T, seconds, switches string) {
	t.Helper()
	s := service.NewSettingService(&requestSchedulingSettingsRepo{}, &config.Config{})
	require.NoError(t, s.UpdateSettings(context.Background(), &service.SystemSettings{OpenAIAdvancedSchedulerEnabled: true, OpenAIAdvancedSchedulerMode: "low_load", OpenAIAdvancedSchedulerQueueSeconds: "1", OpenAIAdvancedSchedulerPreOutputSeconds: seconds, OpenAIAdvancedSchedulerMaxSwitches: switches}))
	t.Cleanup(func() { require.NoError(t, s.UpdateSettings(context.Background(), &service.SystemSettings{})) })
}

func TestRequestSchedulingQueueSliceReselectsWithoutClientError(t *testing.T) {
	setRequestSchedulingTestPolicy(t, "25", "5")
	c, rec := gin.CreateTestContext(httptest.NewRecorder())
	_ = rec
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	cache := &poolWaitCache{}
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), "", 0)}
	retries, finish := h.startRetryBudget(c)
	defer finish()
	selection := &service.AccountSelectionResult{Account: &service.Account{ID: 1, Concurrency: 1}, WaitPlan: &service.AccountWaitPlan{RecheckPool: true, MaxConcurrency: 1, MaxWaiting: 8, Timeout: 10 * time.Second}}
	started := false
	t0 := time.Now()
	_, result := h.acquireResponsesAccountSlot(c, nil, "", selection, false, &started, zap.NewNop())
	require.Equal(t, openAISlotAcquireReselect, result)
	require.GreaterOrEqual(t, time.Since(t0), 900*time.Millisecond)
	require.Less(t, time.Since(t0), 3*time.Second)
	require.Zero(t, cache.waiting)
	require.False(t, c.Writer.Written())
	require.Empty(t, retries.accounts, "queue-only reselection is not an upstream account attempt")
	deadline, _ := c.Get(openAIAccountWaitDeadlineKey)
	cache.available = true
	selection.Account.ID = 2
	release, result := h.acquireResponsesAccountSlot(c, nil, "", selection, false, &started, zap.NewNop())
	require.Equal(t, openAISlotAcquireOK, result)
	require.NotNil(t, release)
	release()
	require.NoError(t, c.Request.Context().Err(), "queue slice cancellation must not cancel forwarding")
	after, _ := c.Get(openAIAccountWaitDeadlineKey)
	require.Equal(t, deadline, after)
}

func TestRequestSchedulingSwitchCapWorksWithoutTimer(t *testing.T) {
	for _, tc := range []struct {
		value string
		limit int
	}{{"0", 0}, {"5", 5}} {
		t.Run(tc.value, func(t *testing.T) {
			setRequestSchedulingTestPolicy(t, "", tc.value)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
			b, finish := h.startRetryBudget(c)
			defer finish()
			require.NotNil(t, b)
			require.Nil(t, b.budget)
			require.Equal(t, tc.limit, b.switchLimit())
			excluded := map[int64]struct{}{}
			for id := int64(1); id <= int64(tc.limit+1); id++ {
				require.True(t, b.admit(id, excluded))
			}
			require.False(t, b.admit(int64(tc.limit+2), excluded))
		})
	}
}
