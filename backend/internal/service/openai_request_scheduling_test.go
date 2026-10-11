package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type requestSchedulingRepo struct {
	SettingRepository
	values     map[string]string
	batchCalls int
	err        error
	beforeRead func()
}

func TestRequestSchedulingGroupOverrides(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	repo := &requestSchedulingRepo{values: map[string]string{
		openAIAdvancedSchedulerSettingKey:                 "false",
		SettingKeyOpenAIAdvancedSchedulerPreOutputSeconds: "25",
		SettingKeyOpenAIAdvancedSchedulerMaxSwitches:      "5",
		SettingKeyOpenAIAdvancedSchedulerGroupPolicies:    `[{"group_id":2,"mode":"low_load","pre_output_seconds":25,"max_switches":5},{"group_id":5,"mode":"legacy"},{"group_id":6,"mode":"inherit","pre_output_seconds":0,"max_switches":0}]`,
	}}
	svc := &OpenAIGatewayService{settingService: NewSettingService(repo, nil)}
	group := int64(2)
	ctx := svc.WithOpenAISchedulingSnapshot(context.Background(), &group)
	p := OpenAIRequestSchedulingFromContext(ctx)
	require.True(t, p.Enabled)
	require.NotNil(t, svc.getOpenAIAccountScheduler(ctx))
	require.Equal(t, "low_load", p.Mode)
	require.Equal(t, 25, p.PreOutputSeconds)
	require.Equal(t, 5, p.MaxSwitches)
	group = 5
	p = OpenAIRequestSchedulingFromContext(svc.WithOpenAISchedulingSnapshot(context.Background(), &group))
	require.False(t, p.Enabled)
	require.True(t, p.LegacyRetries)
	require.Nil(t, svc.getOpenAIAccountScheduler(svc.WithOpenAISchedulingSnapshot(context.Background(), &group)))
	require.Zero(t, p.PreOutputSeconds)
	require.Equal(t, -1, p.MaxSwitches)
	require.True(t, OpenAIRequestSchedulingFromContext(svc.WithOpenAISchedulingSnapshot(ctx, &group)).Enabled, "an admitted request keeps its original policy")
	group = 6
	p = OpenAIRequestSchedulingFromContext(svc.WithOpenAISchedulingSnapshot(context.Background(), &group))
	require.False(t, p.Enabled)
	require.Zero(t, p.PreOutputSeconds)
	require.Zero(t, p.MaxSwitches)
	group = 7
	p = OpenAIRequestSchedulingFromContext(svc.WithOpenAISchedulingSnapshot(context.Background(), &group))
	require.False(t, p.Enabled)
	require.False(t, p.LegacyRetries)
	require.Equal(t, 25, p.PreOutputSeconds)
	require.Equal(t, 1, repo.batchCalls, "group resolution must reuse the same settings batch cache")
	settings := &SystemSettings{OpenAIAdvancedSchedulerGroupPolicies: repo.values[SettingKeyOpenAIAdvancedSchedulerGroupPolicies]}
	require.NoError(t, svc.settingService.UpdateSettings(context.Background(), settings))
	require.Equal(t, settings.OpenAIAdvancedSchedulerGroupPolicies, svc.settingService.parseSettings(repo.values).OpenAIAdvancedSchedulerGroupPolicies)
	for _, raw := range []string{`{}`, `[{"group_id":0,"mode":"legacy"}]`, `[{"group_id":2,"mode":"bad"}]`, `[{"group_id":2,"mode":"legacy"},{"group_id":2,"mode":"low_load"}]`, `[{"group_id":2,"mode":"inherit","pre_output_seconds":-1}]`, `[{"group_id":2,"mode":"inherit","max_switches":21}]`} {
		require.Error(t, normalizeOpenAIRequestSchedulingSettings(&SystemSettings{OpenAIAdvancedSchedulerGroupPolicies: raw}))
	}
}

func (r *requestSchedulingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	r.batchCalls++
	if r.beforeRead != nil {
		r.beforeRead()
	}
	return r.values, r.err
}
func (r *requestSchedulingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.values = values
	return nil
}

