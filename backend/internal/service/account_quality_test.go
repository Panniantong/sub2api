package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestQualityValidationAndGrading(t *testing.T) {
	plan := pelicanPlan()
	plan.PelicanConfig.QuestionKind = "candy"
	plan.PelicanConfig.Quality = &QualityPolicy{Judge: &QualityJudgeConfig{GroupID: 9, ModelID: "custom-judge", Prompt: "grade"}, ExpectedAnswer: "42", Action: "remove_groups", RemoveGroupIDs: []int64{3}}
	_, err := nextPlanRun(plan, time.Now())
	require.NoError(t, err)
	require.Empty(t, intelligenceTestOutputError(plan.PelicanConfig, " 42\n"))
	require.Empty(t, intelligenceTestOutputError(plan.PelicanConfig, "The answer is 42"), "completed output is sent to the configured judge")
	require.NotContains(t, intelligenceTestPrompt(plan.PelicanConfig), "HTML")
	for _, change := range []func(*ScheduledTestPlan){
		func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.ExpectedAnswer = " " },
		func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.Action = "delete_account" },
		func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.RemoveGroupIDs = nil },
		func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.RemoveGroupIDs = []int64{3, 3} },
		func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.RemoveGroupIDs = []int64{-1} },
		func(p *ScheduledTestPlan) { p.PelicanConfig.QuestionKind = "pelican" },
		func(p *ScheduledTestPlan) { p.AutoRecover = true },
	} {
		copy := *plan
		cfg := *plan.PelicanConfig
		q := *cfg.Quality
		cfg.Quality = &q
		copy.PelicanConfig = &cfg
		change(&copy)
		_, err = nextPlanRun(&copy, time.Now())
		require.Error(t, err)
	}
}
func TestQualityOutcomeRequiresCompletedWrongAnswerAndAllPassingRecovery(t *testing.T) {
	passed := &ScheduledTestResult{Status: "success", QualityJudgment: &QualityJudgment{Verdict: "correct"}}
	wrong := &ScheduledTestResult{Status: "failed", ErrorMessage: "answer_mismatch", QualityJudgment: &QualityJudgment{Verdict: "incorrect"}}
	timeout := &ScheduledTestResult{Status: "failed", ErrorMessage: "timeout"}
	for _, tc := range []struct {
		results []*ScheduledTestResult
		want    string
	}{
		{nil, "inconclusive"}, {[]*ScheduledTestResult{passed, passed}, "passed"},
		{[]*ScheduledTestResult{passed, timeout}, "inconclusive"},
		{[]*ScheduledTestResult{wrong, passed}, "failed"},
		{[]*ScheduledTestResult{wrong, timeout}, "failed"},
	} {
		require.Equal(t, tc.want, qualityOutcome(tc.results))
	}
}

type qualityPlanRepo struct {
	pelicanPlanRepo
	outcomes []string
}

func (r *qualityPlanRepo) ApplyQualityOutcome(_ context.Context, _ *ScheduledTestPlan, _ time.Time, outcome string) (string, error) {
	r.outcomes = append(r.outcomes, outcome)
	return "groups_removed", nil
}
func TestQualityRunnerAppliesCombinedOutcomeOnce(t *testing.T) {
	plans := &qualityPlanRepo{}
	results := &pelicanResults{}
	runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: NewScheduledTestService(plans, results)}
	runner.runPelican = func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
		return &ScheduledTestResult{Status: "failed", ErrorMessage: "answer_mismatch", QualityJudgment: &QualityJudgment{Verdict: "incorrect"}}, nil
	}
	plan := pelicanPlan()
	plan.PelicanConfig.QuestionKind = "candy"
	plan.PelicanConfig.Quality = &QualityPolicy{ExpectedAnswer: "21", Action: "disable_scheduling"}
	runner.runOnePlan(context.Background(), plan)
	require.Equal(t, []string{"failed"}, plans.outcomes)
	require.Len(t, results.results, 2)
	require.Equal(t, "groups_removed", results.results[0].QualityAction)
}
