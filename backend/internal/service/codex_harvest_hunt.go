package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// Singleflight owns the entire node hunt, not each individual HTTP attempt.
func (s *OpenAIGatewayService) probeOnceOpenAICodexTicket(ctx context.Context, account *Account, model string) {
	if s == nil || !isOpenAICodexTicketAccount(account) || s.httpUpstream == nil {
		return
	}
	key := openAICodexTicketKey(account.ID, model)
	_, _, _ = s.openaiCodexTicketFlight.Do(key, func() (any, error) {
		s.huntCodexHarvestTicket(ctx, account, model)
		return nil, nil
	})
}

func (s *OpenAIGatewayService) huntCodexHarvestTicket(ctx context.Context, account *Account, model string) {
	if !s.codexHarvestRunMu.TryRLock() {
		return
	}
	defer s.codexHarvestRunMu.RUnlock()

	controls, _ := s.harvestControls(ctx)
	round, _ := ctx.Value(codexHarvestRoundKey{}).(*codexHarvestRound)
	if round == nil {
		round = &codexHarvestRound{limit: controls.Speed.MaxRequestsPerRound}
	}
	tried := map[string]bool{}
	attempts := 0
	var last codexHarvestProbeResult
	defer func() {
		if s.codexHarvest != nil {
			s.codexHarvest.setRuntime(func(r *CodexHarvestRuntime) { r.CurrentNode = "" })
		}
	}()
	for n := 0; ; n++ {
		current, configured := s.harvestControls(ctx)
		controls = current
		maxAttempts := 1
		if controls.NodeMemoryEnabled {
			maxAttempts = controls.Speed.MaxNodeAttempts
		}
		if n >= maxAttempts || round.AccountStopped(account.ID) || round.Used() >= min(round.limit, controls.Speed.MaxRequestsPerRound) || ctx.Err() != nil {
			break
		}
		fresh, ok := s.freshHarvestAccount(ctx, account, model)
		if !ok {
			return
		}
		account = fresh
		proxy := s.openAICodexTicketHarvestProxyURLContext(ctx)
		if proxy == "" {
			return
		}
		token, _, err := s.GetAccessToken(ctx, account)
		if err != nil || strings.TrimSpace(token) == "" {
			last.Kind = "token_error"
			break
		}
		if !s.waitHarvestPace(ctx, controls, configured) {
			return
		}
		controls, _ = s.harvestControls(ctx)
		fresh, ok = s.freshHarvestAccount(ctx, account, model)
		if !ok {
			return
		}
		account = fresh
		if controls.NodeMemoryEnabled {
			maxAttempts = controls.Speed.MaxNodeAttempts
		} else {
			maxAttempts = 1
		}
		if n >= maxAttempts || round.AccountStopped(account.ID) || round.Used() >= min(round.limit, controls.Speed.MaxRequestsPerRound) {
			break
		}
		attempt, ok := s.prepareHarvestAttempt(ctx, account, model, proxy, tried, controls)
		if !ok {
			break
		}
		if attempt.node.ID != "" {
			recordCodexHarvestNode(attempt.node.Name, "Selector", 0)
		}
		started := time.Now()
		session := s.harvestAttemptSession(account, model, attempt)
		result := s.executeCodexHarvestProbe(ctx, account, token, model, attempt.proxy, time.Duration(controls.Speed.AttemptTimeoutSeconds)*time.Second, func() bool {
			return s.reserveHarvestRequest(ctx, account, model, round, attempt.sidecar != nil)
		}, session)
		if result.Kind == "account_error" || result.Kind == "rate_limited" {
			round.stopped.Store(account.ID, struct{}{})
		}
		s.finishHarvestAttempt(ctx, attempt, result, time.Since(started), controls)
		if !result.Sent || result.Kind == "cancelled" {
			return
		}
		attempts++
		last = result
		cfg := s.openAICodexTicketConfig()
		raw := ""
		if result.Err != nil {
			raw = result.Err.Error()
		}
		recordCodexHarvestProbe(account, model, result.Kind, attempt.node.Name, raw, result.Status, len(result.State), result.Shape.Blocks, openAICodexTicketTargetLength(account, cfg), codexHarvestExpectedBlocks(account, cfg))
		if result.Kind == "success" {
			s.openaiCodexTicketProbeCooldown.Delete(openAICodexTicketKey(account.ID, model))
			ticket := codexHarvestTicket(account, model, result, cfg, attempts)
			bindCodexHarvestEgress(ticket, attempt, session)
			if err := s.storeOpenAICodexTicket(ctx, account, ticket); err != nil && s.codexHarvest != nil {
				s.codexHarvest.degrade("ticket persisted in memory only; database write failed")
			}
			s.recordCodexProbe(ctx, account, model, result.Kind, result.Status)
			logger.L().Info("openai_codex_ticket harvested", zap.Int64("account_id", account.ID), zap.String("model", model), zap.Int("attempts", attempts))
			return
		}
		if result.Kind == "account_error" || result.Kind == "rate_limited" || attempt.sidecar == nil {
			break
		}
		if s.codexHarvest != nil {
			s.codexHarvest.setRuntime(func(r *CodexHarvestRuntime) { r.SelectionReason = "switch_after_" + result.Kind })
		}
	}
	if ctx.Err() != nil || last.Kind == "" {
		return
	}
	cooldown := time.Duration(controls.Speed.CooldownSeconds) * time.Second
	if last.RetryAfter > cooldown {
		cooldown = last.RetryAfter
	}
	s.openaiCodexTicketProbeCooldown.Store(openAICodexTicketKey(account.ID, model), time.Now().Add(cooldown))
	s.recordCodexProbe(ctx, account, model, last.Kind, last.Status)
	logger.L().Info("openai_codex_ticket probe miss", zap.Int64("account_id", account.ID), zap.String("model", model), zap.String("reason", last.Kind), zap.Int("http", last.Status), zap.Int("attempts", attempts))
}
