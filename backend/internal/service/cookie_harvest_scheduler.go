package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The existing attempt setting remains the upper bound for initial learning.
// Successful samples are a separate early completion criterion.
type CookieHarvestPolicy struct {
	ExplorePercent            int `json:"explore_percent"`
	FillPercent               int `json:"fill_percent"`
	RefreshPercent            int `json:"refresh_percent"`
	LearningSamples           int `json:"learning_samples"`
	FailureThreshold          int `json:"failure_threshold"`
	FailureBackoffSeconds     int `json:"failure_backoff_seconds"`
	TargetMissLimit           int `json:"target_miss_limit"`
	TargetBackoffSeconds      int `json:"target_backoff_seconds"`
	ExplorationRevisitSeconds int `json:"exploration_revisit_seconds"`
}

func defaultCookieHarvestPolicy() *CookieHarvestPolicy {
	return &CookieHarvestPolicy{30, 50, 20, 20, 3, 60, 3, 120, 900}
}

func (p *CookieHarvestPolicy) validate() error {
	if p.ExplorePercent < 1 || p.FillPercent < 0 || p.RefreshPercent < 0 || p.ExplorePercent+p.FillPercent+p.RefreshPercent != 100 {
		return fmt.Errorf("探索比例至少 1%%，探索、补齐和刷新比例之和必须为 100%%")
	}
	if p.LearningSamples < 1 || p.LearningSamples > 10000 || p.FailureThreshold < 1 || p.FailureThreshold > 20 || p.TargetMissLimit < 1 || p.TargetMissLimit > 100 {
		return fmt.Errorf("成功学习样本需为 1–10000，连续失败阈值 1–20，目标未命中阈值 1–100")
	}
	if p.FailureBackoffSeconds < 1 || p.FailureBackoffSeconds > 3600 || p.TargetBackoffSeconds < 1 || p.TargetBackoffSeconds > 86400 || p.ExplorationRevisitSeconds < 30 || p.ExplorationRevisitSeconds > 86400 {
		return fmt.Errorf("失败退避需为 1–3600 秒，目标退避 1–86400 秒，探索巡检周期 30–86400 秒")
	}
	return nil
}

type cookieHarvestRuntime struct {
	wg              sync.WaitGroup
	mu              sync.Mutex
	accounts        map[int64]bool
	jobs            map[int64]string
	proxies         map[string]bool
	nextAccount     map[int64]time.Time
	lastProxy       map[string]time.Time
	targets         map[string]bool
	misses          map[string]int
	deferredTargets map[string]time.Time
	credits         [3]int
	accountCursor   uint64
	proxyCursor     uint64
	lastNotice      time.Time
}

func (r *cookieHarvestRuntime) init() {
	if r.accounts != nil {
		return
	}
	r.accounts = map[int64]bool{}
	r.jobs = map[int64]string{}
	r.proxies = map[string]bool{}
	r.nextAccount = map[int64]time.Time{}
	r.lastProxy = map[string]time.Time{}
	r.targets = map[string]bool{}
	r.misses = map[string]int{}
	r.deferredTargets = map[string]time.Time{}
}

type cookieHarvestTask struct{ kind, host, proxy string }
type cookieHarvestTarget struct {
	host   string
	expiry time.Time
}

func cookieProxySamples(s openAICookieProxyHostBindingState) int {
	n := 0
	for _, count := range s.Hosts {
		n += count
	}
	return n
}

func cookieProxyHistory(s openAICookieProxyHostBindingState) map[string]int {
	if len(s.History) > 0 {
		return s.History
	}
	return s.Hosts
}

func cookieHostInScope(host string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return true
	}
	for _, h := range whitelist {
		if normalizeOpenAICookieHost(h) == host {
			return true
		}
	}
	return false
}

