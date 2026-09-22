package service

import (
	"context"
	"encoding/json"
	"time"
)

type CodexProbeSummary struct {
	Result      string     `json:"result"`
	HTTPStatus  int        `json:"http_status,omitempty"`
	CheckedAt   time.Time  `json:"checked_at"`
	NextProbeAt *time.Time `json:"next_probe_at,omitempty"`
}

func codexProbeSummaryKey(model string) string { return openAICodexTicketExtraKey(model) + ":probe" }

func (s *OpenAIGatewayService) recordCodexProbe(ctx context.Context, account *Account, model, result string, status int) {
	if s.accountRepo == nil || ctx.Err() != nil {
		return
	}
	summary := CodexProbeSummary{Result: result, HTTPStatus: status, CheckedAt: time.Now()}
	if value, ok := s.openaiCodexTicketProbeCooldown.Load(openAICodexTicketKey(account.ID, model)); ok {
		if until, ok := value.(time.Time); ok {
			summary.NextProbeAt = &until
		}
	}
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_ = s.accountRepo.UpdateExtra(writeCtx, account.ID, map[string]any{codexProbeSummaryKey(model): summary})
}

func readCodexProbe(account *Account, model string) *CodexProbeSummary {
	if account == nil || account.Extra == nil {
		return nil
	}
	raw := account.Extra[codexProbeSummaryKey(model)]
	if raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var s CodexProbeSummary
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}
