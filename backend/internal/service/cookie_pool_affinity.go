package service

import (
	"context"
	"time"
)

// Affinity is a bounded hint, never an eligibility override. Keep it separate
// from the primary binding so a temporary Host rotation does not erase history.
type CookiePoolAffinityCache interface {
	RememberCookiePoolAffinity(context.Context, int64, string, int64) error
	ReadCookiePoolAffinity(context.Context, int64, string) ([]int64, error)
}

func cookiePoolAffinityGroup(cfg *OpenAICookieSettings, groupID int64) bool {
	if cfg == nil || !cfg.Enabled || !cfg.CookieRotationEnabled || groupID <= 0 {
		return false
	}
	for _, p := range cookiePoolPolicies(cfg) {
		if p.Enabled && p.GroupID == groupID {
			return true
		}
	}
	return false
}

func (s *OpenAIGatewayService) rememberCookiePoolAffinity(ctx context.Context, groupID *int64, sessionHash string, accountID int64) {
	if s == nil || s.settingService == nil || sessionHash == "" {
		return
	}
	cache, ok := s.cache.(CookiePoolAffinityCache)
	if !ok {
		return
	}
	cfg, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil || !cookiePoolAffinityGroup(cfg, derefGroupID(groupID)) {
		return
	}
	// Redis failure must not add a long wait or fail an otherwise valid request.
	callCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = cache.RememberCookiePoolAffinity(callCtx, derefGroupID(groupID), s.openAISessionCacheKey(sessionHash), accountID)
}

func cookiePoolAffinityRanks(ctx context.Context, s *OpenAIGatewayService, cfg *OpenAICookieSettings, sessionHash string) map[int64]int {
	group, _ := ctx.Value(cookieSchedulingGroupKey{}).(int64)
	if sessionHash == "" || !cookiePoolAffinityGroup(cfg, group) {
		return nil
	}
	cache, ok := s.cache.(CookiePoolAffinityCache)
	if !ok {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	ids, err := cache.ReadCookiePoolAffinity(callCtx, group, s.openAISessionCacheKey(sessionHash))
	if err != nil {
		return nil
	}
	ranks := make(map[int64]int, len(ids))
	for i, id := range ids {
		ranks[id] = len(ids) - i
	}
	return ranks
}