// Pick from a single snapshot: no per-host database calls in the planning loop.
func (r *cookieHarvestRuntime) plan(now time.Time, settings *OpenAICookieSettings, proxies []string, memory map[string]openAICookieProxyHostBindingState, library []OpenAICodexCookieLibraryEntry) *cookieHarvestTask {
	p := settings.HarvestPolicy
	if p == nil {
		p = defaultCookieHarvestPolicy()
	}
	available := make([]string, 0, len(proxies))
	for _, raw := range proxies {
		key := normalizeCookieProxyKey(raw)
		if !r.proxies[key] && !memory[key].BackoffUntil.After(now) {
			available = append(available, key)
		}
	}
	if len(available) == 0 {
		return nil
	}
	if settings.CookieProxyScheduleMode != "dynamic" {
		for range proxies {
			proxy := normalizeCookieProxyKey(proxies[r.proxyCursor%uint64(len(proxies))])
			r.proxyCursor++
			if !r.proxies[proxy] && !memory[proxy].BackoffUntil.After(now) {
				return &cookieHarvestTask{kind: "round_robin", proxy: proxy}
			}
		}
		return nil
	}
	expiry := map[string]time.Time{}
	for _, e := range library {
		expiry[e.Host] = e.ExpiresAt
	}
	hosts := map[string]bool{}
	for _, raw := range proxies {
		for host := range cookieProxyHistory(memory[normalizeCookieProxyKey(raw)]) {
			if cookieHostInScope(host, settings.HostWhitelist) {
				hosts[host] = true
			}
		}
	}
	missing, refresh := []cookieHarvestTarget{}, []cookieHarvestTarget{}
	for host := range hosts {
		if r.targets[host] {
			continue
		}
		exp := expiry[host]
		if !exp.After(now) {
			missing = append(missing, cookieHarvestTarget{host, exp})
		} else if settings.CookieRefreshBeforeSeconds > 0 && !exp.After(now.Add(time.Duration(settings.CookieRefreshBeforeSeconds)*time.Second)) {
			refresh = append(refresh, cookieHarvestTarget{host, exp})
		}
	}
	sortTargets := func(items []cookieHarvestTarget) {
		sort.Slice(items, func(i, j int) bool {
			if items[i].expiry.Equal(items[j].expiry) {
				return items[i].host < items[j].host
			}
			return items[i].expiry.Before(items[j].expiry)
		})
	}
	sortTargets(missing)
	sortTargets(refresh)
	chooseTarget := func(kind string, targets []cookieHarvestTarget) *cookieHarvestTask {
		var chosen *cookieHarvestTask
		best := -1.0
		for _, target := range targets {
			for _, proxy := range available {
				if r.deferredTargets[proxy+"\n"+target.host].After(now) {
					continue
				}
				state := memory[proxy]
				history := cookieProxyHistory(state)
				hits := history[target.host]
				if hits == 0 {
					continue
				}
				total := 0
				for _, n := range history {
					total += n
				}
				// Penalize repeated misses and use oldest-used proxy as a tie breaker.
				score := float64(hits) / float64(total+1) / float64(1+r.misses[proxy+"\n"+target.host])
				if chosen == nil || score > best || (score == best && r.lastProxy[proxy].Before(r.lastProxy[chosen.proxy])) {
					chosen = &cookieHarvestTask{kind, target.host, proxy}
					best = score
				}
			}
			if chosen != nil {
				return chosen
			}
		}
		return nil
	}
	weights := [3]int{p.ExplorePercent, p.FillPercent, p.RefreshPercent}
	index := 0
	for i, w := range weights {
		r.credits[i] += w
		if r.credits[i] > r.credits[index] {
			index = i
		}
	}
	r.credits[index] -= 100
	if index == 1 {
		if task := chooseTarget("fill", missing); task != nil {
			return task
		}
	}
	if index == 2 {
		if task := chooseTarget("refresh", refresh); task != nil {
			return task
		}
	}
	// Empty repair queues donate their slots to discovery. Every proxy retains
	// a revisit opportunity; initial learning is prioritized, never a global gate.
	bestProxy := ""
	bestPriority := -1
	bestScore := -1.0
	for _, proxy := range available {
		state := memory[proxy]
		priority := 0
		if state.Requests < settings.CookieProxyLearningAttempts && cookieProxySamples(state) < p.LearningSamples {
			priority = 1
		}
		if r.lastProxy[proxy].IsZero() || now.Sub(r.lastProxy[proxy]) >= time.Duration(p.ExplorationRevisitSeconds)*time.Second {
			priority = 2
		}
		// Recent-use fairness bounds starvation; successful discovery yield
		// favors productive exits between forced revisits.
		score := now.Sub(r.lastProxy[proxy]).Seconds() * (0.1 + float64(state.Stats.NewHosts+1)/float64(state.Stats.Attempts+5))
		better := priority == bestPriority && ((priority > 0 && r.lastProxy[proxy].Before(r.lastProxy[bestProxy])) || (priority == 0 && score > bestScore))
		if bestProxy == "" || priority > bestPriority || better {
			bestProxy = proxy
			bestPriority = priority
			bestScore = score
		}
	}
	return &cookieHarvestTask{kind: "explore", proxy: bestProxy}
}