func TestRequestSchedulingSaveSnapshotsAndValidation(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	repo := &requestSchedulingRepo{}
	settings := NewSettingService(repo, &config.Config{})
	svc := &OpenAIGatewayService{settingService: settings}
	value := &SystemSettings{OpenAIAdvancedSchedulerEnabled: true, OpenAIAdvancedSchedulerMode: "low_load", OpenAIAdvancedSchedulerQueueSeconds: " 2 ", OpenAIAdvancedSchedulerPreOutputSeconds: "25", OpenAIAdvancedSchedulerMaxSwitches: "5"}
	require.NoError(t, settings.UpdateSettings(context.Background(), value))
	require.Equal(t, "2", repo.values[SettingKeyOpenAIAdvancedSchedulerQueueSeconds])
	loaded := settings.parseSettings(repo.values)
	require.Equal(t, "low_load", loaded.OpenAIAdvancedSchedulerMode)
	require.Equal(t, "25", loaded.OpenAIAdvancedSchedulerPreOutputSeconds)
	require.Equal(t, "5", loaded.OpenAIAdvancedSchedulerMaxSwitches)
	ctx := svc.WithOpenAISchedulingSnapshot(context.Background())
	require.Equal(t, OpenAIRequestSchedulingPolicy{Enabled: true, Mode: "low_load", QueueSeconds: 2, PreOutputSeconds: 25, MaxSwitches: 5}, OpenAIRequestSchedulingFromContext(ctx))
	value.OpenAIAdvancedSchedulerMode = "balanced"
	value.OpenAIAdvancedSchedulerMaxSwitches = "0"
	require.NoError(t, settings.UpdateSettings(context.Background(), value))
	require.Equal(t, 5, OpenAIRequestSchedulingFromContext(svc.WithOpenAISchedulingSnapshot(ctx)).MaxSwitches)
	require.Equal(t, 0, OpenAIRequestSchedulingFromContext(svc.WithOpenAISchedulingSnapshot(context.Background())).MaxSwitches)
	require.Zero(t, repo.batchCalls, "saving publishes settings without another request-side query")
	for _, invalid := range []SystemSettings{
		{OpenAIAdvancedSchedulerMode: "random"}, {OpenAIAdvancedSchedulerQueueSeconds: "0"}, {OpenAIAdvancedSchedulerQueueSeconds: "11"},
		{OpenAIAdvancedSchedulerPreOutputSeconds: "0"}, {OpenAIAdvancedSchedulerPreOutputSeconds: "3601"},
		{OpenAIAdvancedSchedulerMaxSwitches: "-1"}, {OpenAIAdvancedSchedulerMaxSwitches: "21"}, {OpenAIAdvancedSchedulerMaxSwitches: "1.5"},
	} {
		require.Error(t, settings.UpdateSettings(context.Background(), &invalid))
	}
	require.Equal(t, "0", repo.values[SettingKeyOpenAIAdvancedSchedulerMaxSwitches], "invalid settings must not persist")
}

func TestRequestSchedulingReadFailureKeepsPolicyWithoutQueryFanout(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	old := &cachedOpenAIAdvancedSchedulerSetting{enabled: true, requestPolicy: OpenAIRequestSchedulingPolicy{Mode: "low_load", QueueSeconds: 2, PreOutputSeconds: 25, MaxSwitches: 5}}
	openAIAdvancedSchedulerSettingCache.Store(old)
	repo := &requestSchedulingRepo{err: errors.New("database busy")}
	svc := &OpenAIGatewayService{settingService: NewSettingService(repo, nil)}
	for i := 0; i < 10; i++ {
		require.Equal(t, old.requestPolicy, svc.openAIAdvancedSchedulerRuntimeSettings(context.Background()).requestPolicy)
	}
	require.Equal(t, 1, repo.batchCalls)
}

func TestRequestSchedulingOldReadCannotOverwriteSave(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	fresh := &cachedOpenAIAdvancedSchedulerSetting{enabled: true, requestPolicy: OpenAIRequestSchedulingPolicy{Mode: "low_load", QueueSeconds: 3, PreOutputSeconds: 25, MaxSwitches: 5}, expiresAt: time.Now().Add(time.Minute).UnixNano()}
	repo := &requestSchedulingRepo{values: map[string]string{}, beforeRead: func() { openAIAdvancedSchedulerSettingCache.Store(fresh) }}
	svc := &OpenAIGatewayService{settingService: NewSettingService(repo, nil)}
	svc.openAIAdvancedSchedulerRuntimeSettings(context.Background())
	require.Equal(t, fresh.requestPolicy, svc.openAIAdvancedSchedulerRuntimeSettings(context.Background()).requestPolicy)
}

