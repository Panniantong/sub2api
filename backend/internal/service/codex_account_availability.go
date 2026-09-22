package service

import "time"

// Account availability matches groupAccountAvailableSQL / ListSchedulableByGroupID:
// status=active + schedulable + not expired, then the latest of the three
// rate-limit / overload / temp-unschedulable windows.
const (
	CodexAccountAvailabilityAvailable         = "available"
	CodexAccountAvailabilityRateLimited       = "rate_limited"
	CodexAccountAvailabilityOverload          = "overload"
	CodexAccountAvailabilityTempUnschedulable = "temp_unschedulable"
	CodexAccountAvailabilityError             = "error"
	CodexAccountAvailabilityDisabled          = "disabled"
	CodexAccountAvailabilityExpired           = "expired"
)

func ResolveCodexAccountAvailability(account *Account, now time.Time) (string, *time.Time) {
	if account == nil {
		return CodexAccountAvailabilityError, nil
	}
	if account.Status != StatusActive {
		return CodexAccountAvailabilityError, nil
	}
	if !account.Schedulable {
		return CodexAccountAvailabilityDisabled, nil
	}
	if account.ExpiresAt != nil && !account.ExpiresAt.After(now) && account.AutoPauseOnExpired {
		return CodexAccountAvailabilityExpired, nil
	}

	kind := ""
	var recoverAt *time.Time
	consider := func(window *time.Time, candidate string) {
		if window == nil || !window.After(now) {
			return
		}
		if recoverAt == nil || window.After(*recoverAt) {
			recoverAt = window
			kind = candidate
		}
	}
	consider(account.RateLimitResetAt, CodexAccountAvailabilityRateLimited)
	consider(account.OverloadUntil, CodexAccountAvailabilityOverload)
	consider(account.TempUnschedulableUntil, CodexAccountAvailabilityTempUnschedulable)
	if recoverAt == nil {
		return CodexAccountAvailabilityAvailable, nil
	}
	return kind, recoverAt
}
