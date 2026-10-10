package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cookieAdminCountingRepo struct {
	*cookiePoolRepoStub
	reads                 map[string]int
	batchReads, poolReads int
	emails                []string
	until                 time.Time
	fail                  error
}

func (r *cookieAdminCountingRepo) GetValue(ctx context.Context, key string) (string, error) {
	r.reads[key]++
	return r.cookieTestRepo.GetValue(ctx, key)
}
func (r *cookieAdminCountingRepo) CookieRotationPool(ctx context.Context) (*CookieRotationPoolSnapshot, error) {
	r.poolReads++
	return r.cookiePoolRepoStub.CookieRotationPool(ctx)
}
func (r *cookieAdminCountingRepo) CookieEmailCooldownsBatch(ctx context.Context, emails []string) (map[string]map[string]time.Time, error) {
	r.batchReads++
	r.emails = emails
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if r.fail != nil {
		return nil, r.fail
	}
	result := map[string]map[string]time.Time{}
	for _, email := range emails {
		result[email] = map[string]time.Time{"cooled.example": r.until}
	}
	return result, nil
}

func cookieAdminFixture(n int) (*SettingService, *cookieAdminCountingRepo, []Account) {
	now := time.Now()
	g := &CookieRotationPoolGroup{Members: map[int64]CookieRotationPoolMember{}}
	p := poolTestPolicy()
	accounts := make([]Account, n)
	for i := range accounts {
		accounts[i] = cookieGuardTestAccount("bound.example", now.Add(time.Hour))
		accounts[i].ID = int64(i + 1)
		accounts[i].Credentials["email"] = fmt.Sprintf("USER%d@example.com", i/2)
		g.Members[int64(i+1)] = CookieRotationPoolMember{State: "active"}
	}
	raw, _ := json.Marshal(poolTestConfig(p, g))
	r := &cookieAdminCountingRepo{cookiePoolRepoStub: &cookiePoolRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{
		cookieSettingsKey: string(raw), SettingKeyOpenAICodexCookieLibrary: `[{"host":"bound.example","cookie":"c","expires_at":"2099-01-01T00:00:00Z"},{"host":"cooled.example","cookie":"c","expires_at":"2099-01-01T00:00:00Z"},{"host":"free.example","cookie":"c","expires_at":"2099-01-01T00:00:00Z"}]`,
	}}, snapshot: &CookieRotationPoolSnapshot{Groups: map[int64]*CookieRotationPoolGroup{2: g}}}, reads: map[string]int{}, until: now.Add(time.Hour)}
	return &SettingService{settingRepo: r}, r, accounts
}

func TestCookieAdminSnapshotBatches80AccountsAndRefreshesNextRequest(t *testing.T) {
	s, r, accounts := cookieAdminFixture(80)
	ctx := s.WithCookieAdminSnapshot(context.Background(), accounts)
	for i := range accounts {
		status := s.OpenAICookieBinding(ctx, &accounts[i])
		require.Equal(t, 1, status.AvailableHostCount)
		require.True(t, status.AvailableHostCountKnown)
		require.False(t, status.SchedulingBlocked)
		entry, err := s.LookupOpenAICodexCookie(ctx, "bound.example")
		require.NoError(t, err)
		require.NotNil(t, entry)
		require.Nil(t, accounts[i].Extra[openAICodexCookieCooldownsExtraKey], "display snapshot must not mutate scheduling accounts")
	}
	require.Equal(t, 1, r.batchReads)
	require.Len(t, r.emails, 40, "same mailbox reimports share a read")
	require.Equal(t, 1, r.poolReads)
	require.Equal(t, 1, r.reads[cookieSettingsKey])
	require.Equal(t, 1, r.reads[SettingKeyOpenAICodexCookieLibrary])
	r.until = time.Now().Add(-time.Minute)
	next := s.WithCookieAdminSnapshot(context.Background(), accounts)
	require.Equal(t, 2, s.OpenAICookieBinding(next, &accounts[0]).AvailableHostCount)
	require.Equal(t, 2, r.batchReads, "new requests must read current cooldowns")
}

func TestCookieAdminSnapshotFailureIsUnknownAndNotRetriedPerRow(t *testing.T) {
	s, r, accounts := cookieAdminFixture(80)
	r.fail = context.DeadlineExceeded
	ctx := s.WithCookieAdminSnapshot(context.Background(), accounts)
	for i := range accounts {
		status := s.OpenAICookieBinding(ctx, &accounts[i])
		require.False(t, status.AvailableHostCountKnown)
		require.True(t, status.SchedulingBlocked)
		require.Zero(t, status.DegradedGroupID)
	}
	require.Equal(t, 1, r.batchReads)
}

func TestCookieDashboardBatchesMailboxReads(t *testing.T) {
	s, r, accounts := cookieAdminFixture(80)
	gateway := &OpenAIGatewayService{settingService: s, accountRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}}
	d, err := gateway.CookieDashboard(context.Background(), 2)
	require.NoError(t, err)
	require.Len(t, d.PoolGroups[0].Accounts, 80)
	require.Equal(t, 1, r.batchReads)
	require.Equal(t, 1, r.poolReads)
	require.Equal(t, 1, r.reads[SettingKeyOpenAICodexCookieLibrary])
}

func TestCookieAdminSnapshotCancelsWhileSettingsLockIsHeld(t *testing.T) {
	s, r, accounts := cookieAdminFixture(80)
	cookieSettingsMu.Lock()
	defer cookieSettingsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan context.Context, 1)
	go func() { done <- s.WithCookieAdminSnapshot(ctx, accounts) }()
	select {
	case snapshotCtx := <-done:
		_, err := s.GetOpenAICookieSettings(snapshotCtx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Zero(t, r.batchReads, "a cancelled request must not start mailbox queries")
		require.Empty(t, r.reads)
	case <-time.After(time.Second):
		t.Fatal("admin timeout did not interrupt settings lock wait")
	}
}

func TestCookieAdminSnapshotMalformedLibraryRemainsUnknownAndReadsOnce(t *testing.T) {
	s, r, accounts := cookieAdminFixture(80)
	r.values[SettingKeyOpenAICodexCookieLibrary] = `{"broken"`
	ctx := s.WithCookieAdminSnapshot(context.Background(), accounts)
	for i := range accounts {
		status := s.OpenAICookieBinding(ctx, &accounts[i])
		require.False(t, status.AvailableHostCountKnown)
		require.True(t, status.SchedulingBlocked)
		require.Zero(t, status.DegradedGroupID)
	}
	require.Equal(t, 1, r.reads[SettingKeyOpenAICodexCookieLibrary])
}

func TestCookieAdminSnapshotDoesNotChangeLiveSchedulingSettings(t *testing.T) {
	s, r, accounts := cookieAdminFixture(2)
	ctx := s.WithCookieAdminSnapshot(context.Background(), accounts)
	_, err := s.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	live, err := s.GetOpenAICookieSettings(context.Background())
	require.NoError(t, err)
	_, err = live.rotationCandidates()
	require.NoError(t, err)
	_, err = s.GetOpenAICodexCookieLibrary(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, r.reads[SettingKeyOpenAICodexCookieLibrary], "live reads must not inherit admin snapshot closures")
}

func BenchmarkCookieAdmin80Accounts(b *testing.B) {
	s, _, accounts := cookieAdminFixture(80)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := s.WithCookieAdminSnapshot(context.Background(), accounts)
		for j := range accounts {
			s.OpenAICookieBinding(ctx, &accounts[j])
		}
	}
}