func TestRequestSchedulingLowLoadOrderAndUnknownLoad(t *testing.T) {
	candidate := func(id int64, load int, known bool, score float64) openAIAccountCandidateScore {
		return openAIAccountCandidateScore{account: &Account{ID: id}, loadKnown: known, loadInfo: &AccountLoadInfo{LoadRate: load}, score: score}
	}
	s := &defaultOpenAIAccountScheduler{}
	plan := openAIAccountLoadPlan{topK: 7, candidates: []openAIAccountCandidateScore{
		candidate(1, 25, true, 100), candidate(2, 0, false, 1000), candidate(3, 20, true, 20), candidate(4, 0, true, 1),
	}}
	req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, LowLoadFirst: true, StickyWeighted: true}
	order := s.buildOpenAISelectionOrder(req, plan)
	ids := []int64{}
	for _, c := range order {
		ids = append(ids, c.account.ID)
	}
	require.Equal(t, []int64{4, 3, 1, 2}, ids, "prefer known low load even when the sticky account scores higher")
	req.LowLoadFirst = false
	req.StickyAccountID = 1
	require.Equal(t, int64(1), s.buildOpenAISelectionOrder(req, plan)[0].account.ID)
}

func TestRequestSchedulingLowLoadAffinityAtEqualLoad(t *testing.T) {
	s := &defaultOpenAIAccountScheduler{}
	plan := openAIAccountLoadPlan{topK: 2, candidates: []openAIAccountCandidateScore{
		{account: &Account{ID: 1}, loadKnown: true, loadInfo: &AccountLoadInfo{}, score: 1},
		{account: &Account{ID: 2}, loadKnown: true, loadInfo: &AccountLoadInfo{}, score: 1},
	}}
	counts := map[int64]int{}
	for i := 0; i < 2000; i++ {
		req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, LowLoadFirst: true, StickyWeighted: true, StickyAccountID: 1, SessionHash: time.Unix(int64(i), 0).String()}
		counts[s.buildOpenAISelectionOrder(req, plan)[0].account.ID]++
	}
	require.Greater(t, counts[1], 1500)
	require.Greater(t, counts[2], 100)
}

func TestRequestSchedulingLowLoadTriesIdleOverflowBeforeBusy(t *testing.T) {
	s := &defaultOpenAIAccountScheduler{}
	plan := openAIAccountLoadPlan{topK: 1, candidates: []openAIAccountCandidateScore{
		{account: &Account{ID: 1}, loadKnown: true, loadInfo: &AccountLoadInfo{LoadRate: 50}, score: 100},
		{account: &Account{ID: 2}, loadKnown: true, loadInfo: &AccountLoadInfo{}, score: 1},
		{account: &Account{ID: 3}, loadKnown: true, loadInfo: &AccountLoadInfo{}, score: 2},
	}}
	order := s.buildOpenAISelectionOrder(OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, LowLoadFirst: true}, plan)
	require.Len(t, order, 3)
	require.Equal(t, int64(1), order[2].account.ID)
}

type requestSchedulingAccounts struct {
	schedulerTestOpenAIAccountRepo
	readIDs []int64
}

func (r *requestSchedulingAccounts) GetByID(ctx context.Context, id int64) (*Account, error) {
	r.readIDs = append(r.readIDs, id)
	return r.schedulerTestOpenAIAccountRepo.GetByID(ctx, id)
}

