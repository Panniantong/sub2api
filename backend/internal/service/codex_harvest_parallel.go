package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/google/uuid"
)

type harvestCollection interface {
	Next(context.Context, int) (string, string, error)
	Close() error
}

type parallelHarvestOutcome struct {
	result        codexHarvestProbeResult
	node, session string
	attempt       int
}

// Only the coordinator persists tickets and emits SSE. Workers never touch the
// response writer or mutate the account snapshot.
func (s *OpenAIGatewayService) executeParallelHarvest(ctx context.Context, req ManualHarvestRequest, account *Account, emit func(ManualHarvestProgress)) (err error) {
	if s.codexTicketChatHeld(account.ID) {
		return errors.New("account is in an active conversation")
	}
	collection, err := mihomo.BeginCollection(ctx, s.openAICodexTicketHarvestProxyURLContext(ctx))
	if err != nil {
		return err
	}
	return s.runParallelHarvest(ctx, req, account, emit, collection)
}

func (s *OpenAIGatewayService) runParallelHarvest(ctx context.Context, req ManualHarvestRequest, account *Account, emit func(ManualHarvestProgress), collection harvestCollection) (err error) {
	defer func() {
		if closeErr := collection.Close(); err == nil {
			err = closeErr
		}
	}()
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil || strings.TrimSpace(token) == "" {
		return errors.New("cannot obtain account access token")
	}
	controls, _ := s.harvestControls(ctx)
	timeout := time.Duration(controls.Speed.AttemptTimeoutSeconds) * time.Second
	if timeout < time.Second {
		timeout = 20 * time.Second
	}
	cfg := s.openAICodexTicketConfig()
	stored := 0
	var used atomic.Int64
	emit(ManualHarvestProgress{Result: "start", MaxAttempts: req.MaxAttempts, Message: fmt.Sprintf("开始并行采集（%d 路，总请求上限 %d）", req.CollectLanes, req.MaxAttempts)})
	for _, model := range req.Models {
		if used.Load() >= int64(req.MaxAttempts) {
			break
		}
		if _, ok := s.manualHarvestLiveModels(account, []string{model})[model]; ok {
			continue
		}
		round, cancel := context.WithCancel(ctx)
		results := make(chan parallelHarvestOutcome, req.CollectLanes)
		var wg sync.WaitGroup
		for lane := 0; lane < req.CollectLanes; lane++ {
			wg.Add(1)
			go func(lane int) {
				defer wg.Done()
				for round.Err() == nil {
					// Reserve before selecting a node, with a strict shared request cap.
					n := reserveParallelProbe(&used, req.MaxAttempts)
					if n == 0 {
						return
					}
					node, proxy, e := collection.Next(round, lane)
					if e != nil {
						select {
						case results <- parallelHarvestOutcome{result: codexHarvestProbeResult{Kind: "network_error", Err: e}, attempt: int(n)}:
						case <-round.Done():
						}
						return
					}
					if node == "" {
						return
					}
					session := uuid.NewString()
					result := s.executeCodexHarvestProbe(round, account, token, model, proxy, timeout, func() bool { return round.Err() == nil && !s.codexTicketChatHeld(account.ID) }, session)
					outcome := parallelHarvestOutcome{result: result, node: node, session: session, attempt: int(n)}
					select {
					case results <- outcome:
					case <-round.Done():
						return
					}
					if result.Kind == "account_error" || result.Kind == "rate_limited" || result.Kind == "success" {
						return
					}
					if waitManualHarvest(round, req.ProbeIntervalSeconds) != nil {
						return
					}
				}
			}(lane)
		}
		go func() { wg.Wait(); close(results) }()
		won := false
		stop := false
		for out := range results {
			if won || stop || ctx.Err() != nil {
				continue
			}
			r := out.result
			raw := safeCodexHarvestError(r.Err)
			message, level, detail := describeCodexHarvestOutcome(r.Kind, raw, r.Status, len(r.State), r.Shape.Blocks, openAICodexTicketTargetLength(account, cfg), codexHarvestExpectedBlocks(account, cfg), model, mihomo.NodeDisplayName(out.node))
			event := ManualHarvestProgress{Attempt: out.attempt, MaxAttempts: req.MaxAttempts, Model: model, Node: mihomo.NodeDisplayName(out.node), HTTPStatus: r.Status, Length: len(r.State), Blocks: r.Shape.Blocks, Result: r.Kind, Level: level, Message: message, Detail: detail, TicketsStored: stored}
			recordCodexHarvestProbe(account, model, r.Kind, mihomo.NodeDisplayName(out.node), raw, r.Status, len(r.State), r.Shape.Blocks, openAICodexTicketTargetLength(account, cfg), codexHarvestExpectedBlocks(account, cfg))
			if r.Kind == "success" {
				fresh, e := s.accountRepo.GetByID(ctx, account.ID)
				if e != nil || fresh == nil || ticketIdentity(fresh) != ticketIdentity(account) {
					cancel()
					stop = true
					err = errors.New("account identity changed during collection")
					continue
				}
				ticket := codexHarvestTicket(account, model, r, cfg, out.attempt)
				bindCodexHarvestEgress(ticket, codexHarvestAttempt{proxy: mihomo.Endpoint, node: mihomo.HarvestNode{ID: out.node, Name: out.node, Provider: "managed"}}, out.session)
				if e = s.storeOpenAICodexTicket(ctx, fresh, ticket); e != nil {
					event.Result = "persist_failed"
					event.Level = "WARN"
					event.Message = "合规门票持久化失败，未计入成功"
				} else {
					stored++
					won = true
					cancel()
					event.Result = "hit"
					event.TicketsStored = stored
					s.openaiCodexTicketProbeCooldown.Delete(openAICodexTicketKey(account.ID, model))
				}
			}
			if r.Kind == "account_error" || r.Kind == "rate_limited" {
				stop = true
				cancel()
				cooldown := r.RetryAfter
				if fallback := time.Duration(req.RateLimitCooldownSeconds) * time.Second; fallback > cooldown {
					cooldown = fallback
				}
				s.openaiCodexTicketProbeCooldown.Store(openAICodexTicketKey(account.ID, model), time.Now().Add(cooldown))
			}
			emit(event)
		}
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if stop || (won && req.StopOnSuccess) {
			break
		}
	}
	if err != nil {
		return err
	}
	if err = collection.Close(); err != nil {
		return err
	}
	emit(ManualHarvestProgress{Attempt: min(int(used.Load()), req.MaxAttempts), MaxAttempts: req.MaxAttempts, TicketsStored: stored, Result: "done", Done: true, Message: fmt.Sprintf("并行采集结束，已保存 %d 张门票", stored)})
	return err
}

func reserveParallelProbe(used *atomic.Int64, limit int) int64 {
	for {
		n := used.Load()
		if n >= int64(limit) {
			return 0
		}
		if used.CompareAndSwap(n, n+1) {
			return n + 1
		}
	}
}
