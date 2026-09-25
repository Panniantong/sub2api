package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type showcaseSettingsStub struct {
	runtime PelicanShowcaseRuntime
	err     error
}

func (s *showcaseSettingsStub) GetPelicanShowcaseRuntime(context.Context) (PelicanShowcaseRuntime, error) {
	return s.runtime, s.err
}

type publishCall struct {
	resultID int64
	groupIDs []int64
	maxItems int
}

type pruneCall struct {
	groupIDs []int64
	maxItems int
	before   time.Time
}

type showcaseRepoStub struct {
	published []publishCall
	pruned    []pruneCall
	groups    []*PelicanShowcaseGroup
	items     []*PelicanShowcaseItem
	listSince time.Time
	listIDs   []int64
	item      *PelicanShowcaseItem
	deleted   bool
}

func (r *showcaseRepoStub) Publish(_ context.Context, result *ScheduledTestResult, groupIDs []int64, maxItems int) error {
	r.published = append(r.published, publishCall{result.ID, groupIDs, maxItems})
	return nil
}
func (r *showcaseRepoStub) Prune(_ context.Context, groupIDs []int64, maxItems int, before time.Time) error {
	r.pruned = append(r.pruned, pruneCall{groupIDs, maxItems, before})
	return nil
}
func (r *showcaseRepoStub) ListGroups(context.Context, []int64) ([]*PelicanShowcaseGroup, error) {
	return r.groups, nil
}
func (r *showcaseRepoStub) ListItems(_ context.Context, groupIDs []int64, _ int, since time.Time) ([]*PelicanShowcaseItem, error) {
	r.listIDs, r.listSince = groupIDs, since
	return r.items, nil
}
func (r *showcaseRepoStub) GetItem(context.Context, int64, []int64, int, time.Time) (*PelicanShowcaseItem, error) {
	return r.item, nil
}
func (r *showcaseRepoStub) Delete(context.Context, int64) (bool, error) { return r.deleted, nil }

func enabledShowcase(groupIDs ...int64) *showcaseSettingsStub {
	return &showcaseSettingsStub{runtime: PelicanShowcaseRuntime{Enabled: true, Config: PelicanShowcaseConfig{
		GroupIDs: groupIDs, MaxItems: 12, AutoCleanup: true, RetentionDays: 3,
	}}}
}

func pelicanSuccess(id int64, output string) *ScheduledTestResult {
	return &ScheduledTestResult{ID: id, PlanID: 7, Status: "success", ResponseText: output,
		PelicanConfig: &PelicanTestConfig{ModelID: "gpt-6-astra", ReasoningEffort: "high"}}
}

func TestPelicanShowcaseConfigNormalizeAndParse(t *testing.T) {
	cfg, err := NormalizePelicanShowcaseConfig(PelicanShowcaseConfig{GroupIDs: []int64{9, 3, 9}})
	require.NoError(t, err)
	require.Equal(t, PelicanShowcaseConfig{GroupIDs: []int64{3, 9}, MaxItems: 20, RetentionDays: 7}, cfg)

	for _, bad := range []PelicanShowcaseConfig{
		{MaxItems: 101}, {MaxItems: -1}, {RetentionDays: 91}, {GroupIDs: []int64{0}},
		{GroupIDs: make([]int64, 51)},
	} {
		if len(bad.GroupIDs) == 51 {
			for i := range bad.GroupIDs {
				bad.GroupIDs[i] = int64(i + 1)
			}
		}
		_, err := NormalizePelicanShowcaseConfig(bad)
		require.Error(t, err, "%+v", bad)
	}

	cfg, err = parsePelicanShowcaseConfig("")
	require.NoError(t, err)
	require.Equal(t, DefaultPelicanShowcaseConfig(), cfg, "never configured means defaults with no groups")
	_, err = parsePelicanShowcaseConfig("{broken")
	require.Error(t, err, "corrupt config must not silently become defaults")

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	require.True(t, PelicanShowcaseConfig{AutoCleanup: false, RetentionDays: 3}.retentionCutoff(now).IsZero())
	require.Equal(t, now.Add(-72*time.Hour), PelicanShowcaseConfig{AutoCleanup: true, RetentionDays: 3}.retentionCutoff(now))
}

