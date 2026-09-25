//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanSchedulePersistenceLeaseAndRetention(t *testing.T) {
	ctx := context.Background()
	var account int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts (name,platform,type,status) VALUES ('pelican-integration','openai','apikey','active') RETURNING id`).Scan(&account))
	defer func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account) }()
	plans := NewScheduledTestPlanRepository(integrationDB)
	results := NewScheduledTestResultRepository(integrationDB)
	svc := service.NewScheduledTestService(plans, results)
	config := &service.PelicanTestConfig{Prompt: "<pelican>", ReasoningEffort: "medium", ParallelCount: 2}
	plan, err := svc.CreatePlan(ctx, &service.ScheduledTestPlan{AccountID: account, ModelID: "gpt-6-astra", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 2, PelicanConfig: config})
	require.NoError(t, err)
	require.Equal(t, config, plan.PelicanConfig)
	second, err := svc.CreatePlan(ctx, &service.ScheduledTestPlan{AccountID: account, ModelID: "gpt-6-astra", CronExpression: "*/30 * * * *", Enabled: true, PelicanConfig: config})
	require.NoError(t, err, "multiple cron plans per account are supported")
	legacy, err := svc.CreatePlan(ctx, &service.ScheduledTestPlan{AccountID: account, CronExpression: "*/30 * * * *", Enabled: true})
	require.NoError(t, err)
	require.Nil(t, legacy.PelicanConfig)
	now := time.Now().Truncate(time.Microsecond)
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_plans SET next_run_at=$2 WHERE id=$1`, plan.ID, now.Add(-time.Minute))
	require.NoError(t, err)
	until := now.Add(15 * time.Minute)
	claimed, err := plans.ClaimPelican(ctx, plan, now, until, now.Add(30*time.Minute))
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_plans SET next_run_at=$2 WHERE id=$1`, second.ID, now.Add(-time.Minute))
	require.NoError(t, err)
	claimed, err = plans.ClaimPelican(ctx, second, now, until, now.Add(30*time.Minute))
	require.NoError(t, err)
	require.False(t, claimed, "another plan for the same account must wait for the active lease")

	claimed, err = plans.ClaimPelican(ctx, plan, now, until, now.Add(30*time.Minute))
	require.NoError(t, err)
	require.False(t, claimed)
	plan.Enabled = false
	_, err = svc.UpdatePlan(ctx, plan)
	require.NoError(t, err)
	require.NoError(t, plans.FinishPelican(ctx, plan.ID, until, now))
	stored, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	require.Nil(t, stored.RunningUntil)
	for i := 0; i < 3; i++ {
		require.NoError(t, svc.SaveResult(ctx, plan.ID, 2, &service.ScheduledTestResult{Status: "success", ResponseText: "<html></html>", StartedAt: now, FinishedAt: now, PelicanConfig: config}))
	}
	saved, err := results.ListByPlanID(ctx, plan.ID, 50)
	require.NoError(t, err)
	require.Len(t, saved, 2)
	require.Equal(t, config, saved[0].PelicanConfig)
	summaries, err := results.ListByPlanID(ctx, plan.ID, 50, false)
	require.NoError(t, err)
	require.Len(t, summaries, 2)
	require.Empty(t, summaries[0].ResponseText)
	detail, err := results.GetResult(ctx, plan.ID, summaries[0].ID)
	require.NoError(t, err)
	require.Equal(t, "<html></html>", detail.ResponseText)
	_, err = results.GetResult(ctx, legacy.ID, summaries[0].ID)
	require.Error(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_results SET created_at=$2 WHERE plan_id=$1`, plan.ID, now.Add(-8*24*time.Hour))
	require.NoError(t, err)
	_, err = results.Create(ctx, &service.ScheduledTestResult{PlanID: legacy.ID, Status: "success", StartedAt: now, FinishedAt: now})
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_results SET created_at=$2 WHERE plan_id=$1`, legacy.ID, now.Add(-8*24*time.Hour))
	require.NoError(t, err)
	require.NoError(t, results.PruneExpiredPelican(ctx, now.Add(-7*24*time.Hour)))
	saved, err = results.ListByPlanID(ctx, plan.ID, 50)
	require.NoError(t, err)
	require.Empty(t, saved)
	saved, err = results.ListByPlanID(ctx, legacy.ID, 50)
	require.NoError(t, err)
	require.Len(t, saved, 1, "do not expire connectivity history")
}

func TestPelicanHistoryReturnsEveryRetainedOutputAcrossAccounts(t *testing.T) {
	ctx := context.Background()
	plans := NewScheduledTestPlanRepository(integrationDB)
	results := NewScheduledTestResultRepository(integrationDB)
	svc := service.NewScheduledTestService(plans, results)
	var accountIDs []int64
	defer func() {
		for _, id := range accountIDs {
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id)
		}
	}()
	var ids []int64
	now := time.Now()
	for i := 0; i < 2; i++ {
		var accountID int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts (name,platform,type,status) VALUES ('pelican-history','openai','apikey','active') RETURNING id`).Scan(&accountID))
		accountIDs = append(accountIDs, accountID)
		plan, err := svc.CreatePlan(ctx, &service.ScheduledTestPlan{AccountID: accountID, ModelID: "gpt-6-astra", CronExpression: "*/3 * * * *", Enabled: false, MaxResults: 6, PelicanConfig: &service.PelicanTestConfig{Prompt: "draw", ReasoningEffort: "medium", ParallelCount: 1}})
		require.NoError(t, err)
		count := 6
		if i == 1 {
			count = 1
		}
		for j := 0; j < count; j++ {
			status := "success"
			if j == 5 {
				status = "failed"
			}
			result, err := results.Create(ctx, &service.ScheduledTestResult{PlanID: plan.ID, Status: status, ResponseText: "<html>saved</html>", StartedAt: now, FinishedAt: now, PelicanConfig: plan.PelicanConfig})
			require.NoError(t, err)
			ids = append(ids, result.ID)
		}
		// Ordinary connection records must not leak into Pelican history.
		legacy, err := svc.CreatePlan(ctx, &service.ScheduledTestPlan{AccountID: accountID, CronExpression: "*/3 * * * *"})
		require.NoError(t, err)
		_, err = results.Create(ctx, &service.ScheduledTestResult{PlanID: legacy.ID, Status: "success", StartedAt: now, FinishedAt: now})
		require.NoError(t, err)
	}
	var found []*service.PelicanHistoryResult
	cursor := int64(0)
	for {
		page, err := svc.ListPelicanHistory(ctx, cursor, 2)
		require.NoError(t, err)
		found = append(found, page.Items...)
		if page.NextCursor == 0 {
			break
		}
		if cursor > 0 {
			require.Less(t, page.NextCursor, cursor)
		}
		cursor = page.NextCursor
	}
	require.Len(t, found, 7)
	actual := make([]int64, 0)
	failed := 0
	for _, result := range found {
		actual = append(actual, result.ID)
		require.Empty(t, result.ResponseText)
		require.Equal(t, "pelican-history", result.AccountName)
		if result.Status == "failed" {
			failed++
		}
	}
	require.ElementsMatch(t, ids, actual)
	require.Equal(t, 1, failed)
}
