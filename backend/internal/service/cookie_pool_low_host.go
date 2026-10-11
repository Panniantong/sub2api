package service

import (
	"sort"
	"time"
)

// This is an internal work limit, not another administrator setting. Slots
// reserve useful account capacity; only two accounts prepare at once per pool.
const cookieLowHostPreparingLimit = 2

func cookieLowHostEnabled(p CookieRotationPoolPolicy) bool {
	return p.Enabled && p.LowHostRotationEnabled && p.LowHostThreshold >= 2 && p.LowHostAccountSlots > 0
}

func cookieLowHostSlotsUsed(g *CookieRotationPoolGroup) int {
	n := 0
	for _, m := range g.Members {
		if m.LowHostForced && (m.State == "active" || m.State == "preparing") {
			n++
		}
	}
	return n
}

// Count exactly the candidates advertised as available by the account UI.
// A missing/expired library is unknown, never evidence of recovery/exhaustion.
func cookieLowHostCount(a *Account, cfg *OpenAICookieSettings, now time.Time) (int, bool) {
	if cfg.rotationCandidates == nil {
		return 0, false
	}
	entries, err := cfg.rotationCandidates()
	if err != nil {
		return 0, false
	}
	count, known := 0, false
	seen := map[string]bool{}
	for _, entry := range entries {
		host := normalizeOpenAICookieHost(entry.Host)
		if host == "" || seen[host] || !cookieHostInScope(host, cfg.HostWhitelist) || entry.Cookie == "" || !entry.ExpiresAt.After(now) {
			continue
		}
		seen[host], known = true, true
		if openAICookieRotationCandidate(a, entry, now) {
			count++
		}
	}
	return count, known
}

// Managed IDs are omitted from ordinary admission so neither shrink nor its
// MaxAccounts/MaxPreparingAccounts can cancel the independent forced slots.
func reconcileCookieLowHost(g *CookieRotationPoolGroup, p CookieRotationPoolPolicy, eligible map[int64]*Account, cfg *OpenAICookieSettings, now time.Time, claimed map[string]bool) map[int64]bool {
	managed := map[int64]bool{}
	enabled := cfg.Enabled && cfg.CookieRotationEnabled && cookieLowHostEnabled(p)
	if !enabled && cookieLowHostSlotsUsed(g) == 0 {
		return managed
	}
	ids := make([]int64, 0, len(eligible))
	for id := range eligible {
		ids = append(ids, id)
	}
	// Keep existing reservations first; deterministic order also handles a
	// reduced slot limit without allowing late validation results to re-admit.
	sort.Slice(ids, func(i, j int) bool {
		a, b := g.Members[ids[i]], g.Members[ids[j]]
		if (a.State == "active") != (b.State == "active") {
			return a.State == "active"
		}
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		return ids[i] < ids[j]
	})
	used, preparing := 0, 0
	for _, id := range ids {
		a, m := eligible[id], g.Members[id]
		if !m.LowHostForced {
			continue
		}
		_, reason := cookieHostBindingStatus(a, cfg, now)
		until, _ := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_validation_resume_at"))
		keep := enabled && used < p.LowHostAccountSlots && !claimed[cookiePoolIdentity(a)] && !until.After(now)
		switch m.State {
		case "active":
			// Let the existing rotation worker persist the expired Host cooldown
			// once before relinquishing the slot (no network validation involved).
			retiring := reason == "cookie_host_binding_expired" && !m.LowHostBindingUntil.IsZero() && !m.LowHostBindingUntil.After(now)
			keep = keep && ((reason == "" && m.LowHostBindingUntil.After(now)) || retiring)
		case "preparing":
			keep = keep && now.Sub(m.Since) < time.Duration(p.ReadyTimeoutSeconds)*time.Second && !cookieHostsExhausted(a, cfg, now)
		default:
			keep = false
		}
		if keep {
			managed[id] = true
			claimed[cookiePoolIdentity(a)] = true
			used++
			if m.State == "active" && reason == "" {
				g.ReadyCapacity += a.Concurrency
				g.LowHostReadyCapacity += a.Concurrency
			} else if m.State == "preparing" {
				preparing++
			}
			continue
		}
		wasPreparing := m.State == "preparing"
		m.LowHostForced = false
		if m.State == "active" && reason == "" {
			m.State, m.DrainUntil = "draining", m.LowHostBindingUntil
		} else {
			m.State = "waiting"
			if wasPreparing {
				m.RetryAt = now.Add(time.Duration(p.RetrySeconds) * time.Second)
			}
		}
		m.LowHostBindingUntil = time.Time{}
		g.Members[id] = m
	}
	for id, m := range g.Members {
		if eligible[id] == nil && m.LowHostForced {
			m.LowHostForced, m.LowHostBindingUntil = false, time.Time{}
			m.State, m.RetryAt = "waiting", now.Add(time.Duration(p.RetrySeconds)*time.Second)
			g.Members[id] = m
		}
	}
	if !enabled {
		return managed
	}
	ordinaryOwners := map[string]bool{}
	for id, a := range eligible {
		m := g.Members[id]
		if !m.LowHostForced && (m.State == "active" || m.State == "preparing" || m.State == "draining") {
			ordinaryOwners[cookiePoolIdentity(a)] = true
		}
	}
	// Round-robin across admissions, rather than repeatedly selecting the same
	// few low IDs. Successful accounts keep their slot for one binding only.
	sort.Slice(ids, func(i, j int) bool {
		if (ids[i] > g.LowHostCursor) != (ids[j] > g.LowHostCursor) {
			return ids[i] > g.LowHostCursor
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		a, m := eligible[id], g.Members[id]
		if managed[id] || claimed[cookiePoolIdentity(a)] || ordinaryOwners[cookiePoolIdentity(a)] || m.State == "active" || m.State == "draining" || m.State == "preparing" {
			continue
		}
		_, reason := cookieHostBindingStatus(a, cfg, now)
		if reason == "" {
			continue // Never replace an existing valid binding early.
		}
		count, known := cookieLowHostCount(a, cfg, now)
		if !known {
			continue
		}
		if cookieHostsExhausted(a, cfg, now) {
			m.State, m.KeepDegraded = "exhausted", true
			g.Members[id] = m
			continue
		}
		if m.State == "exhausted" {
			m.State, m.KeepDegraded, m.RetryAt = "waiting", true, time.Time{}
			g.Members[id] = m
		}
		if count == 0 || count >= p.LowHostThreshold || used >= p.LowHostAccountSlots || preparing >= cookieLowHostPreparingLimit {
			continue
		}
		until, _ := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_validation_resume_at"))
		if until.After(now) || m.RetryAt.After(now) {
			continue
		}
		m = CookieRotationPoolMember{State: "preparing", Since: now, LowHostForced: true, KeepDegraded: m.KeepDegraded}
		g.Members[id], g.LowHostCursor = m, id
		managed[id], claimed[cookiePoolIdentity(a)] = true, true
		used++
		preparing++
	}
	return managed
}