func TestPelicanShowcasePublishOnlyEnabledHTMLSuccess(t *testing.T) {
	ctx := context.Background()
	skipped := []struct {
		name     string
		settings *showcaseSettingsStub
		result   *ScheduledTestResult
	}{
		{"failed run", enabledShowcase(1), &ScheduledTestResult{ID: 1, Status: "failed", ResponseText: "<html>", PelicanConfig: &PelicanTestConfig{}}},
		{"connectivity test", enabledShowcase(1), &ScheduledTestResult{ID: 1, Status: "success", ResponseText: "<html>"}},
		{"answer-only output", enabledShowcase(1), pelicanSuccess(1, "21")},
		{"candy question", enabledShowcase(1), &ScheduledTestResult{ID: 1, Status: "success", ResponseText: "<svg></svg>",
			PelicanConfig: &PelicanTestConfig{QuestionKind: "candy"}}},
		{"built-in candy prompt", enabledShowcase(1), &ScheduledTestResult{ID: 1, Status: "success", ResponseText: "<svg></svg>",
			PelicanConfig: &PelicanTestConfig{Prompt: CandyPrompt}}},
		{"graded quality question", enabledShowcase(1), &ScheduledTestResult{ID: 1, Status: "success", ResponseText: "<html></html>",
			PelicanConfig: &PelicanTestConfig{Quality: &QualityPolicy{}}}},
		{"unsaved result", enabledShowcase(1), pelicanSuccess(0, "<svg></svg>")},
		{"gallery off", &showcaseSettingsStub{runtime: PelicanShowcaseRuntime{Config: PelicanShowcaseConfig{GroupIDs: []int64{1}}}}, pelicanSuccess(1, "<svg></svg>")},
		{"no groups", enabledShowcase(), pelicanSuccess(1, "<svg></svg>")},
		{"unreadable settings", &showcaseSettingsStub{err: errors.New("db down")}, pelicanSuccess(1, "<svg></svg>")},
	}
	for _, tc := range skipped {
		repo := &showcaseRepoStub{}
		svc := &PelicanShowcaseService{repo: repo, settings: tc.settings}
		svc.PublishScheduledResult(ctx, tc.result)
		require.Empty(t, repo.published, tc.name)
	}

	repo := &showcaseRepoStub{}
	svc := &PelicanShowcaseService{repo: repo, settings: enabledShowcase(4, 8)}
	svc.PublishScheduledResult(ctx, pelicanSuccess(55, "<!DOCTYPE html><html><body><svg></svg></body></html>"))
	require.Equal(t, []publishCall{{resultID: 55, groupIDs: []int64{4, 8}, maxItems: 12}}, repo.published)

	var nilService *PelicanShowcaseService
	require.NotPanics(t, func() { nilService.PublishScheduledResult(ctx, pelicanSuccess(1, "<svg></svg>")) })
}

func TestPelicanShowcaseCleanupAppliesLimitsAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	repo := &showcaseRepoStub{}
	(&PelicanShowcaseService{repo: repo, settings: &showcaseSettingsStub{err: errors.New("corrupt")}}).Cleanup(ctx, now)
	require.Empty(t, repo.pruned, "unreadable settings must never delete snapshots")

	settings := enabledShowcase(2)
	settings.runtime.Enabled = false
	(&PelicanShowcaseService{repo: repo, settings: settings}).Cleanup(ctx, now)
	require.Equal(t, []pruneCall{{groupIDs: []int64{2}, maxItems: 12, before: now.Add(-72 * time.Hour)}}, repo.pruned,
		"cleanup keeps running while the gallery is off")

	repo = &showcaseRepoStub{}
	settings.runtime.Config.AutoCleanup = false
	(&PelicanShowcaseService{repo: repo, settings: settings}).Cleanup(ctx, now)
	require.True(t, repo.pruned[0].before.IsZero(), "auto cleanup off keeps only the count limit")

	var nilService *PelicanShowcaseService
	require.NotPanics(t, func() { nilService.Cleanup(ctx, now) })
}

