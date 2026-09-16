//go:build unit

package service

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"
)

func TestIntelligentAdmissionSharesCapacityAndReleases(t *testing.T) {
	calls := 0
	svc, _ := intelligentRunnerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"12\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
	})
	cache := &stubConcurrencyCacheForTest{acquireResult: false}
	svc.concurrencyService = NewConcurrencyService(cache)
	record := &IntelligentTestRecord{AccountID: 81, Input: "test", ConfigSnapshot: &IntelligentTestConfig{Model: "test"}}
	require.ErrorIs(t, svc.RunIntelligentTest(context.Background(), record), ErrIntelligentAccountBusy)
	require.Zero(t, calls)
	require.Empty(t, cache.releasedAccountIDs)
	cache.acquireResult = true
	require.NoError(t, svc.RunIntelligentTest(context.Background(), record))
	require.Equal(t, 1, calls)
	require.Equal(t, []int64{81}, cache.releasedAccountIDs)
}

func TestIntelligentAdmissionDefersAccountAndSelectedModelCooldown(t *testing.T) {
	calls := 0
	svc, repo := intelligentRunnerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"12\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
	})
	until := time.Now().Add(90 * time.Second)
	repo.account.RateLimitResetAt = &until
	r := &IntelligentTestRecord{AccountID: 81, Input: "test", ConfigSnapshot: &IntelligentTestConfig{Model: "test-model", Evaluator: "exact_answer", ExpectedAnswer: "12", TimeoutSeconds: 30}}
	worker := NewIntelligentTestService(nil, svc)
	worker.run(context.Background(), r)
	require.Equal(t, "queued", r.Status)
	require.NotNil(t, r.AvailableAt)
	require.WithinDuration(t, until, *r.AvailableAt, time.Second)
	require.Contains(t, r.QueueReason, "冷却")
	require.Zero(t, calls)
	repo.account.RateLimitResetAt = nil
	setAccountModelRateLimitSnapshot(repo.account, "test-model", until, "model cooldown", time.Now())
	require.ErrorIs(t, svc.RunIntelligentTest(context.Background(), r), ErrIntelligentAccountBusy)
	require.Zero(t, calls)
	r.ConfigSnapshot.Model = "other-model"
	require.NoError(t, svc.RunIntelligentTest(context.Background(), r))
	require.Equal(t, 1, calls)
	require.Zero(t, repo.writes)
}
