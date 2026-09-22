package service

import "context"

const SettingKeyOpenAICodexTicketStrategy = "openai_codex_ticket_strategy"
const SettingKeyOpenAICodexTicketStrict = "openai_codex_ticket_strict_response"

func (s *SettingService) CodexTicketStrictResponse(ctx context.Context) bool {
	if s == nil || s.settingRepo == nil {
		return false
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAICodexTicketStrict)
	return err == nil && value == "true"
}

func NormalizeCodexTicketStrategy(value string) string {
	if value == "fixed" {
		return "fixed"
	}
	return "standby"
}

// Read once per harvest round; missing settings retain early refresh.
func (s *SettingService) GetCodexTicketStrategy(ctx context.Context) string {
	if s == nil || s.settingRepo == nil {
		return "standby"
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAICodexTicketStrategy)
	if err != nil {
		return "standby"
	}
	return NormalizeCodexTicketStrategy(value)
}
