package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type poolWaitCache struct {
	service.ConcurrencyCache
	waiting, releases int
	available         bool
}

func (s *poolWaitCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return s.available, nil
}
func (s *poolWaitCache) ReleaseAccountSlot(context.Context, int64, string) error {
	s.releases++
	return nil
}
func (s *poolWaitCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	s.waiting++
	return true, nil
}
func (s *poolWaitCache) DecrementAccountWaitCount(context.Context, int64) error {
	s.waiting--
	return nil
}

func TestOpenAIPoolWaitAdmissionSharesDeadlineAndReleasesQueue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	cache := &poolWaitCache{}
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), "", 0)}
	selection := &service.AccountSelectionResult{Account: &service.Account{ID: 1, Concurrency: 1}, WaitPlan: &service.AccountWaitPlan{RecheckPool: true, MaxConcurrency: 1, MaxWaiting: 8, Timeout: time.Second}}
	started := false
	release, result := h.acquireResponsesAccountSlot(c, nil, "", selection, false, &started, zap.NewNop())
	require.Nil(t, release)
	require.Equal(t, openAISlotAcquireReselect, result)
	require.Zero(t, cache.waiting)
	deadline, _ := c.Get(openAIAccountWaitDeadlineKey)
	selection.Account.ID = 2
	release, result = h.acquireResponsesAccountSlot(c, nil, "", selection, false, &started, zap.NewNop())
	require.Nil(t, release)
	require.Equal(t, openAISlotAcquireReselect, result)
	after, _ := c.Get(openAIAccountWaitDeadlineKey)
	require.Equal(t, deadline, after, "changing accounts must not extend the deadline")
	require.Zero(t, cache.waiting)
	c.Set(openAIAccountWaitDeadlineKey, time.Now().Add(-time.Millisecond))
	released := 0
	selection.Acquired = true
	selection.WaitPlan = nil
	selection.ReleaseFunc = func() { released++ }
	_, result = h.acquireResponsesAccountSlot(c, nil, "", selection, false, &started, zap.NewNop())
	require.Equal(t, openAISlotAcquireFailed, result)
	require.Equal(t, 1, released, "late acquisitions must be released")
}

func TestOpenAIPoolWaitCancellationAndBudget(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	h := &ConcurrencyHelper{}
	started := false
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	require.ErrorIs(t, h.waitOpenAIPoolRetry(c, ctx, time.Second, false, &started), context.Canceled)
	remaining := openAIAccountWaitRemaining(c, 10*time.Millisecond, true)
	err := h.waitOpenAIPoolRetry(c, c.Request.Context(), remaining, false, &started)
	var timeout *ConcurrencyError
	require.ErrorAs(t, err, &timeout)
	require.True(t, timeout.IsTimeout)
	require.LessOrEqual(t, openAIAccountWaitRemaining(c, time.Minute, true), time.Duration(0))
}

func TestOpenAIPoolWaitDisconnectReleasesWaitCount(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	cache := &poolWaitCache{}
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), "", 0)}
	selection := &service.AccountSelectionResult{Account: &service.Account{ID: 1, Concurrency: 1}, WaitPlan: &service.AccountWaitPlan{RecheckPool: true, MaxConcurrency: 1, MaxWaiting: 8, Timeout: time.Second}}
	timer := time.AfterFunc(20*time.Millisecond, cancel)
	defer timer.Stop()
	started := false
	release, result := h.acquireResponsesAccountSlot(c, nil, "", selection, false, &started, zap.NewNop())
	require.Nil(t, release)
	require.Equal(t, openAISlotAcquireFailed, result)
	require.Zero(t, cache.waiting)
}
