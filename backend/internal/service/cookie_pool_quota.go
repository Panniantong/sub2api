package service

import (
	"context"
	"sort"
	"time"
)

// Use the tighter of the two current quota windows. Expired/stale measurements
// are unknown, not an assumed empty/full quota. Existing quota gates still apply.
func cookiePoolQuotaUsed(a *Account, now time.Time) (float64, bool) {
	if a == nil || !isOpenAICodexTicketAccount(a) {
		return 0, false
	}
	used, known := 0.0, false
	for _, window := range []string{"5h", "7d"} {
		if c := openAIThresholdCandidate(a.Extra, window, now); c != nil {
			if !known || c.usedPercent > used {
				used = c.usedPercent
			}
			known = true
		}
	}
	return used, known
}

func cookiePoolQuotaCompare(a, b *Account, mode string, now time.Time) int {
	if mode != "used_desc" && mode != "remaining_desc" {
		return 0
	}
	left, lk := cookiePoolQuotaUsed(a, now)
	right, rk := cookiePoolQuotaUsed(b, now)
	if lk != rk {
		if lk {
			return -1
		}
		return 1
	}
	if !lk || left == right {
		return 0
	}
	if (mode == "used_desc" && left > right) || (mode == "remaining_desc" && left < right) {
		return -1
	}
	return 1
}

// Reorder only positions belonging to the same pool and compact-support tier.
// Recent session accounts precede quota priority; untouched/cold accounts keep
// quota ordering. Eligibility and slot acquisition still run after this hint.
func cookiePoolQuotaOrder[T any](ctx context.Context, s *OpenAIGatewayService, items []T, account func(T) *Account, compact bool, sessionHash ...string) {
	if len(items) < 2 || s == nil || s.settingService == nil {
		return
	}
	cfg, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil || !cfg.Enabled || !cfg.CookieRotationEnabled {
		return
	}
	var ranks map[int64]int
	if len(sessionHash) > 0 {
		ranks = cookiePoolAffinityRanks(ctx, s, cfg, sessionHash[0])
	}
	type bucketKey struct {
		group int64
		tier  int
	}
	buckets := map[bucketKey][]int{}
	modes := map[bucketKey]string{}
	for i, item := range items {
		a := account(item)
		p := cookiePoolPolicy(a, cfg)
		if p == nil || !p.Enabled || (p.QuotaPriority == "" && len(ranks) == 0) {
			continue
		}
		key := bucketKey{group: p.GroupID}
		if compact {
			key.tier = openAICompactSupportTier(a)
		}
		buckets[key] = append(buckets[key], i)
		modes[key] = p.QuotaPriority
	}
	now := time.Now()
	for key, positions := range buckets {
		ordered := make([]T, len(positions))
		for i, pos := range positions {
			ordered[i] = items[pos]
		}
		sort.SliceStable(ordered, func(i, j int) bool {
			if left, right := ranks[account(ordered[i]).ID], ranks[account(ordered[j]).ID]; left != right {
				return left > right
			}
			return cookiePoolQuotaCompare(account(ordered[i]), account(ordered[j]), modes[key], now) < 0
		})
		for i, pos := range positions {
			items[pos] = ordered[i]
		}
	}
}
