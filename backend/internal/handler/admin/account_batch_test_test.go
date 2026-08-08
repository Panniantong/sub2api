package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountBatchTestAdminService struct {
	*stubAdminService
	accountsByID []*service.Account
}

func (s *accountBatchTestAdminService) GetAccountsByIDs(context.Context, []int64) ([]*service.Account, error) {
	return s.accountsByID, nil
}

func TestNormalizeBatchAccountTestIDsRemovesInvalidAndDuplicateIDs(t *testing.T) {
	require.Equal(t, []int64{7, 3, 9}, normalizeBatchAccountTestIDs([]int64{0, 7, 3, 7, -1, 9, 3}))
}

func TestAccountBatchTestJobStoreLimitsConcurrencyAndTracksProgress(t *testing.T) {
	const workerCount = 10
	const total = 25

	release := make(chan struct{})
	var active atomic.Int32
	var maxActive atomic.Int32
	var started atomic.Int32

	store := newAccountBatchTestJobStore(func(ctx context.Context, accountID int64) bool {
		current := active.Add(1)
		defer active.Add(-1)
		started.Add(1)
		for {
			observed := maxActive.Load()
			if current <= observed || maxActive.CompareAndSwap(observed, current) {
				break
			}
		}

		select {
		case <-release:
			return accountID%2 == 0
		case <-ctx.Done():
			return false
		}
	}, workerCount, time.Minute)

	accountIDs := make([]int64, 0, total)
	eligible := make(map[int64]bool, total)
	for id := int64(1); id <= total; id++ {
		accountIDs = append(accountIDs, id)
		eligible[id] = true
	}

	job := store.Start(accountIDs, eligible)
	require.Eventually(t, func() bool { return started.Load() == workerCount }, time.Second, 5*time.Millisecond)
	require.LessOrEqual(t, maxActive.Load(), int32(workerCount))

	running, ok := store.Get(job.ID)
	require.True(t, ok)
	require.Equal(t, accountBatchTestStateRunning, running.State)
	require.Equal(t, total, running.Total)
	require.Zero(t, running.Processed)

	close(release)
	require.Eventually(t, func() bool {
		status, found := store.Get(job.ID)
		return found && status.State == accountBatchTestStateCompleted
	}, 2*time.Second, 10*time.Millisecond)

	completed, ok := store.Get(job.ID)
	require.True(t, ok)
	require.Equal(t, total, completed.Processed)
	require.Equal(t, total/2, completed.Success)
	require.Equal(t, total-total/2, completed.Failed)
	require.Equal(t, completed.Processed, completed.Success+completed.Failed)
}

func TestAccountBatchTestJobStoreCountsIneligibleAccountsWithoutRunningThem(t *testing.T) {
	var calls atomic.Int32
	store := newAccountBatchTestJobStore(func(context.Context, int64) bool {
		calls.Add(1)
		return true
	}, 2, time.Minute)

	job := store.Start([]int64{1, 2, 3}, map[int64]bool{1: true, 3: true})
	require.Eventually(t, func() bool {
		status, ok := store.Get(job.ID)
		return ok && status.State == accountBatchTestStateCompleted
	}, time.Second, 5*time.Millisecond)

	completed, ok := store.Get(job.ID)
	require.True(t, ok)
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, 3, completed.Processed)
	require.Equal(t, 2, completed.Success)
	require.Equal(t, 1, completed.Failed)
}

func TestAccountHandlerBatchTestAcceptsDeduplicatedSelectionAndReportsProgress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminService := &accountBatchTestAdminService{
		stubAdminService: newStubAdminService(),
		accountsByID: []*service.Account{
			{ID: 1, Platform: service.PlatformOpenAI},
			{ID: 2, Platform: service.PlatformAnthropic},
		},
	}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, &service.AccountTestService{}, nil, nil, nil, nil, nil)
	handler.batchTestJobs = newAccountBatchTestJobStore(func(_ context.Context, accountID int64) bool {
		return accountID == 1
	}, 10, time.Minute)
	handler.batchTestJobsInit.Do(func() {})

	router := gin.New()
	router.POST("/accounts/batch-test", handler.CreateBatchTest)
	router.GET("/accounts/batch-test/:job_id", handler.GetBatchTest)

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/accounts/batch-test", bytes.NewBufferString(`{"account_ids":[1,2,2,3,0]}`))
	createRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(createRecorder, createRequest)
	require.Equal(t, http.StatusAccepted, createRecorder.Code)

	var createResponse struct {
		Data accountBatchTestJob `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRecorder.Body.Bytes(), &createResponse))
	require.NotEmpty(t, createResponse.Data.ID)
	require.Equal(t, accountBatchTestModel, createResponse.Data.Model)
	require.Equal(t, 3, createResponse.Data.Total)

	require.Eventually(t, func() bool {
		status, ok := handler.batchTestJobs.Get(createResponse.Data.ID)
		return ok && status.State == accountBatchTestStateCompleted
	}, time.Second, 5*time.Millisecond)

	statusRecorder := httptest.NewRecorder()
	statusRequest := httptest.NewRequest(http.MethodGet, "/accounts/batch-test/"+createResponse.Data.ID, nil)
	router.ServeHTTP(statusRecorder, statusRequest)
	require.Equal(t, http.StatusOK, statusRecorder.Code)

	var statusResponse struct {
		Data accountBatchTestJob `json:"data"`
	}
	require.NoError(t, json.Unmarshal(statusRecorder.Body.Bytes(), &statusResponse))
	require.Equal(t, 3, statusResponse.Data.Processed)
	require.Equal(t, 1, statusResponse.Data.Success)
	require.Equal(t, 2, statusResponse.Data.Failed)
}
