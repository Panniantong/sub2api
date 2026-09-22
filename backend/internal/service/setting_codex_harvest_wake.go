package service

import (
	"context"
	"strings"
)

// The process-local harvester consumes one buffered signal. Concurrent writes
// coalesce, including writes during an active round. Never close this channel:
// an admin write after harvester shutdown must remain safe and non-blocking.
func (s *SettingService) codexHarvestWakeups() <-chan struct{} {
	if s == nil {
		return nil
	}
	s.codexHarvestWakeOnce.Do(func() { s.codexHarvestWake = make(chan struct{}, 1) })
	return s.codexHarvestWake
}

func (s *SettingService) NotifyCodexHarvest() {
	if s == nil {
		return
	}
	s.codexHarvestWakeups()
	select {
	case s.codexHarvestWake <- struct{}{}:
	default:
	}
}

func (s *SettingService) codexHarvestSettingsChanged(ctx context.Context, updates map[string]string) bool {
	keys := make([]string, 0)
	for key := range updates {
		if strings.HasPrefix(key, "openai_codex_ticket_") {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return false
	}
	before, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		// The caller only notifies after a successful write. A failed comparison
		// may trigger one extra check, never bypass a harvesting policy.
		return true
	}
	for _, key := range keys {
		if old, exists := before[key]; !exists || old != updates[key] {
			return true
		}
	}
	return false
}

// Invalidate all harvesting inputs before waking, even if reloading the rest
// of a partially updated settings document failed.
func (s *SettingService) notifyCodexHarvestAfterSettingsWrite() {
	s.InvalidateOpenAICodexTicketEnabledCache()
	s.InvalidateOpenAICodexTicketFailClosedCache()
	s.InvalidateOpenAICodexTicketModelsCache()
	s.InvalidateOpenAICodexTicketHarvestProxyCache()
	s.InvalidateOpenAICodexTicketHarvestScopeCache()
	s.NotifyCodexHarvest()
}