func TestRequestSchedulingLowLoadRoutesAwayFromBusyStickyWithoutCandidateReads(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	group := int64(17)
	accounts := &requestSchedulingAccounts{}
	loads := map[int64]*AccountLoadInfo{}
	for id := int64(1); id <= 40; id++ {
		accounts.accounts = append(accounts.accounts, Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 20, GroupIDs: []int64{group}})
		loads[id] = &AccountLoadInfo{AccountID: id, CurrentConcurrency: 18, LoadRate: 90}
	}
	loads[40] = &AccountLoadInfo{AccountID: 40}
	rateLimit := newOpenAIAdvancedSchedulerRateLimitService("true")
	rateLimit.settingService.settingRepo.(*openAIAdvancedSchedulerSettingRepoStub).values[SettingKeyOpenAIAdvancedSchedulerMode] = "low_load"
	acquired := []int64{}
	svc := &OpenAIGatewayService{accountRepo: accounts, cfg: &config.Config{}, rateLimitService: rateLimit,
		cache:              &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:busy-session": 1}},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{loadMap: loads, acquiredIDs: &acquired}),
	}
	selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &group, "", "busy-session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(40), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	require.Equal(t, []int64{40}, acquired)
	for _, id := range accounts.readIDs {
		require.Equal(t, int64(40), id, "scoring must not fetch each candidate from the database")
	}
}

func TestRequestSchedulingAffinityShareAndBusyExclusion(t *testing.T) {
	s := &defaultOpenAIAccountScheduler{}
	plan := openAIAccountLoadPlan{topK: 1, candidates: []openAIAccountCandidateScore{
		{account: &Account{ID: 1}, loadKnown: true, loadInfo: &AccountLoadInfo{LoadRate: 60}, score: 1},
		{account: &Account{ID: 2}, loadKnown: true, loadInfo: &AccountLoadInfo{}, score: 10},
	}}
	req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, LowLoadFirst: true, StickyWeighted: true, StickyAccountID: 1, SessionHash: "same-session"}
	hits := 0
	for i := 0; i < 10000; i++ {
		order := s.buildOpenAISelectionOrder(req, plan)
		require.Len(t, order, 2)
		require.NotEqual(t, order[0].account.ID, order[1].account.ID)
		if order[0].account.ID == 1 {
			hits++
		}
	}
	require.InDelta(t, 7000, hits, 300)
	plan.candidates[0].loadInfo.LoadRate = 80
	for i := 0; i < 100; i++ {
		require.Equal(t, int64(2), s.buildOpenAISelectionOrder(req, plan)[0].account.ID)
	}
	plan.candidates[0].loadInfo.LoadRate = 60
	req.ExcludedIDs = map[int64]struct{}{1: {}}
	for i := 0; i < 100; i++ {
		require.Equal(t, int64(2), s.buildOpenAISelectionOrder(req, plan)[0].account.ID)
	}
}

func TestRequestSchedulingAffinityStableAcrossRebuild(t *testing.T) {
	s := &defaultOpenAIAccountScheduler{}
	plan := openAIAccountLoadPlan{topK: 1, candidates: []openAIAccountCandidateScore{
		{account: &Account{ID: 1}, loadKnown: true, loadInfo: &AccountLoadInfo{LoadRate: 60}, score: 1},
		{account: &Account{ID: 2}, loadKnown: true, loadInfo: &AccountLoadInfo{}, score: 10},
	}}
	for _, choice := range []bool{true, false} {
		req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, LowLoadFirst: true, StickyWeighted: true, StickyAccountID: 1, lowLoadAffinity: &choice}
		expected := int64(2)
		if choice {
			expected = 1
		}
		for i := 0; i < 100; i++ {
			require.Equal(t, expected, s.buildOpenAISelectionOrder(req, plan)[0].account.ID)
		}
		plan.topK = 0
		require.Empty(t, s.buildOpenAISelectionOrder(req, plan))
		plan.topK = 1
	}
}

func TestRequestSchedulingAffinityKeepsBindingOnSpillover(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:affinity": 1}}
	svc := &OpenAIGatewayService{cache: cache}
	ctx := context.WithValue(context.Background(), openAISchedulingSnapshotKey{}, openAIAdvancedSchedulerRuntimeSettings{enabled: true, requestPolicy: OpenAIRequestSchedulingPolicy{Mode: "low_load"}})
	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ctx, nil, "affinity", 2))
	require.Equal(t, int64(1), cache.sessionBindings["openai:affinity"])
	require.NoError(t, svc.BindStickySession(ctx, nil, "fresh", 2))
	require.Equal(t, int64(2), cache.sessionBindings["openai:fresh"])
}