func (s *OpenAIGatewayService) dispatchOpenAICookieHarvest(ctx context.Context, settings *OpenAICookieSettings) {
	r := &s.cookieHarvestRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if len(r.accounts) >= settings.CookieHarvestConcurrency {
		return
	}
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return
	}
	eligible := []*Account{}
	for i := range accounts {
		a := &accounts[i]
		if a.Status == StatusActive && isOpenAICodexTicketAccount(a) && isOpenAICookieCollector(a, settings) {
			eligible = append(eligible, a)
		}
	}
	if len(eligible) == 0 {
		if time.Since(r.lastNotice) >= time.Minute {
			r.lastNotice = time.Now()
			_ = s.settingService.appendOpenAICookieLog(ctx, OpenAICookieAcquisitionLog{ID: uuid.NewString(), Kind: "scheduler", CreatedAt: time.Now(), Model: settings.Model, Message: "当前没有符合采集分组/账号配置的启用账号；一分钟内相同状态不重复输出"})
		}
		return
	}
	proxies := s.settingService.resolveOpenAICookieProxyURLs(ctx, settings)
	memory := s.loadOpenAICookieProxyBindings(ctx)
	library, err := s.settingService.GetOpenAICodexCookieLibrary(ctx)
	if err != nil {
		return
	}
	for checked := 0; checked < len(eligible) && len(r.accounts) < settings.CookieHarvestConcurrency; checked++ {
		a := eligible[r.accountCursor%uint64(len(eligible))]
		r.accountCursor++
		now := time.Now()
		if r.accounts[a.ID] || r.nextAccount[a.ID].After(now) {
			continue
		}
		candidateProxies := proxies
		if len(candidateProxies) == 0 && a.Proxy != nil {
			candidateProxies = []string{a.Proxy.URL()}
		}
		task := r.plan(now, settings, candidateProxies, memory, library)
		if task == nil {
			continue
		}
		r.accounts[a.ID] = true
		r.jobs[a.ID] = task.kind
		r.proxies[task.proxy] = true
		r.lastProxy[task.proxy] = now
		if task.host != "" {
			r.targets[task.host] = true
		}
		cfg := s.codexTicketConfig()
		cfg.Models = []string{settings.Model}
		cfg.HarvestProxyURL = ""
		cfg.CookieHarvestProxyURLs = []string{task.proxy}
		cfg.CookieDynamicProxyFillHostCookie = false
		cfg.CookieHostWhitelist = settings.HostWhitelist
		cfg.CookieProxyLearningAttempts = settings.CookieProxyLearningAttempts
		probeCopy := *openAICookieProbeSettings(a, settings)
		probeSettings := &probeCopy
		probeSettings.harvestTask = task.kind
		probeSettings.harvestTarget = task.host
		probeSettings.harvestProxy = task.proxy
		r.wg.Add(1)
		go func(account *Account, task *cookieHarvestTask) {
			defer r.wg.Done()
			defer func() {
				r.mu.Lock()
				delete(r.accounts, account.ID)
				delete(r.jobs, account.ID)
				delete(r.proxies, task.proxy)
				delete(r.targets, task.host)
				r.nextAccount[account.ID] = time.Now().Add(time.Duration(settings.IntervalSeconds) * time.Second)
				r.mu.Unlock()
			}()
			s.probeOpenAICodexTicket(ctx, account, cfg, probeSettings)
		}(a, task)
	}
}

