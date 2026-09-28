package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCookieHarvestTaskSharesAndFallback(t *testing.T) {
	r := cookieHarvestRuntime{}
	r.init()
	now := time.Now()
	settings := &OpenAICookieSettings{CookieProxyScheduleMode: "dynamic", HarvestPolicy: defaultCookieHarvestPolicy(), CookieRefreshBeforeSeconds: 600, CookieProxyLearningAttempts: 40}
	memory := map[string]openAICookieProxyHostBindingState{"p": {Requests: 40, History: map[string]int{"missing.example": 10, "refresh.example": 10}}}
	library := []OpenAICodexCookieLibraryEntry{{Host: "refresh.example", ExpiresAt: now.Add(time.Minute)}}
	counts := map[string]int{}
	for i := 0; i < 100; i++ {
		task := r.plan(now, settings, []string{"p"}, memory, library)
		require.NotNil(t, task)
		counts[task.kind]++
	}
	require.Equal(t, map[string]int{"explore": 30, "fill": 50, "refresh": 20}, counts)
	for i := 0; i < 100; i++ {
		require.Equal(t, "explore", r.plan(now, settings, []string{"p"}, memory, []OpenAICodexCookieLibraryEntry{{Host: "missing.example", ExpiresAt: now.Add(time.Hour)}, {Host: "refresh.example", ExpiresAt: now.Add(time.Hour)}}).kind)
	}
}

func TestCookieHarvestExploresEveryProxyAndRespectsReservations(t *testing.T) {
	r := cookieHarvestRuntime{}
	r.init()
	now := time.Now()
	settings := &OpenAICookieSettings{CookieProxyScheduleMode: "dynamic", HarvestPolicy: defaultCookieHarvestPolicy(), CookieProxyLearningAttempts: 40}
	proxies := []string{}
	for i := 0; i < 221; i++ {
		proxies = append(proxies, fmt.Sprintf("p%03d", i))
	}
	seen := map[string]bool{}
	for i := 0; i < 221; i++ {
		task := r.plan(now.Add(time.Duration(i)*time.Second), settings, proxies, nil, nil)
		require.False(t, seen[task.proxy])
		seen[task.proxy] = true
		r.lastProxy[task.proxy] = now.Add(time.Duration(i) * time.Second)
	}
	r.proxies["p000"] = true
	memory := map[string]openAICookieProxyHostBindingState{"p001": {BackoffUntil: now.Add(time.Hour)}}
	task := r.plan(now, settings, []string{"p000", "p001", "p002"}, memory, nil)
	require.Equal(t, "p002", task.proxy)
	require.Nil(t, r.plan(now, settings, []string{"p000", "p001"}, memory, nil))
}

func TestCookieHarvestMissingHostRotatesProxyAndHonorsWhitelist(t *testing.T) {
	r := cookieHarvestRuntime{}
	r.init()
	now := time.Now()
	p := defaultCookieHarvestPolicy()
	p.ExplorePercent = 1
	p.FillPercent = 99
	p.RefreshPercent = 0
	settings := &OpenAICookieSettings{CookieProxyScheduleMode: "dynamic", HarvestPolicy: p, HostWhitelist: []string{"wanted.example"}}
	memory := map[string]openAICookieProxyHostBindingState{"p1": {History: map[string]int{"wanted.example": 10, "excluded.example": 20}}, "p2": {History: map[string]int{"wanted.example": 1, "other.example": 20}}}
	task := r.plan(now, settings, []string{"p1", "p2"}, memory, nil)
	require.Equal(t, "wanted.example", task.host)
	require.Equal(t, "p1", task.proxy)
	r.deferredTargets["p1\nwanted.example"] = now.Add(time.Minute)
	task = r.plan(now, settings, []string{"p1", "p2"}, memory, nil)
	require.Equal(t, "p2", task.proxy)
	r.targets["wanted.example"] = true
	require.Equal(t, "explore", r.plan(now, settings, []string{"p1", "p2"}, memory, nil).kind)
}

func TestCookieHarvestFailureBackoffAnd401Isolation(t *testing.T) {
	ctx := context.Background()
	svc := &OpenAIGatewayService{settingService: cookieGuardTestSettings(t)}
	proxy := "http://proxy.example:80"
	p := defaultCookieHarvestPolicy()
	settings := &OpenAICookieSettings{HarvestPolicy: p, harvestProxy: proxy}
	require.True(t, svc.recordOpenAICookieProxyAttempt(ctx, proxy, 40))
	for i := 0; i < 3; i++ {
		svc.recordCookieHarvestOutcome(settings, &OpenAICookieAcquisitionLog{harvestAttempted: true, Task: "explore", StatusCode: 401})
	}
	state := svc.loadOpenAICookieProxyBindings(ctx)[proxy]
	require.True(t, state.BackoffUntil.IsZero())
	require.Equal(t, 3, state.Stats.Unauthorized)
	for i := 0; i < 3; i++ {
		svc.recordCookieHarvestOutcome(settings, &OpenAICookieAcquisitionLog{harvestAttempted: true, Task: "fill", TargetHost: "fixed.example"})
	}
	state = svc.loadOpenAICookieProxyBindings(ctx)[proxy]
	require.True(t, state.BackoffUntil.After(time.Now()))
	require.True(t, svc.cookieHarvestRuntime.deferredTargets[proxy+"\nfixed.example"].After(time.Now()))
	svc.recordCookieHarvestOutcome(settings, &OpenAICookieAcquisitionLog{harvestAttempted: true, Task: "fill", TargetHost: "fixed.example", Host: "fixed.example", Success: true, StatusCode: 200})
	state = svc.loadOpenAICookieProxyBindings(ctx)[proxy]
	require.True(t, state.BackoffUntil.IsZero())
	require.Equal(t, 1, state.Stats.TargetHits)
	require.Empty(t, svc.cookieHarvestRuntime.deferredTargets)
	svc.recordOpenAICookieProxyHost(ctx, proxy, "fixed.example", true)
	svc.recordOpenAICookieProxyHost(ctx, proxy, "fixed.example", true)
	state = svc.loadOpenAICookieProxyBindings(ctx)[proxy]
	require.Equal(t, 1, state.Stats.NewHosts)
}

