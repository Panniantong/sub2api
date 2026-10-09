package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// GroupIDs share configuration, but each group owns an independent runtime pool.
type CookieRotationPoolPolicy struct {
	GroupID               int64   `json:"group_id"`
	GroupIDs              []int64 `json:"group_ids,omitempty"`
	DemandMode            string  `json:"demand_mode,omitempty"`
	ScaleDownSeconds      int     `json:"scale_down_seconds,omitempty"`
	Enabled               bool    `json:"enabled"`
	TargetConcurrency     int     `json:"target_concurrency"`
	RedundancyPercent     int     `json:"redundancy_percent"`
	RedundancyMode        string  `json:"redundancy_mode,omitempty"`
	RedundancyConcurrency int     `json:"redundancy_concurrency"`
	MinAccounts           int     `json:"min_accounts"`
	MaxAccounts           int     `json:"max_accounts"`
	ReadyTimeoutSeconds   int     `json:"ready_timeout_seconds"`
	RetrySeconds          int     `json:"retry_seconds"`
	ShortageRetrySeconds  int     `json:"shortage_retry_seconds,omitempty"`
	PreparationMultiplier int     `json:"preparation_multiplier,omitempty"`
	MaxPreparingAccounts  int     `json:"max_preparing_accounts,omitempty"`
	QuotaPriority         string  `json:"quota_priority,omitempty"`
}

type CookieRotationPoolMember struct {
	State      string    `json:"state"`
	Since      time.Time `json:"since"`
	RetryAt    time.Time `json:"retry_at,omitempty"`
	DrainUntil time.Time `json:"drain_until,omitempty"`
}
type CookieRotationPoolGroup struct {
	CurrentDemand     int                                `json:"current_demand"`
	DemandConcurrency int                                `json:"demand_concurrency"`
	DemandUnavailable bool                               `json:"demand_unavailable"`
	Cursor            int64                              `json:"cursor"`
	Members           map[int64]CookieRotationPoolMember `json:"members"`
	TargetCapacity    int                                `json:"target_capacity"`
	ReadyCapacity     int                                `json:"ready_capacity"`
	UpdatedAt         time.Time                          `json:"updated_at"`
}
type CookieRotationPoolSnapshot struct {
	Groups map[int64]*CookieRotationPoolGroup `json:"groups"`
}
type CookieRotationPoolRepository interface {
	CookieRotationPool(context.Context) (*CookieRotationPoolSnapshot, error)
	UpdateCookieRotationPool(context.Context, func(*CookieRotationPoolSnapshot) error) error
}

func cookieAccountInGroup(a *Account, id int64) bool {
	return isOpenAICookieRotationGroupAccount(a, &OpenAICookieSettings{RotationGroupIDs: []int64{id}})
}
func cookiePoolPolicy(a *Account, cfg *OpenAICookieSettings) *CookieRotationPoolPolicy {
	if a == nil || cfg == nil {
		return nil
	}
	for _, p := range cookiePoolPolicies(cfg) {
		if cookieAccountInGroup(a, p.GroupID) {
			return &p
		}
	}
	return nil
}

func cookiePoolPolicies(cfg *OpenAICookieSettings) []CookieRotationPoolPolicy {
	var result []CookieRotationPoolPolicy
	for _, p := range cfg.RotationPools {
		ids := p.GroupIDs
		if len(ids) == 0 {
			ids = []int64{p.GroupID}
		}
		for _, id := range ids {
			copy := p
			copy.GroupID, copy.GroupIDs = id, nil
			result = append(result, copy)
		}
	}
	return result
}

func cookiePoolTargetCapacity(p CookieRotationPoolPolicy, demand int) int {
	if p.RedundancyMode == "fixed" {
		return demand + p.RedundancyConcurrency
	}
	return (demand*(100+p.RedundancyPercent) + 99) / 100
}

func cookiePoolScaleDownSeconds(p CookieRotationPoolPolicy) int {
	if p.ScaleDownSeconds == 0 {
		return 60
	}
	return p.ScaleDownSeconds
}

