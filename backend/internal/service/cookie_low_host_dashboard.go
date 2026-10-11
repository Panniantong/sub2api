package service

import "time"

// Current display counters, never persisted or used for admission decisions.
type CookieLowHostDashboard struct {
	SlotsUsed          int `json:"slots_used"`
	Preparing          int `json:"preparing"`
	Active             int `json:"active"`
	Retiring           int `json:"retiring"`
	Waiting            int `json:"waiting"`
	Blocked            int `json:"blocked"`
	ReadyCapacity      int `json:"ready_capacity"`
	DegradedContinuing int `json:"degraded_continuing"`
}

type CookieLowHostAccount struct {
	State              string     `json:"state"`
	SlotOccupied       bool       `json:"slot_occupied"`
	DegradedContinuing bool       `json:"degraded_continuing"`
	BindingUntil       *time.Time `json:"binding_until,omitempty"`
}

func (d *CookieLowHostDashboard) add(a CookieLowHostAccount, concurrency int) {
	if a.SlotOccupied {
		d.SlotsUsed++
	}
	if a.DegradedContinuing {
		d.DegradedContinuing++
	}
	switch a.State {
	case "active":
		d.Active++
		d.ReadyCapacity += concurrency
	case "preparing":
		d.Preparing++
	case "retiring":
		d.Retiring++
	case "waiting":
		d.Waiting++
	case "blocked":
		d.Blocked++
	}
}

func cookieDashboardLowHostAccount(a *Account, m CookieRotationPoolMember, row *CookiePoolDashboardGroup, cfg *OpenAICookieSettings, healthy, degraded bool, now time.Time) CookieLowHostAccount {
	d := CookieLowHostAccount{SlotOccupied: m.LowHostForced && (m.State == "active" || m.State == "preparing")}
	if m.KeepDegraded && degraded {
		exhausted, known := cookieHostExhaustionStatus(a, cfg, now)
		d.DegradedContinuing = known && !exhausted
	}
	if d.SlotOccupied && !m.LowHostBindingUntil.IsZero() {
		at := m.LowHostBindingUntil
		d.BindingUntil = &at
	}
	if !d.SlotOccupied {
		if !cookieLowHostEnabled(row.Policy) {
			return d
		}
		// Candidates exclude normal active/preparing members and healthy standby
		// bindings: they are not waiting for the independent forced queue.
		if m.State == "active" || m.State == "preparing" || m.State == "draining" {
			return d
		}
		if _, reason := cookieHostBindingStatus(a, cfg, now); reason == "" {
			return d
		}
		count, known := cookieLowHostCount(a, cfg, now)
		if !known || count == 0 || count >= row.Policy.LowHostThreshold {
			return d
		}
	}
	until, _ := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_validation_resume_at"))
	if !row.Enabled || !cookieLowHostEnabled(row.Policy) || !a.IsSchedulable() || cookieHostMonitorOwns(a, cfg) || until.After(now) || a.Concurrency <= 0 {
		d.State = "blocked"
		return d
	}
	if d.SlotOccupied {
		switch m.State {
		case "active":
			if healthy && m.LowHostBindingUntil.After(now) {
				d.State = "active"
			} else {
				d.State = "retiring"
			}
		case "preparing":
			if now.Sub(m.Since) >= time.Duration(row.Policy.ReadyTimeoutSeconds)*time.Second {
				d.State = "retiring"
			} else {
				d.State = "preparing"
			}
		}
	} else if m.State != "exhausted" && m.RetryAt.After(now) {
		d.State = "blocked"
	} else {
		d.State = "waiting"
	}
	return d
}
