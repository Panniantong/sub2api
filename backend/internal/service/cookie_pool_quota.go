package service

import (
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