func cookiePoolPreparationMultiplier(p CookieRotationPoolPolicy) int {
	if p.PreparationMultiplier == 0 {
		return 1
	}
	return p.PreparationMultiplier
}

func cookiePoolEffectiveRetryAt(m CookieRotationPoolMember, p CookieRotationPoolPolicy, shortage bool) time.Time {
	if shortage && m.State == "waiting" && p.ShortageRetrySeconds > 0 && p.ShortageRetrySeconds < p.RetrySeconds && !m.RetryAt.IsZero() {
		return m.RetryAt.Add(time.Duration(p.ShortageRetrySeconds-p.RetrySeconds) * time.Second)
	}
	return m.RetryAt
}

func cookiePoolMember(a *Account, cfg *OpenAICookieSettings) (CookieRotationPoolMember, string) {
	p := cookiePoolPolicy(a, cfg)
	if p == nil {
		return CookieRotationPoolMember{}, ""
	}
	if !p.Enabled || !cfg.Enabled || !cfg.CookieRotationEnabled || !a.IsSchedulable() {
		return CookieRotationPoolMember{}, "cookie_pool_disabled"
	}
	if cfg.rotationPool == nil {
		return CookieRotationPoolMember{}, "cookie_pool_waiting"
	}
	snapshot, err := cfg.rotationPool()
	if err != nil || snapshot == nil {
		return CookieRotationPoolMember{}, "cookie_pool_unavailable"
	}
	g := snapshot.Groups[p.GroupID]
	if g == nil {
		return CookieRotationPoolMember{}, "cookie_pool_waiting"
	}
	m := g.Members[a.ID]
	switch m.State {
	case "active":
		return m, ""
	case "draining":
		if m.DrainUntil.After(time.Now()) {
			return m, ""
		}
		return m, "cookie_pool_waiting"
	case "preparing":
		return m, "cookie_pool_preparing"
	case "exhausted":
		// A saved exhaustion marker is not proof of current exhaustion: cooldowns
		// expire and newly harvested Hosts can recover a mailbox before its turn.
		if !cookieHostsExhausted(a, cfg, time.Now()) {
			m.State = "waiting"
			return m, "cookie_pool_waiting"
		}
		return m, "cookie_pool_exhausted"
	default:
		return m, "cookie_pool_waiting"
	}
}

func cookiePoolMayRotate(a *Account, cfg *OpenAICookieSettings) bool {
	if a == nil || !a.IsSchedulable() {
		return false
	}
	if cookiePoolPolicy(a, cfg) == nil {
		return true
	}
	m, _ := cookiePoolMember(a, cfg)
	return m.State == "active" || m.State == "preparing"
}

// New admissions must not inherit a previous turn's 15-second retry delay.
// Existing members still respect their retry timer and quota reset deadline.
func cookiePoolRotationDue(a *Account, cfg *OpenAICookieSettings, now time.Time) bool {
	if !cookiePoolMayRotate(a, cfg) {
		return false
	}
	if until, err := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_validation_resume_at")); err == nil && until.After(now) {
		return false
	}
	if cookiePoolPolicy(a, cfg) != nil {
		m, _ := cookiePoolMember(a, cfg)
		if m.State == "preparing" {
			started, err := time.Parse(time.RFC3339Nano, a.GetExtraString(openAICodexCookieRotationStartedExtraKey))
			if err != nil || started.Before(m.Since) {
				return true
			}
		}
	}
	return openAICookieRotationDue(a, now)
}