func TestPelicanShowcaseViewAndItemVisibility(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	repo := &showcaseRepoStub{}
	off := enabledShowcase(1)
	off.runtime.Enabled = false
	view, err := (&PelicanShowcaseService{repo: repo, settings: off}).View(ctx, now)
	require.NoError(t, err)
	require.False(t, view.Enabled)
	require.Empty(t, view.Groups)
	_, err = (&PelicanShowcaseService{repo: repo, settings: off}).Item(ctx, 1, now)
	require.ErrorIs(t, err, ErrPelicanShowcaseItemNotFound)

	repo = &showcaseRepoStub{
		groups: []*PelicanShowcaseGroup{{ID: 5, Name: "B"}, {ID: 3, Name: "A"}},
		items: []*PelicanShowcaseItem{
			{ID: 10, GroupID: 3}, {ID: 11, GroupID: 3}, {ID: 12, GroupID: 99},
		},
	}
	svc := &PelicanShowcaseService{repo: repo, settings: enabledShowcase(3, 5, 7)}
	view, err = svc.View(ctx, now)
	require.NoError(t, err)
	require.True(t, view.Enabled)
	require.Equal(t, 12, view.MaxItems)
	require.Equal(t, 3, view.RetentionDays)
	require.Equal(t, []int64{5, 3}, repo.listIDs, "items are only read for groups that still exist and are active")
	require.Equal(t, now.Add(-72*time.Hour), repo.listSince)
	require.Len(t, view.Groups, 2)
	require.Equal(t, "B", view.Groups[0].Name, "repository order (group sort order) is kept")
	require.NotNil(t, view.Groups[0].Items)
	require.Empty(t, view.Groups[0].Items, "a showcased group without snapshots is still listed")
	require.Len(t, view.Groups[1].Items, 2)

	settings := enabledShowcase(3)
	settings.runtime.Config.AutoCleanup = false
	view, err = (&PelicanShowcaseService{repo: repo, settings: settings}).View(ctx, now)
	require.NoError(t, err)
	require.Zero(t, view.RetentionDays, "retention is reported as 0 when auto cleanup is off")

	_, err = svc.Item(ctx, 10, now)
	require.ErrorIs(t, err, ErrPelicanShowcaseItemNotFound, "a snapshot outside the gallery limits is not served")
	repo.item = &PelicanShowcaseItem{ID: 10, GroupID: 3, ResponseText: "<svg></svg>"}
	item, err := svc.Item(ctx, 10, now)
	require.NoError(t, err)
	require.Equal(t, "<svg></svg>", item.ResponseText)

	require.ErrorIs(t, svc.Remove(ctx, 10), ErrPelicanShowcaseItemNotFound)
	repo.deleted = true
	require.NoError(t, svc.Remove(ctx, 10))
}

func TestScheduledSaveResultPublishesSavedPelicanResult(t *testing.T) {
	results := &pelicanResults{}
	repo := &showcaseRepoStub{}
	svc := NewScheduledTestService(&pelicanPlanRepo{}, results)
	svc.showcase = &PelicanShowcaseService{repo: repo, settings: enabledShowcase(6)}

	result := pelicanSuccess(0, "<svg></svg>")
	result.ID = 91 // the stub repository returns the input as the saved row
	require.NoError(t, svc.SaveResult(context.Background(), 7, 50, result))
	require.Equal(t, []publishCall{{resultID: 91, groupIDs: []int64{6}, maxItems: 12}}, repo.published)
	require.Equal(t, 50, results.pruned, "admin history retention is unchanged")
}
