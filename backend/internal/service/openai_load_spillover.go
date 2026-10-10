package service

import (
	"context"
	"time"
)

// Ordinary affinity yields only for a substantial load difference. Keep the
// durable binding, and keep using the owner if no alternative can acquire a slot.
// These are admission heuristics, not a prediction of upstream first-token time.
func (s *OpenAIGatewayService) selectOpenAILessLoadedAlternative(ctx context.Context, req OpenAIAccountScheduleRequest, owner *Account) *AccountSelectionResult {
	if s == nil || s.concurrencyService == nil || s.concurrencyService.cache == nil || owner == nil || owner.Platform != PlatformOpenAI || owner.Concurrency <= 0 ||
		req.DisableStickyEscape || req.PreserveStickyBinding || req.PreviousResponseID != "" || req.GuardianParentAccountID > 0 {
		return nil
	}
	// Optional balancing must not add a multi-second Redis wait to affinity.
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	loads, err := s.concurrencyService.cache.GetAccountsLoadBatch(ctx, []AccountWithConcurrency{{ID: owner.ID, MaxConcurrency: owner.Concurrency}})
	if err != nil || loads[owner.ID] == nil {
		return nil
	}
	utilization := float64(loads[owner.ID].CurrentConcurrency) / float64(owner.Concurrency)
	if utilization < 0.70 {
		return nil
	}
	ceiling := utilization - 0.30 + 1e-9
	req.spilloverMaxUtilization = &ceiling
	req.ExcludedIDs = cloneExcludedAccountIDs(req.ExcludedIDs)
	if req.ExcludedIDs == nil {
		req.ExcludedIDs = make(map[int64]struct{})
	}
	req.ExcludedIDs[owner.ID] = struct{}{}
	req.PreserveStickyBinding = true
	scheduler := &defaultOpenAIAccountScheduler{service: s, stats: newOpenAIAccountRuntimeStats()}
	selection, _, _, _, err := scheduler.selectByLoadBalance(ctx, req)
	if err != nil || selection == nil {
		return nil
	}
	if ctx.Err() != nil {
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		return nil
	}
	if !selection.Acquired {
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		return nil
	}
	return selection
}
