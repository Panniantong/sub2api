package service

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
)

type codexHarvestRoundKey struct{}
type codexHarvestRound struct {
	limit   int
	used    atomic.Int64
	stopped sync.Map
}

func (r *codexHarvestRound) AccountStopped(id int64) bool {
	_, stopped := r.stopped.Load(id)
	return stopped
}

func (r *codexHarvestRound) Used() int { return int(r.used.Load()) }

func (r *codexHarvestRound) Take(limit int) bool {
	for {
		used := r.used.Load()
		if used >= int64(min(r.limit, limit)) {
			return false
		}
		if r.used.CompareAndSwap(used, used+1) {
			return true
		}
	}
}

type codexHarvestAttempt struct {
	sidecar  *mihomo.DirectedSidecar
	node     mihomo.HarvestNode
	feedback CodexHarvestNodeFeedback
	proxy    string
	release  func()
}

func (s *OpenAIGatewayService) harvestControls(ctx context.Context) (CodexHarvestControls, bool) {
	if s.codexHarvest != nil {
		v, saved, err := s.codexHarvest.Controls(ctx)
		if err != nil {
			s.codexHarvest.degrade("settings unavailable; retaining last valid speed")
		}
		return v, saved
	}
	cfg := s.openAICodexTicketConfig()
	return CodexHarvestControls{Version: 1, Speed: CodexHarvestSpeed{
		RoundIntervalSeconds:  cfg.HarvestProbeIntervalSeconds,
		ProbeIntervalSeconds:  0,
		AttemptTimeoutSeconds: cfg.HarvestAttemptTimeoutSeconds,
		CooldownSeconds:       cfg.HarvestCooldownSeconds,
		MaxRequestsPerRound:   cfg.MaxProbesPerRound,
		MaxNodeAttempts:       1,
		RefreshBeforeSeconds:  cfg.RefreshBeforeSeconds,
	}}, false
}

func (s *OpenAIGatewayService) harvestTicketConfig(ctx context.Context) config.OpenAICodexTicketConfig {
	cfg := s.openAICodexTicketConfig()
	controls, _ := s.harvestControls(ctx)
	applyHarvestSpeed(&cfg, controls.Speed)
	return cfg
}

func (s *OpenAIGatewayService) freshHarvestAccount(ctx context.Context, account *Account, model string) (*Account, bool) {
	if ctx.Err() != nil || account == nil || openAICodexSkipHarvest(account) || !s.openAICodexTicketEnabledContext(ctx) {
		return nil, false
	}
	if s.codexHarvest != nil {
		fresh, err := s.accountRepo.GetByID(ctx, account.ID)
		if err != nil || fresh == nil || ticketIdentity(fresh) != ticketIdentity(account) {
			return nil, false
		}
		account = fresh
		scope, err := s.settingService.GetCodexTicketHarvestScope(ctx)
		if err != nil || !scope.includes(account) || !scope.allowsAccount(account) {
			return nil, false
		}
	}
	if !isOpenAICodexTicketAccount(account) || account.IsRateLimited() || openAICodexSkipHarvest(account) || s.codexTicketChatHeld(account.ID) || s.ticketProbeCoolingDown(account.ID, model, time.Now()) {
		return nil, false
	}
	cfg := s.openAICodexTicketConfig()
	found := false
	for _, m := range cfg.Models {
		if normalizeOpenAICodexTicketModel(m) == model {
			found = true
		}
	}
	if !found {
		return nil, false
	}
	if !s.codexHarvestNeedsTicket(account, model, s.harvestTicketConfig(ctx)) {
		return nil, false
	}
	return account, true
}

func (s *OpenAIGatewayService) codexHarvestNeedsTicket(account *Account, model string, cfg config.OpenAICodexTicketConfig) bool {
	refresh := time.Duration(cfg.RefreshBeforeSeconds) * time.Second
	if s.settingService.GetCodexTicketStrategy(context.Background()) == "fixed" {
		refresh = 0
	}
	now := time.Now()
	ticket := s.lookupOpenAICodexTicket(account, model)
	target := openAICodexTicketTargetLength(account, cfg)
	if ticket.valid(now, target) && !ticket.needsRefresh(now, refresh) {
		return false
	}
	return ticket == nil || !ticket.Standby.valid(now, target) || ticket.Standby.needsRefresh(now, refresh)
}

func pinnedHarvestNode(nodes []mihomo.HarvestNode, ticket *openAICodexTicket, tried map[string]bool) (mihomo.HarvestNode, bool) {
	if ticket == nil || strings.TrimSpace(ticket.HarvestNodeID) == "" {
		return mihomo.HarvestNode{}, false
	}
	for _, node := range nodes {
		if node.ID == ticket.HarvestNodeID && !tried[node.ID] {
			return node, true
		}
	}
	return mihomo.HarvestNode{}, false
}