func TestCookieHarvestPolicyValidation(t *testing.T) {
	p := defaultCookieHarvestPolicy()
	require.NoError(t, p.validate())
	p.ExplorePercent = 0
	require.Error(t, p.validate())
	p.ExplorePercent = 30
	p.RefreshPercent = 21
	require.Error(t, p.validate())
}

func TestCookieHarvestRoundRobinKeepsOriginalOrder(t *testing.T) {
	r := cookieHarvestRuntime{}
	r.init()
	now := time.Now()
	settings := &OpenAICookieSettings{CookieProxyScheduleMode: "round_robin"}
	proxies := []string{"p1", "p2", "p3"}
	for _, expected := range proxies {
		task := r.plan(now, settings, proxies, nil, nil)
		require.Equal(t, expected, task.proxy)
		r.proxies[task.proxy] = true
	}
	require.Nil(t, r.plan(now, settings, proxies, nil, nil))
	delete(r.proxies, "p2")
	require.Equal(t, "p2", r.plan(now, settings, proxies, nil, nil).proxy)
}

type cookieConcurrentSettings struct {
	cookieTestRepo
	mu sync.Mutex
}

func (r *cookieConcurrentSettings) GetValue(ctx context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cookieTestRepo.GetValue(ctx, key)
}
func (r *cookieConcurrentSettings) Set(ctx context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cookieTestRepo.Set(ctx, key, value)
}

type cookieSlowHarvestUpstream struct {
	HTTPUpstream
	entered chan int64
	release chan struct{}
}

func (u *cookieSlowHarvestUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	u.entered <- id
	if id == 1 {
		select {
		case <-u.release:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
}
func TestCookieHarvestSlowWorkerDoesNotBlockRefill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	accounts := []Account{}
	for i := int64(1); i <= 3; i++ {
		a := cookieGuardTestAccount("", time.Now())
		a.ID = i
		a.Credentials["access_token"] = "test"
		accounts = append(accounts, a)
	}
	upstream := &cookieSlowHarvestUpstream{entered: make(chan int64, 10), release: make(chan struct{})}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, settingService: &SettingService{settingRepo: &cookieConcurrentSettings{cookieTestRepo: cookieTestRepo{values: map[string]string{}}}}, accountRepo: cookieMonitorRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}, httpUpstream: upstream}
	defer func() { close(upstream.release); svc.cookieHarvestRuntime.wg.Wait() }()
	settings := &OpenAICookieSettings{Enabled: true, Model: "gpt-6-astra", CookieHarvestConcurrency: 2, IntervalSeconds: 60, CookieProxyLearningAttempts: 40, CookieProxyScheduleMode: "dynamic", HarvestPolicy: defaultCookieHarvestPolicy(), ProxyURLs: []string{"http://p1.example:80", "http://p2.example:80"}}
	svc.dispatchOpenAICookieHarvest(ctx, settings)
	seen := map[int64]bool{}
	for len(seen) < 2 {
		select {
		case id := <-upstream.entered:
			seen[id] = true
		case <-time.After(5 * time.Second):
			t.Fatal("initial requests did not start")
		}
	}
	require.True(t, seen[1])
	require.True(t, seen[2])
	require.Eventually(t, func() bool { return svc.CookieHarvestRunning()["total"] == 1 }, 5*time.Second, 10*time.Millisecond)
	svc.dispatchOpenAICookieHarvest(ctx, settings)
	select {
	case id := <-upstream.entered:
		require.Equal(t, int64(3), id)
	case <-time.After(5 * time.Second):
		t.Fatal("free slot did not refill while account 1 was blocked")
	}
	// Account 1 remains reserved; account 2's rest interval prevents reuse.
	require.Eventually(t, func() bool { return svc.CookieHarvestRunning()["total"] == 1 }, 5*time.Second, 10*time.Millisecond)
	svc.dispatchOpenAICookieHarvest(ctx, settings)
	select {
	case id := <-upstream.entered:
		t.Fatalf("duplicate or resting account dispatched: %d", id)
	default:
	}
}