// Publish readiness immediately after a successful binding, guarded by the same
// admission generation so a late validation cannot revive a revoked pool slot.
func (s *OpenAIGatewayService) publishCookiePoolReady(ctx context.Context, a *Account, cfg *OpenAICookieSettings, expected CookieRotationPoolMember, bind ...func() error) error {
	p := cookiePoolPolicy(a, cfg)
	if p == nil {
		if len(bind) > 0 {
			return bind[0]()
		}
		return nil
	}
	repo, ok := s.settingService.settingRepo.(CookieRotationPoolRepository)
	if !ok {
		return fmt.Errorf("cookie pool storage unavailable")
	}
	// Resolve library reads before taking the shared pool row lock.
	if _, reason := cookieHostBindingStatus(a, cfg, time.Now()); len(bind) == 0 && reason != "" {
		return fmt.Errorf("cookie binding unavailable: %s", reason)
	}
	admitted := false
	err := repo.UpdateCookieRotationPool(ctx, func(snapshot *CookieRotationPoolSnapshot) error {
		g := snapshot.Groups[p.GroupID]
		if g == nil {
			return fmt.Errorf("cookie pool admission revoked")
		}
		m := g.Members[a.ID]
		if !m.Since.Equal(expected.Since) || (m.State != "preparing" && m.State != "active") {
			return fmt.Errorf("cookie pool admission revoked")
		}
		if m.State == "preparing" {
			if time.Since(m.Since) >= time.Duration(p.ReadyTimeoutSeconds)*time.Second {
				return fmt.Errorf("cookie pool preparation timed out")
			}
			active := 0
			for _, member := range g.Members {
				if member.State == "active" {
					active++
				}
			}
			if (p.MaxAccounts > 0 && active >= p.MaxAccounts) || (active >= p.MinAccounts && g.ReadyCapacity >= g.TargetCapacity) {
				m.State = "standby"
				g.Members[a.ID] = m
				return nil
			}
		}
		// Serialize final binding with admission/shrink. A validation that loses
		// its slot must never renew an account selected to finish its old binding.
		if len(bind) > 0 {
			if err := bind[0](); err != nil {
				return err
			}
		}
		if m.State == "preparing" {
			g.ReadyCapacity += a.Concurrency
		}
		m.State = "active"
		g.Members[a.ID] = m
		g.UpdatedAt = time.Now()
		admitted = true
		return nil
	})
	if err == nil && !admitted {
		return fmt.Errorf("cookie pool target already covered")
	}
	return err
}

func (s *SettingService) validateCookieRotationPools(ctx context.Context, cfg *OpenAICookieSettings) error {
	seen := map[int64]bool{}
	for _, p := range cookiePoolPolicies(cfg) {
		if (p.RedundancyMode != "" && p.RedundancyMode != "percent" && p.RedundancyMode != "fixed") || p.RedundancyConcurrency < 0 || p.RedundancyConcurrency > 1000000 {
			return fmt.Errorf("冗余方式需为比例或固定并发，固定冗余需为 0–1000000")
		}
		if p.QuotaPriority != "" && p.QuotaPriority != "used_desc" && p.QuotaPriority != "remaining_desc" {
			return fmt.Errorf("账户池额度优先策略无效")
		}
		if p.ShortageRetrySeconds != 0 && (p.ShortageRetrySeconds < 5 || p.ShortageRetrySeconds > p.RetrySeconds) {
			return fmt.Errorf("缺口重试间隔需为 5 秒至普通重试间隔，0 表示沿用普通间隔")
		}
		if p.PreparationMultiplier < 0 || p.PreparationMultiplier > 5 || p.MaxPreparingAccounts < 0 || p.MaxPreparingAccounts > 10000 {
			return fmt.Errorf("准备倍率需为 1–5，准备账户上限需为 0–10000")
		}
		if p.GroupID <= 0 || seen[p.GroupID] {
			return fmt.Errorf("动态活跃池分组无效或重复")
		}
		seen[p.GroupID] = true
		selected := false
		for _, id := range cfg.RotationGroupIDs {
			selected = selected || id == p.GroupID
		}
		if !selected {
			return fmt.Errorf("动态活跃池必须属于已选择的 Host 轮换分组")
		}
		if (p.DemandMode != "" && p.DemandMode != "fixed" && p.DemandMode != "dynamic") || p.TargetConcurrency < 0 || (p.DemandMode != "dynamic" && p.TargetConcurrency < 1) || p.TargetConcurrency > 1000000 || p.RedundancyPercent < 0 || p.RedundancyPercent > 500 || p.MinAccounts < 1 || p.MinAccounts > 10000 || p.MaxAccounts < 0 || (p.MaxAccounts > 0 && p.MaxAccounts < p.MinAccounts) || p.MaxAccounts > 10000 || cookiePoolScaleDownSeconds(p) < 10 || cookiePoolScaleDownSeconds(p) > 3600 || p.ReadyTimeoutSeconds < 10 || p.ReadyTimeoutSeconds > 3600 || p.RetrySeconds < 10 || p.RetrySeconds > 86400 {
			return fmt.Errorf("动态活跃池容量、账号数或等待时间配置无效")
		}
		if s.defaultSubGroupReader == nil {
			return fmt.Errorf("分组校验服务不可用")
		}
		g, err := s.defaultSubGroupReader.GetByID(ctx, p.GroupID)
		if err != nil || g == nil || !g.IsActive() || g.Platform != PlatformOpenAI {
			return fmt.Errorf("动态活跃池必须选择启用的 OpenAI 分组")
		}
	}
	return nil
}