type CookieProxyHarvestStats struct {
	Attempts            int            `json:"attempts"`
	Successes           int            `json:"successes"`
	Failures            int            `json:"failures"`
	Unauthorized        int            `json:"unauthorized"`
	ConsecutiveFailures int            `json:"consecutive_failures"`
	NewHosts            int            `json:"new_hosts"`
	LastNewHostAt       time.Time      `json:"last_new_host_at,omitempty"`
	Tasks               map[string]int `json:"tasks,omitempty"`
	TargetHits          int            `json:"target_hits"`
}

func (s *OpenAIGatewayService) CookieHarvestRunning() map[string]int {
	r := &s.cookieHarvestRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := map[string]int{"total": len(r.accounts), "explore": 0, "fill": 0, "refresh": 0, "round_robin": 0}
	for _, kind := range r.jobs {
		counts[kind]++
	}
	return counts
}

func (s *OpenAIGatewayService) recordCookieHarvestOutcome(settings *OpenAICookieSettings, log *OpenAICookieAcquisitionLog) {
	if !log.harvestAttempted {
		return
	}
	p := settings.HarvestPolicy
	if p == nil {
		p = defaultCookieHarvestPolicy()
	}
	proxy := normalizeCookieProxyKey(settings.harvestProxy)
	now := time.Now()
	if log.TargetHost != "" && log.StatusCode != 401 {
		r := &s.cookieHarvestRuntime
		r.mu.Lock()
		r.init()
		key := proxy + "\n" + log.TargetHost
		if log.Success && log.Host == log.TargetHost {
			delete(r.misses, key)
			delete(r.deferredTargets, key)
		} else {
			r.misses[key]++
			if r.misses[key] >= p.TargetMissLimit {
				r.deferredTargets[key] = now.Add(time.Duration(p.TargetBackoffSeconds) * time.Second)
				r.misses[key] = 0
			}
		}
		r.mu.Unlock()
	}
	openAICookieProxyHostBindingsMu.Lock()
	defer openAICookieProxyHostBindingsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bindings := s.loadOpenAICookieProxyBindings(ctx)
	state, ok := bindings[proxy]
	if !ok {
		return
	} // A concurrent explicit reset must not resurrect old samples.
	stats := &state.Stats
	stats.Attempts++
	if stats.Tasks == nil {
		stats.Tasks = map[string]int{}
	}
	stats.Tasks[log.Task]++
	if log.Success {
		stats.Successes++
		stats.ConsecutiveFailures = 0
		state.BackoffUntil = time.Time{}
		if log.TargetHost != "" && log.Host == log.TargetHost {
			stats.TargetHits++
		}
	} else if log.StatusCode == 401 {
		stats.Unauthorized++
	} else {
		stats.Failures++
		stats.ConsecutiveFailures++
		if stats.ConsecutiveFailures >= p.FailureThreshold {
			state.BackoffUntil = now.Add(time.Duration(p.FailureBackoffSeconds) * time.Second)
			stats.ConsecutiveFailures = 0
		}
	}
	state.UpdatedAt = now
	bindings[proxy] = state
	if data, err := json.Marshal(bindings); err == nil {
		if err := s.settingService.settingRepo.Set(ctx, cookieProxyHostBindingsKey, string(data)); err != nil {
			slog.Warn("cookie harvest statistics save failed", "error", err)
		}
	}
}
