package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	accountBatchTestModel        = "gpt-5.4-mini"
	accountBatchTestWorkerCount  = 10
	accountBatchTestItemTimeout  = 2 * time.Minute
	accountBatchTestJobRetention = 30 * time.Minute

	accountBatchTestStateRunning   = "running"
	accountBatchTestStateCompleted = "completed"
)

type accountBatchTestRunFunc func(ctx context.Context, accountID int64) bool

type accountBatchTestJob struct {
	ID          string     `json:"job_id"`
	State       string     `json:"state"`
	Model       string     `json:"model"`
	Total       int        `json:"total"`
	Processed   int        `json:"processed"`
	Success     int        `json:"success"`
	Failed      int        `json:"failed"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type accountBatchTestJobStore struct {
	mu          sync.Mutex
	jobs        map[string]*accountBatchTestJob
	run         accountBatchTestRunFunc
	workerCount int
	retention   time.Duration
}

func newAccountBatchTestJobStore(run accountBatchTestRunFunc, workerCount int, retention time.Duration) *accountBatchTestJobStore {
	if workerCount <= 0 {
		workerCount = 1
	}
	if retention <= 0 {
		retention = accountBatchTestJobRetention
	}
	return &accountBatchTestJobStore{
		jobs:        make(map[string]*accountBatchTestJob),
		run:         run,
		workerCount: workerCount,
		retention:   retention,
	}
}

func normalizeBatchAccountTestIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	normalized := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized
}

func (s *accountBatchTestJobStore) Start(accountIDs []int64, eligible map[int64]bool) accountBatchTestJob {
	now := time.Now()
	job := &accountBatchTestJob{
		ID:        newAccountBatchTestJobID(now),
		State:     accountBatchTestStateRunning,
		Model:     accountBatchTestModel,
		Total:     len(accountIDs),
		CreatedAt: now,
	}

	queue := make([]int64, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		if eligible[accountID] {
			queue = append(queue, accountID)
			continue
		}
		job.Processed++
		job.Failed++
	}

	s.mu.Lock()
	s.cleanupLocked(now)
	s.jobs[job.ID] = job
	snapshot := cloneAccountBatchTestJob(job)
	s.mu.Unlock()

	if len(queue) == 0 {
		s.complete(job.ID)
		completed, _ := s.Get(job.ID)
		return completed
	}

	go s.runJob(job.ID, queue)
	return snapshot
}

func (s *accountBatchTestJobStore) Get(jobID string) (accountBatchTestJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(time.Now())
	job, ok := s.jobs[jobID]
	if !ok {
		return accountBatchTestJob{}, false
	}
	return cloneAccountBatchTestJob(job), true
}

func (s *accountBatchTestJobStore) runJob(jobID string, accountIDs []int64) {
	workers := s.workerCount
	if workers > len(accountIDs) {
		workers = len(accountIDs)
	}

	queue := make(chan int64)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for accountID := range queue {
				ctx, cancel := context.WithTimeout(context.Background(), accountBatchTestItemTimeout)
				success := s.run != nil && s.run(ctx, accountID)
				cancel()
				s.recordResult(jobID, success)
			}
		}()
	}

	for _, accountID := range accountIDs {
		queue <- accountID
	}
	close(queue)
	wg.Wait()
	s.complete(jobID)
}

func (s *accountBatchTestJobStore) recordResult(jobID string, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok || job.State != accountBatchTestStateRunning {
		return
	}
	job.Processed++
	if success {
		job.Success++
	} else {
		job.Failed++
	}
}

func (s *accountBatchTestJobStore) complete(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok || job.State == accountBatchTestStateCompleted {
		return
	}
	now := time.Now()
	job.State = accountBatchTestStateCompleted
	job.CompletedAt = &now
}

func (s *accountBatchTestJobStore) cleanupLocked(now time.Time) {
	for jobID, job := range s.jobs {
		if job.CompletedAt != nil && now.Sub(*job.CompletedAt) >= s.retention {
			delete(s.jobs, jobID)
		}
	}
}

func cloneAccountBatchTestJob(job *accountBatchTestJob) accountBatchTestJob {
	if job == nil {
		return accountBatchTestJob{}
	}
	copy := *job
	return copy
}

func newAccountBatchTestJobID(now time.Time) string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err == nil {
		return hex.EncodeToString(random)
	}
	return fmt.Sprintf("batch-test-%d", now.UnixNano())
}

type createAccountBatchTestRequest struct {
	AccountIDs []int64 `json:"account_ids"`
}

func (h *AccountHandler) getBatchTestJobStore() *accountBatchTestJobStore {
	if h == nil {
		return nil
	}
	h.batchTestJobsInit.Do(func() {
		h.batchTestJobs = newAccountBatchTestJobStore(func(ctx context.Context, accountID int64) bool {
			if h.accountTestService == nil {
				return false
			}
			result, err := h.accountTestService.RunTestBackground(ctx, accountID, accountBatchTestModel)
			if err != nil || result == nil || result.Status != "success" {
				return false
			}
			if h.rateLimitService != nil {
				_, _ = h.rateLimitService.RecoverAccountAfterSuccessfulTest(ctx, accountID)
			}
			return true
		}, accountBatchTestWorkerCount, accountBatchTestJobRetention)
	})
	return h.batchTestJobs
}

// CreateBatchTest accepts a selected set of accounts and starts a bounded background probe.
func (h *AccountHandler) CreateBatchTest(c *gin.Context) {
	if h == nil || h.adminService == nil || h.accountTestService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account test service unavailable")
		return
	}

	var req createAccountBatchTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	accountIDs := normalizeBatchAccountTestIDs(req.AccountIDs)
	if len(accountIDs) == 0 {
		response.BadRequest(c, "account_ids must contain at least one positive ID")
		return
	}

	accounts, err := h.adminService.GetAccountsByIDs(c.Request.Context(), accountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	eligible := make(map[int64]bool, len(accounts))
	for _, account := range accounts {
		if account != nil && account.Platform == service.PlatformOpenAI {
			eligible[account.ID] = true
		}
	}

	job := h.getBatchTestJobStore().Start(accountIDs, eligible)
	response.Accepted(c, job)
}

// GetBatchTest returns a snapshot of one in-memory batch probe job.
func (h *AccountHandler) GetBatchTest(c *gin.Context) {
	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		response.BadRequest(c, "Invalid batch test job ID")
		return
	}
	job, ok := h.getBatchTestJobStore().Get(jobID)
	if !ok {
		response.NotFound(c, "Batch test job not found")
		return
	}
	response.Success(c, job)
}