func (s *OpenAIGatewayService) prepareHarvestAttempt(ctx context.Context, account *Account, model, proxy string, tried map[string]bool, controls CodexHarvestControls) (codexHarvestAttempt, bool) {
	a := codexHarvestAttempt{proxy: proxy, release: func() {}}
	pinned := s.lookupOpenAICodexTicket(account, model)
	if pinned != nil && strings.TrimSpace(pinned.HarvestProxyURL) != "" && (!controls.NodeMemoryEnabled || s.codexHarvest == nil) {
		a.proxy = pinned.HarvestProxyURL
	}
	if !controls.NodeMemoryEnabled || s.codexHarvest == nil {
		return a, true
	}
	learning := s.codexHarvest
	sidecar, err := mihomo.LoadDirectedSidecar(os.Getenv("DATA_DIR"), proxy)
	if err != nil {
		if pinned != nil && strings.TrimSpace(pinned.HarvestProxyURL) != "" {
			a.proxy = pinned.HarvestProxyURL
		}
		learning.degrade(err.Error())
		return a, true
	}
	query, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	nodes, err := sidecar.Directory(query)
	if err != nil {
		learning.degrade(err.Error())
		return a, true
	}
	scope := CodexHarvestNodeScope{PoolID: sidecar.PoolID, AccountID: account.ID, Identity: ticketIdentity(account), Model: model, Blocks: openAICodexTicketExpectedBlocks(account)}
	generation, records, err := learning.nodes.Snapshot(query, scope)
	if err != nil {
		learning.degrade("node learning storage unavailable; using rotation")
		return a, true
	}
	reason := "explore"
	node, ok := pinnedHarvestNode(nodes, pinned, tried)
	if ok {
		reason = "ticket_sticky"
	} else {
		ranked := rankCodexHarvestNodes(nodes, records, tried, learning.explore.Add(1)-1, time.Now())
		if len(ranked) == 0 {
			learning.degrade("no untried eligible nodes; waiting for cooldown")
			return a, false
		}
		node = ranked[0]
		for _, r := range records {
			if r.NodeID == node.ID && r.LastSuccess != nil && r.LastSuccess.After(time.Now().Add(-7*24*time.Hour)) {
				reason = "recent_success"
			}
		}
	}
	tried[node.ID] = true
	release, err := sidecar.Acquire(ctx, node)
	if err != nil {
		learning.degrade("directed selection unavailable; using rotation")
		return a, true
	}
	learning.setRuntime(func(r *CodexHarvestRuntime) {
		r.CurrentNode = node.Name
		r.SelectionReason = reason
		r.DegradedReason = ""
	})
	return codexHarvestAttempt{sidecar: sidecar, node: node, proxy: sidecar.ProxyURL, release: release,
		feedback: CodexHarvestNodeFeedback{Scope: scope, Node: node, Generation: generation}}, true
}

func (s *OpenAIGatewayService) waitHarvestPace(ctx context.Context, _ CodexHarvestControls, configured bool) bool {
	if s.codexHarvest == nil || !configured {
		return ctx.Err() == nil
	}
	for {
		if ctx.Err() != nil {
			return false
		}
		current, stillConfigured := s.harvestControls(ctx)
		if !stillConfigured {
			return true
		}
		s.codexHarvest.runtimeMu.Lock()
		delay := time.Until(s.codexHarvest.lastRequest.Add(time.Duration(current.Speed.ProbeIntervalSeconds) * time.Second))
		s.codexHarvest.runtimeMu.Unlock()
		if delay <= 0 {
			return true
		}
		if delay > 250*time.Millisecond {
			delay = 250 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func (s *OpenAIGatewayService) reserveHarvestRequest(ctx context.Context, account *Account, model string, round *codexHarvestRound, learning bool) bool {
	controls, _ := s.harvestControls(ctx)
	if learning && !controls.NodeMemoryEnabled {
		return false
	}
	if _, ok := s.freshHarvestAccount(ctx, account, model); !ok {
		return false
	}
	if round.AccountStopped(account.ID) || !round.Take(controls.Speed.MaxRequestsPerRound) {
		return false
	}
	if s.codexHarvest != nil {
		s.codexHarvest.runtimeMu.Lock()
		s.codexHarvest.lastRequest = time.Now()
		s.codexHarvest.runtime.RequestsUsed = round.Used()
		s.codexHarvest.runtime.RequestBudget = min(round.limit, controls.Speed.MaxRequestsPerRound)
		s.codexHarvest.runtimeMu.Unlock()
	}
	return true
}

func (s *OpenAIGatewayService) finishHarvestAttempt(ctx context.Context, attempt codexHarvestAttempt, result codexHarvestProbeResult, elapsed time.Duration, controls CodexHarvestControls) {
	defer attempt.release()
	if attempt.sidecar == nil || s.codexHarvest == nil || !result.Sent || result.Kind == "cancelled" {
		return
	}
	query, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := attempt.sidecar.Confirm(query, attempt.node); err != nil {
		s.codexHarvest.degrade("node attribution changed; learning skipped")
		return
	}
	feedback := attempt.feedback
	feedback.Result = result.Kind
	feedback.LatencyMS = elapsed.Milliseconds()
	feedback.CooldownSeconds = controls.Speed.CooldownSeconds
	if _, err := s.codexHarvest.nodes.Record(query, feedback); err != nil {
		s.codexHarvest.degrade("learning feedback failed; ticket remains usable")
	}
}