func cookiePoolIdentity(a *Account) string {
	if email := CookieAccountEmail(a); email != "" {
		return email
	}
	return "account:" + strconv.FormatInt(a.ID, 10)
}

// Retain incumbents, fill in account-ID order after the cursor, and move recovered
// accounts behind unused reserves. Preparing slots count toward planned capacity
// only; requests cannot use them until validation succeeds.
func reconcileCookiePool(g *CookieRotationPoolGroup, p CookieRotationPoolPolicy, accounts []Account, cfg *OpenAICookieSettings, now time.Time, claimed map[string]bool) {
	if g.Members == nil {
		g.Members = map[int64]CookieRotationPoolMember{}
	}
	if p.DemandMode != "dynamic" {
		g.DemandConcurrency = p.TargetConcurrency
		g.DemandUnavailable = false
	}
	if !g.DemandUnavailable {
		g.TargetCapacity = cookiePoolTargetCapacity(p, g.DemandConcurrency)
	}
	g.ReadyCapacity = 0
	g.UpdatedAt = now
	eligible := map[int64]*Account{}
	for i := range accounts {
		a := &accounts[i]
		if owner := cookiePoolPolicy(a, cfg); owner != nil && owner.GroupID != p.GroupID {
			continue
		}
		if cookieAccountInGroup(a, p.GroupID) && isOpenAICodexTicketAccount(a) && a.IsSchedulable() && !cookieHostMonitorOwns(a, cfg) && a.Concurrency > 0 {
			eligible[a.ID] = a
		}
	}
	ids := make([]int64, 0, len(eligible))
	for id := range eligible {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		order := func(id int64) int {
			switch g.Members[id].State {
			case "active":
				return 0
			case "draining":
				return 1
			default:
				return 2
			}
		}
		if order(ids[i]) != order(ids[j]) {
			return order(ids[i]) < order(ids[j])
		}
		if cmp := cookiePoolQuotaCompare(eligible[ids[i]], eligible[ids[j]], p.QuotaPriority, now); cmp != 0 {
			return cmp < 0
		}
		return ids[i] < ids[j]
	})
	// Ready incumbents are considered before preparation, regardless of account ID.
	// Shrink by finishing the existing binding, never by renewing it or cutting it short.
	capacity, count := 0, 0
	pending := []int64{}
	for _, id := range ids {
		a := eligible[id]
		m := g.Members[id]
		if m.State == "exhausted" {
			if exhausted, known := cookieHostExhaustionStatus(a, cfg, now); known && !exhausted {
				m.State = "waiting"
				g.Members[id] = m
			}
		}
		if m.State != "active" && m.State != "preparing" && m.State != "draining" {
			continue
		}
		if !p.Enabled || claimed[cookiePoolIdentity(a)] {
			m.State = "standby"
			g.Members[id] = m
			continue
		}
		_, reason := cookieHostBindingStatus(a, cfg, now)
		if m.State == "draining" && (!m.DrainUntil.After(now) || reason != "") {
			m.State, m.RetryAt = "standby", now.Add(time.Duration(p.RetrySeconds)*time.Second)
			g.Members[id] = m
			continue
		}
		limited, _ := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_validation_resume_at"))
		exhausted := reason != "" && cookieHostsExhausted(a, cfg, now)
		if limited.After(now) || exhausted || (reason != "" && m.State == "preparing" && now.Sub(m.Since) >= time.Duration(p.ReadyTimeoutSeconds)*time.Second) {
			m.State = "waiting"
			if exhausted {
				m.State = "exhausted"
			}
			m.RetryAt = now.Add(time.Duration(p.RetrySeconds) * time.Second)
			if limited.After(m.RetryAt) {
				m.RetryAt = limited
			}
			g.Members[id] = m
			continue
		}
		if reason != "" {
			if m.State == "active" {
				m.State, m.Since = "preparing", now
			}
			g.Members[id] = m
			pending = append(pending, id)
			continue
		}
		if (p.MaxAccounts > 0 && count >= p.MaxAccounts) || (count >= p.MinAccounts && capacity >= g.TargetCapacity) {
			if m.State == "preparing" {
				// A cached binding is not a newly validated admission.
				m.State = "standby"
			} else {
				if m.State != "draining" {
					m.DrainUntil, _ = time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_host_binding_expires_at"))
				}
				m.State = "draining"
				g.ReadyCapacity += a.Concurrency
				claimed[cookiePoolIdentity(a)] = true
			}
		} else {
			m.State, m.DrainUntil = "active", time.Time{}
			g.ReadyCapacity += a.Concurrency
			capacity += a.Concurrency
			count++
			claimed[cookiePoolIdentity(a)] = true
		}
		g.Members[id] = m
	}
	for id, m := range g.Members {
		if eligible[id] == nil && (m.State == "active" || m.State == "preparing" || m.State == "draining") {
			m.State, m.RetryAt = "waiting", now.Add(time.Duration(p.RetrySeconds)*time.Second)
			g.Members[id] = m
		}
	}
	if !p.Enabled {
		return
	}
	// Incumbent preparation first, then unused reserves, then recovered accounts.
	sort.SliceStable(ids, func(i, j int) bool {
		a, b := g.Members[ids[i]], g.Members[ids[j]]
		if (a.State == "preparing") != (b.State == "preparing") {
			return a.State == "preparing"
		}
		if cmp := cookiePoolQuotaCompare(eligible[ids[i]], eligible[ids[j]], p.QuotaPriority, now); cmp != 0 {
			return cmp < 0
		}
		if a.RetryAt.IsZero() != b.RetryAt.IsZero() {
			return a.RetryAt.IsZero()
		}
		if (ids[i] > g.Cursor) != (ids[j] > g.Cursor) {
			return ids[i] > g.Cursor
		}
		return ids[i] < ids[j]
	})
	candidates := []int64{}
	candidateIdentities := map[string]bool{}
	for _, id := range ids {
		a, m := eligible[id], g.Members[id]
		identity := cookiePoolIdentity(a)
		if claimed[identity] || candidateIdentities[identity] || m.State == "active" || m.State == "draining" || cookiePoolEffectiveRetryAt(m, p, g.ReadyCapacity < g.TargetCapacity || count < p.MinAccounts).After(now) {
			continue
		}
		if until, err := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_validation_resume_at")); err == nil && until.After(now) {
			continue
		}
		if !m.RetryAt.IsZero() && cookieHostsExhausted(a, cfg, now) {
			continue
		}
		candidateIdentities[identity] = true
		candidates = append(candidates, id)
	}
	needed := 0
	for _, id := range candidates {
		if (p.MaxAccounts > 0 && count+needed >= p.MaxAccounts) || (count+needed >= p.MinAccounts && capacity >= g.TargetCapacity) {
			break
		}
		capacity += eligible[id].Concurrency
		needed++
	}
	budget := needed * cookiePoolPreparationMultiplier(p)
	if p.MaxPreparingAccounts > 0 && budget > p.MaxPreparingAccounts {
		budget = p.MaxPreparingAccounts
	}
	selected := map[int64]bool{}
	for _, id := range candidates {
		if len(selected) >= budget {
			break
		}
		selected[id] = true
		if g.Members[id].State != "preparing" {
			g.Members[id] = CookieRotationPoolMember{State: "preparing", Since: now}
			g.Cursor = id
		}
		claimed[cookiePoolIdentity(eligible[id])] = true
	}
	for _, id := range pending {
		if !selected[id] {
			m := g.Members[id]
			m.State = "standby"
			g.Members[id] = m
		}
	}
}

