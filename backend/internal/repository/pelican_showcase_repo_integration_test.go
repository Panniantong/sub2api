//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// The repository manages its own transactions, so fixtures are committed through the
// shared client and removed afterwards. Prune is global by design, so nothing else in
// this package writes pelican_showcase_items.
func TestPelicanShowcaseRepo_PublishListGetPrune(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewPelicanShowcaseRepository(integrationDB)
	suffix := time.Now().UnixNano()
	name := func(label string) string { return fmt.Sprintf("showcase-%s-%d", label, suffix) }

	groupA := mustCreateGroup(t, client, &service.Group{Name: name("a")})
	groupB := mustCreateGroup(t, client, &service.Group{Name: name("b")})
	unbound := mustCreateGroup(t, client, &service.Group{Name: name("unbound")})
	disabled := mustCreateGroup(t, client, &service.Group{Name: name("disabled"), Status: service.StatusDisabled})
	deleted := mustCreateGroup(t, client, &service.Group{Name: name("deleted")})
	account := mustCreateAccount(t, client, &service.Account{Name: name("account")})
	for _, group := range []*service.Group{groupA, groupB, disabled, deleted} {
		mustBindAccountToGroup(t, client, account.ID, group.ID, 1)
	}
	_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET sort_order = 1 WHERE id = $1`, groupA.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET deleted_at = NOW() WHERE id = $1`, deleted.ID)
	require.NoError(t, err)

	nextRun := time.Now().Add(time.Hour)
	plan, err := NewScheduledTestPlanRepository(integrationDB).Create(ctx, &service.ScheduledTestPlan{
		AccountID: account.ID, ModelID: "gpt-6-astra", CronExpression: "0 * * * *", Enabled: true, MaxResults: 100,
		NextRunAt: &nextRun, PelicanConfig: &service.PelicanTestConfig{Prompt: "pelican", ReasoningEffort: "high", ParallelCount: 1},
	})
	require.NoError(t, err)
	allGroups := []int64{groupA.ID, groupB.ID, unbound.ID, disabled.ID, deleted.ID}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM pelican_showcase_items WHERE group_id = ANY($1)`, pq.Array(allGroups))
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM scheduled_test_plans WHERE id = $1`, plan.ID)
	})

	base := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	publish := func(resultID int64, groupIDs []int64, maxItems int) {
		t.Helper()
		require.NoError(t, repo.Publish(ctx, &service.ScheduledTestResult{
			ID: resultID, PlanID: plan.ID, Status: "success", LatencyMs: resultID * 100,
			ResponseText:  fmt.Sprintf(`<svg data-result="%d"></svg>`, resultID),
			StartedAt:     base.Add(time.Duration(resultID) * time.Hour),
			PelicanConfig: &service.PelicanTestConfig{ModelID: "gpt-6-astra", ReasoningEffort: "high"},
		}, groupIDs, maxItems))
	}
	sourcesOf := func(groupID int64) []int64 {
		t.Helper()
		rows, err := integrationDB.QueryContext(ctx, `SELECT source_result_id FROM pelican_showcase_items
 WHERE group_id = $1 ORDER BY generated_at DESC`, groupID)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		out := []int64{}
		for rows.Next() {
			var id int64
			require.NoError(t, rows.Scan(&id))
			out = append(out, id)
		}
		require.NoError(t, rows.Err())
		return out
	}

	// Only selected groups the account belongs to receive a copy; each is trimmed to maxItems.
	selected := []int64{groupA.ID, groupB.ID, unbound.ID}
	for id := int64(1); id <= 5; id++ {
		publish(id, selected, 3)
	}
	publish(5, selected, 3) // republishing the same result is a no-op
	require.Equal(t, []int64{5, 4, 3}, sourcesOf(groupA.ID))
	require.Equal(t, []int64{5, 4, 3}, sourcesOf(groupB.ID))
	require.Empty(t, sourcesOf(unbound.ID), "the plan's account is not in this group")
	require.Empty(t, sourcesOf(disabled.ID), "not selected")

	groups, err := repo.ListGroups(ctx, allGroups)
	require.NoError(t, err)
	require.Len(t, groups, 3, "disabled and deleted groups are hidden")
	require.Equal(t, []int64{groupB.ID, unbound.ID, groupA.ID}, []int64{groups[0].ID, groups[1].ID, groups[2].ID}, "sort_order, then id")
	require.Equal(t, groupA.Name, groups[2].Name)

	items, err := repo.ListItems(ctx, []int64{groupA.ID, groupB.ID}, 2, time.Time{})
	require.NoError(t, err)
	require.Len(t, items, 4)
	for _, item := range items {
		require.Empty(t, item.ResponseText, "the list never carries HTML")
		require.Equal(t, "gpt-6-astra", item.ModelID)
		require.Equal(t, "high", item.ReasoningEffort)
	}
	require.Equal(t, base.Add(5*time.Hour), items[0].GeneratedAt.UTC())
	require.EqualValues(t, 500, items[0].LatencyMs)
	items, err = repo.ListItems(ctx, []int64{groupA.ID}, 3, base.Add(4*time.Hour))
	require.NoError(t, err)
	require.Len(t, items, 2, "items older than the retention cutoff are hidden")

	var oldest int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT id FROM pelican_showcase_items
 WHERE group_id = $1 AND source_result_id = 3`, groupA.ID).Scan(&oldest))
	item, err := repo.GetItem(ctx, oldest, []int64{groupA.ID}, 3, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, item)
	require.Equal(t, `<svg data-result="3"></svg>`, item.ResponseText)
	for _, hidden := range []struct {
		groupIDs []int64
		maxItems int
		since    time.Time
	}{
		{[]int64{groupA.ID}, 2, time.Time{}},             // beyond the per-group count
		{[]int64{groupA.ID}, 3, base.Add(4 * time.Hour)}, // past the retention window
		{[]int64{groupB.ID}, 3, time.Time{}},             // group not selected
	} {
		item, err = repo.GetItem(ctx, oldest, hidden.groupIDs, hidden.maxItems, hidden.since)
		require.NoError(t, err)
		require.Nil(t, item, "%+v", hidden)
	}

	// Prune drops unselected groups, trims to the count and applies the age limit.
	require.NoError(t, repo.Prune(ctx, []int64{groupA.ID}, 2, time.Time{}))
	require.Equal(t, []int64{5, 4}, sourcesOf(groupA.ID))
	require.Empty(t, sourcesOf(groupB.ID))
	require.NoError(t, repo.Prune(ctx, []int64{groupA.ID}, 2, base.Add(5*time.Hour)))
	require.Equal(t, []int64{5}, sourcesOf(groupA.ID))

	var newest int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT id FROM pelican_showcase_items WHERE group_id = $1`, groupA.ID).Scan(&newest))
	removed, err := repo.Delete(ctx, newest)
	require.NoError(t, err)
	require.True(t, removed)
	removed, err = repo.Delete(ctx, newest)
	require.NoError(t, err)
	require.False(t, removed)

	// A soft-deleted account no longer publishes.
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET deleted_at = NOW() WHERE id = $1`, account.ID)
	require.NoError(t, err)
	publish(6, selected, 3)
	require.Empty(t, sourcesOf(groupA.ID))
}
