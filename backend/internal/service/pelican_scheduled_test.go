package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func pelicanPlan() *ScheduledTestPlan {
	return &ScheduledTestPlan{ID: 1, AccountID: 42, ModelID: "gpt-6-astra", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 50, PelicanConfig: &PelicanTestConfig{Prompt: "draw a pelican", ReasoningEffort: "medium", ParallelCount: 2}}
}
func TestPelicanPlanValidation(t *testing.T) {
	now := time.Now()
	plan := pelicanPlan()
	plan.AutoRecover = true
	next, err := nextPlanRun(plan, now)
	require.NoError(t, err)
	expected, err := computeNextRun(plan.CronExpression, now)
	require.NoError(t, err)
	require.Equal(t, expected, next)
	require.Equal(t, plan.ModelID, plan.PelicanConfig.ModelID)
	require.True(t, plan.AutoRecover)
	for _, change := range []func(*ScheduledTestPlan){
		func(p *ScheduledTestPlan) { p.PelicanConfig.ParallelCount = 9 },
		func(p *ScheduledTestPlan) { p.CronExpression = "bad cron" },
		func(p *ScheduledTestPlan) { p.PelicanConfig.Prompt = " " },
		func(p *ScheduledTestPlan) { p.PelicanConfig.ReasoningEffort = "invalid" },
		func(p *ScheduledTestPlan) { p.MaxResults = 201 },
	} {
		p := pelicanPlan()
		change(p)
		_, err := nextPlanRun(p, now)
		require.Error(t, err)
	}
	// Legacy connectivity cron remains valid and retains auto recovery.
	old := &ScheduledTestPlan{CronExpression: "*/30 * * * *", AutoRecover: true}
	_, err = nextPlanRun(old, now)
	require.NoError(t, err)
	require.True(t, old.AutoRecover)
}

type pelicanPlanRepo struct {
	ScheduledTestPlanRepository
	mu       sync.Mutex
	claimed  bool
	finished bool
}

func (r *pelicanPlanRepo) ClaimPelican(context.Context, *ScheduledTestPlan, time.Time, time.Time, time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed {
		return false, nil
	}
	r.claimed = true
	return true, nil
}
func (r *pelicanPlanRepo) FinishPelican(context.Context, int64, time.Time, time.Time) error {
	r.finished = true
	return nil
}

type pelicanResults struct {
	ScheduledTestResultRepository
	results []*ScheduledTestResult
	pruned  int
}

func (r *pelicanResults) Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.results = append(r.results, result)
	return result, nil
}
func (r *pelicanResults) PruneOldResults(_ context.Context, _ int64, count int) error {
	r.pruned = count
	return nil
}
func TestPelicanScheduleClaimParallelResultsAndFailure(t *testing.T) {
	plans := &pelicanPlanRepo{}
	results := &pelicanResults{}
	runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: NewScheduledTestService(plans, results)}
	var mu sync.Mutex
	calls := 0
	runner.runPelican = func(_ context.Context, accountID int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, int64(42), accountID)
		require.Equal(t, "gpt-6-astra", model)
		require.Equal(t, "draw a pelican", cfg.Prompt)
		calls++
		if calls == 1 {
			return nil, errors.New("timeout")
		}
		return &ScheduledTestResult{Status: "success", ResponseText: "<html></html>"}, nil
	}
	runner.runOnePlan(context.Background(), pelicanPlan())
	runner.runOnePlan(context.Background(), pelicanPlan())
	require.Equal(t, 2, calls)
	require.Len(t, results.results, 2)
	require.Equal(t, 50, results.pruned)
	require.True(t, plans.finished)
	statuses := []string{results.results[0].Status, results.results[1].Status}
	require.ElementsMatch(t, []string{"success", "failed"}, statuses)
}
func TestPelicanCaptureBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &pelicanRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	_, err := w.Write([]byte(strings.Repeat("x", (4<<20)+1)))
	require.Error(t, err)
	require.True(t, w.overflow)
	require.Zero(t, w.Body.Len())
	require.Error(t, ctx.Err())
}

func TestPelicanIncompleteStreamIsNotSuccess(t *testing.T) {
	text, message := parsePelicanOutput("data: {\"type\":\"content\",\"text\":\"<html></html>\"}\n")
	require.Equal(t, "<html></html>", text)
	require.NotEmpty(t, message)
	_, message = parsePelicanOutput("data: {\"type\":\"test_complete\",\"success\":false}\n")
	require.NotEmpty(t, message)
	_, message = parsePelicanOutput("data: {\"type\":\"test_complete\",\"success\":true}\n")
	require.Empty(t, message)
}

func TestIntelligenceQuestionContractAndLegacyPlans(t *testing.T) {
	for _, kind := range []string{"", "pelican", "candy"} {
		cfg := &PelicanTestConfig{QuestionKind: kind, Prompt: "original question"}
		prompt := intelligenceTestPrompt(cfg)
		require.Contains(t, prompt, "original question")
		if kind == "candy" {
			require.NotContains(t, prompt, "HTML")
			require.Contains(t, prompt, "只输出最终整数")
			require.Empty(t, intelligenceTestOutputError(cfg, "21"))
			require.Empty(t, intelligenceTestOutputError(cfg, "29"), "not an automatic score")
			require.NotEmpty(t, intelligenceTestOutputError(cfg, " "))
		} else {
			require.Contains(t, prompt, PelicanDeliveryContract)
			require.NotEmpty(t, intelligenceTestOutputError(cfg, "21"))
			require.Empty(t, intelligenceTestOutputError(cfg, "<html></html>"))
		}
	}
	plan := pelicanPlan()
	plan.PelicanConfig.QuestionKind = "candy"
	_, err := nextPlanRun(plan, time.Now())
	require.NoError(t, err)
	plan.PelicanConfig.QuestionKind = "unknown"
	_, err = nextPlanRun(plan, time.Now())
	require.Error(t, err)
}

func TestLegacyCandyPlanRejectsWrongAnswer(t *testing.T) {
	cfg := &PelicanTestConfig{Prompt: CandyPrompt}
	require.True(t, isBuiltinCandyPlan(cfg))
	require.Contains(t, intelligenceTestOutputError(cfg, "29"), "answer_mismatch")
	require.Contains(t, intelligenceTestOutputError(cfg, "<html>29</html>"), "answer_mismatch")
	cfg.QuestionKind = "candy"
	require.Empty(t, intelligenceTestOutputError(cfg, "21"))
	require.False(t, isBuiltinCandyPlan(&PelicanTestConfig{Prompt: "custom question"}))
}