func (s *OpenAIGatewayService) reconcileCookieRotationPools(ctx context.Context, cfg *OpenAICookieSettings, accounts []Account) error {
	if len(cfg.RotationPools) == 0 {
		return nil
	}
	repo, ok := s.settingService.settingRepo.(CookieRotationPoolRepository)
	if !ok {
		return fmt.Errorf("cookie rotation pool storage unavailable")
	}
	for i := range accounts {
		if cookiePoolPolicy(&accounts[i], cfg) == nil {
			continue
		}
		a, err := s.settingService.cookieEmailAccount(ctx, &accounts[i])
		if err != nil {
			return err
		}
		accounts[i] = *a
		if cookiePoolPolicy(a, cfg) != nil && s.rateLimitService != nil && s.rateLimitService.ApplyAccountSchedulingThreshold(ctx, a) {
			accounts[i].Schedulable = false
		}
	}
	// Accounts above already contain mailbox cooldowns. Never perform per-account
	// mailbox/library reads while holding the pool's database row lock.
	view := *cfg
	view.emailAccount = nil
	entries, err := s.settingService.cookieSchedulingCandidates(ctx)
	if err != nil {
		return err
	}
	view.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return entries, nil }
	policies := cookiePoolPolicies(cfg)
	demand := map[int64]CookieGroupDemand{}
	for _, p := range policies {
		if p.Enabled {
			value, err := s.concurrencyService.cookieGroupDemand(ctx, p.GroupID, cookiePoolScaleDownSeconds(p))
			if err == nil {
				demand[p.GroupID] = value
			}
		}
	}
	return repo.UpdateCookieRotationPool(ctx, func(snapshot *CookieRotationPoolSnapshot) error {
		if snapshot.Groups == nil {
			snapshot.Groups = map[int64]*CookieRotationPoolGroup{}
		}
		claimed := map[string]bool{}
		for _, p := range policies {
			g := snapshot.Groups[p.GroupID]
			if g == nil {
				g = &CookieRotationPoolGroup{}
				snapshot.Groups[p.GroupID] = g
			}
			if p.DemandMode == "dynamic" {
				value, ok := demand[p.GroupID]
				g.DemandUnavailable = !ok
				if ok {
					g.CurrentDemand, g.DemandConcurrency = value.Current, value.Peak
				}
			} else if value, ok := demand[p.GroupID]; ok {
				g.CurrentDemand = value.Current
			}
			reconcileCookiePool(g, p, accounts, &view, time.Now(), claimed)
		}
		return nil
	})
}

func (s *SettingService) attachCookiePoolReaders(ctx context.Context, cfg *OpenAICookieSettings) {
	cfg.emailAccount = func(a *Account) (*Account, error) { return s.cookieEmailAccount(ctx, a) }
	if repo, ok := s.settingRepo.(CookieRotationPoolRepository); ok {
		// One immutable pool snapshot per settings read, rather than N SQL reads
		// per candidate scan. Dispatch/validation acquire a new settings view.
		var once sync.Once
		var snapshot *CookieRotationPoolSnapshot
		var err error
		cfg.rotationPool = func() (*CookieRotationPoolSnapshot, error) {
			once.Do(func() { snapshot, err = repo.CookieRotationPool(ctx) })
			return snapshot, err
		}
	}
}
