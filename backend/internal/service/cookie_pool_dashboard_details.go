package service

import (
	"sort"
	"time"
)

type cookiePoolValidationLive struct {
	Host, Stage string
	UpdatedAt   time.Time
}
type CookiePoolDashboardHost struct {
	Host          string     `json:"host"`
	State         string     `json:"state"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
}
type CookiePoolDashboardAccount struct {
	ID                  int64                     `json:"id"`
	Name                string                    `json:"name"`
	Email               string                    `json:"email"`
	State               string                    `json:"state"`
	Concurrency         int                       `json:"concurrency"`
	Reason              string                    `json:"reason"`
	ResumeAt            *time.Time                `json:"resume_at,omitempty"`
	PreparingSince      *time.Time                `json:"preparing_since,omitempty"`
	PreparingDeadline   *time.Time                `json:"preparing_deadline,omitempty"`
	BoundHost           string                    `json:"bound_host"`
	BindingExpiresAt    *time.Time                `json:"binding_expires_at,omitempty"`
	ValidationHost      string                    `json:"validation_host"`
	ValidationStage     string                    `json:"validation_stage"`
	ValidationUpdatedAt *time.Time                `json:"validation_updated_at,omitempty"`
	AvailableHosts      int                       `json:"available_hosts"`
	Hosts               []CookiePoolDashboardHost `json:"hosts"`
}

func cookiePoolAccountWait(a *Account, m CookieRotationPoolMember, row *CookiePoolDashboardGroup, now time.Time) (string, *time.Time) {
	if !row.Enabled {
		return "策略未启用", nil
	}
	if !a.IsActive() || !a.Schedulable {
		return "账户已禁用或异常", nil
	}
	if a.AutoPauseOnExpired && a.ExpiresAt != nil && !a.ExpiresAt.After(now) {
		return "账户已过期", nil
	}
	var until *time.Time
	reason := ""
	add := func(label string, at *time.Time) {
		if at != nil && at.After(now) && (until == nil || at.After(*until)) {
			reason, until = label, at
		}
	}
	add("额度 / 限流暂停", a.RateLimitResetAt)
	add("临时禁用", a.TempUnschedulableUntil)
	add("过载退避", a.OverloadUntil)
	if at, err := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_validation_resume_at")); err == nil {
		add("验证额度耗尽", &at)
	}
	if until != nil {
		return reason, until
	}
	if !a.IsSchedulable() {
		return "账户不可调度", nil
	}
	if m.State == "waiting" {
		at := cookiePoolEffectiveRetryAt(m, row.Policy, row.shortage)
		if at.After(now) {
			return "准备失败后等待重试", &at
		}
		return "等待下一轮补位检查", nil
	}
	if m.State == "exhausted" {
		return "邮箱 Host 全部冷却", nil
	}
	if m.State == "active" || m.State == "draining" {
		return "等待有效绑定 / 状态更新", nil
	}
	if m.State == "preparing" {
		return "正在准备", nil
	}
	return "备用排队", nil
}

func (s *OpenAIGatewayService) cookiePoolDashboardAccount(a *Account, m CookieRotationPoolMember, state, reason string, until *time.Time, cfg *OpenAICookieSettings, entries map[string]OpenAICodexCookieLibraryEntry, now time.Time) CookiePoolDashboardAccount {
	d := CookiePoolDashboardAccount{ID: a.ID, Name: a.Name, Email: CookieAccountEmail(a), State: state, Reason: reason, ResumeAt: until, Concurrency: a.Concurrency, BoundHost: openAICodexCookieHostFromAccount(a), Hosts: []CookiePoolDashboardHost{}}
	if state == "preparing" {
		at := m.Since
		d.PreparingSince = &at
		if p := cookiePoolPolicy(a, cfg); p != nil {
			deadline := at.Add(time.Duration(p.ReadyTimeoutSeconds) * time.Second)
			d.PreparingDeadline = &deadline
		}
	}
	if at, err := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_host_binding_expires_at")); err == nil && d.BoundHost != "" {
		d.BindingExpiresAt = &at
	}
	if value, ok := s.openaiCookieRotationRunning.Load(a.ID); ok {
		if live, ok := value.(cookiePoolValidationLive); ok {
			d.ValidationHost, d.ValidationStage = live.Host, live.Stage
			at := live.UpdatedAt
			d.ValidationUpdatedAt = &at
		}
	}
	for host, e := range entries {
		h := CookiePoolDashboardHost{Host: host, State: "available", ExpiresAt: e.ExpiresAt}
		cooldown := openAICodexCookieHostCooldownUntil(a, host)
		switch {
		case !cookieHostInScope(host, cfg.HostWhitelist):
			h.State = "out_of_scope"
		case e.Cookie == "" || !e.ExpiresAt.After(now):
			h.State = "expired"
		case cooldown.After(now):
			h.State = "cooldown"
			h.CooldownUntil = &cooldown
		case host == normalizeOpenAICookieHost(d.BoundHost):
			h.State = "bound"
		default:
			d.AvailableHosts++
		}
		d.Hosts = append(d.Hosts, h)
	}
	sort.Slice(d.Hosts, func(i, j int) bool { return d.Hosts[i].Host < d.Hosts[j].Host })
	return d
}
