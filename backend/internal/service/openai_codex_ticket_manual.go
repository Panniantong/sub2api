package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
)

const (
	ManualHarvestNodeSwitchEveryRequest = "every_request"
	ManualHarvestNodeSwitch312Or2Fail   = "312_or_2fail"
	ManualHarvestNodeSwitch312Only      = "312_only"
	ManualHarvestNodeSwitchNever        = "never"
)

type ManualHarvestRequest struct {
	CollectLanes             int      `json:"collect_lanes"`
	AccountID                int64    `json:"account_id"`
	Models                   []string `json:"models"`
	ProbeIntervalSeconds     int      `json:"probe_interval_seconds"`
	RateLimitCooldownSeconds int      `json:"rate_limit_cooldown_seconds"`
	MaxAttempts              int      `json:"max_attempts"`
	NodeSwitchRule           string   `json:"node_switch_rule"`
	StopOnSuccess            bool     `json:"stop_on_success"`
}

type ManualHarvestProgress struct {
	Attempt       int    `json:"attempt"`
	MaxAttempts   int    `json:"max_attempts"`
	Model         string `json:"model"`
	Node          string `json:"node"`
	HTTPStatus    int    `json:"http_status"`
	Length        int    `json:"length"`
	Blocks        int    `json:"blocks"`
	ExpectedLen   int    `json:"expected_length"`
	ExpectedBlk   int    `json:"expected_blocks"`
	Result        string `json:"result"`
	Level         string `json:"level,omitempty"`
	Message       string `json:"message"`
	Detail        string `json:"detail,omitempty"`
	TicketsStored int    `json:"tickets_stored"`
	Done          bool   `json:"done"`
}

func (s *OpenAIGatewayService) ExecuteManualHarvest(ctx context.Context, req ManualHarvestRequest, progress func(ManualHarvestProgress)) error {
	if s == nil || s.accountRepo == nil {
		return errors.New("gateway service unavailable")
	}
	if !s.codexHarvestRunMu.TryLock() {
		return errors.New("another harvest is running")
	}
	defer s.codexHarvestRunMu.Unlock()
	normalizeManualHarvestRequest(&req)
	req, err := NormalizeManualHarvestRequest(req)
	if err != nil {
		return err
	}
	account, err := s.accountRepo.GetByID(ctx, req.AccountID)
	if err != nil || account == nil {
		return fmt.Errorf("account %d not found: %w", req.AccountID, err)
	}
	if !isOpenAICodexTicketAccount(account) {
		return errors.New("account does not support Codex ticket harvesting; only OpenAI OAuth accounts can harvest tickets")
	}
	cfg := s.openAICodexTicketConfig()
	if len(req.Models) == 0 {
		req.Models = append([]string(nil), cfg.Models...)
	}
	req.Models = NormalizeOpenAICodexTicketModels(req.Models)
	if len(req.Models) == 0 {
		req.Models = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
	}

	emit := func(p ManualHarvestProgress) {
		if progress == nil {
			return
		}
		p.Message = clipFlowText(p.Message, 240)
		p.Detail = clipFlowText(p.Detail, 240)
		p.Node = clipFlowText(p.Node, 160)
		progress(p)
	}

	if req.CollectLanes > 1 {
		return s.executeParallelHarvest(ctx, req, account, emit)
	}
	proxy := s.openAICodexTicketHarvestProxyURLContext(ctx)
	controls, _ := s.harvestControls(ctx)
	timeout := time.Duration(controls.Speed.AttemptTimeoutSeconds) * time.Second
	if timeout < 3*time.Second {
		timeout = 12 * time.Second
	}
	expectedBlocks := codexHarvestExpectedBlocks(account, s.openAICodexTicketConfig())
	expectedLength := openAICodexTicketTargetLength(account, cfg)
	tried := map[string]bool{}
	got := s.manualHarvestLiveModels(account, req.Models)
	keepID := ""
	ticketsStored := 0
	consecutiveFails := 0
	persistPending := map[string]bool{}
	forceSwitch := req.NodeSwitchRule == ManualHarvestNodeSwitchEveryRequest

	emit(ManualHarvestProgress{
		MaxAttempts: req.MaxAttempts,
		Result:      "start",
		Level:       "INFO",
		Message:     fmt.Sprintf("开始单号打票：账号=%s 模型=%s 换节点=%s", account.Name, strings.Join(req.Models, ","), req.NodeSwitchRule),
	})
	if manualHarvestRunComplete(req.StopOnSuccess, req.Models, got) {
		emit(ManualHarvestProgress{MaxAttempts: req.MaxAttempts, Result: "hit", Level: "OK", Done: true, Message: "目标模型已有有效门票，手动打票结束。"})
		return nil
	}

	for attempt := 0; attempt < req.MaxAttempts; {
		if err := ctx.Err(); err != nil {
			emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, TicketsStored: ticketsStored, Done: true, Result: "done", Level: "INFO", Message: "手动打票已停止。"})
			return err
		}
		fresh, err := s.accountRepo.GetByID(ctx, req.AccountID)
		if err != nil || fresh == nil {
			return fmt.Errorf("reload account: %w", err)
		}
		account = fresh
		s.markManualHarvestLiveModels(account, req.Models, got)
		for model := range persistPending {
			delete(got, model)
		}
		if manualHarvestRunComplete(req.StopOnSuccess, req.Models, got) {
			emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, TicketsStored: ticketsStored, Done: true, Result: "hit", Level: "OK", Message: "目标模型已有有效门票，手动打票结束。"})
			return nil
		}
		if s.codexTicketChatHeld(account.ID) {
			emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, TicketsStored: ticketsStored, Result: "pause", Level: "WARN", Message: "该账号正在对话，暂停打票以免轮转 turn-state。"})
			if waitErr := waitManualHarvest(ctx, req.ProbeIntervalSeconds); waitErr != nil {
				return waitErr
			}
			continue
		}
		for _, model := range req.Models {
			if err := ctx.Err(); err != nil {
				emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, TicketsStored: ticketsStored, Done: true, Result: "done", Level: "INFO", Message: "手动打票已停止。"})
				return err
			}
			if manualHarvestModelDone(got, model) {
				continue
			}
			if attempt >= req.MaxAttempts {
				break
			}
			attempt++
			lease, leaseErr := s.acquireManualHarvestNode(ctx, account, model, proxy, keepID, tried, forceSwitch)
			if leaseErr != nil {
				consecutiveFails++
				message, level, detail := describeCodexProbeFailure(leaseErr.Error(), 0, model, "")
				emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Result: "error", Level: level, Message: message, Detail: detail, TicketsStored: ticketsStored})
				if waitErr := waitManualHarvest(ctx, req.ProbeIntervalSeconds); waitErr != nil {
					return waitErr
				}
				continue
			}
			nodeName := strings.TrimSpace(lease.node.Name)
			if forceSwitch && keepID != "" && lease.node.ID != "" && lease.node.ID == keepID && len(tried) > 1 {
				emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Node: nodeName, Result: "node_switch", Level: "WARN", Message: "定向池里暂时没有新的节点，继续使用当前出口。", TicketsStored: ticketsStored})
			} else if lease.node.ID != "" && lease.node.ID != keepID {
				recordCodexHarvestNode(nodeName, "Selector", 0)
				emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Node: nodeName, Result: "node_switch", Level: "INFO", Message: "已切换到节点 " + nodeName, TicketsStored: ticketsStored})
			}
			if lease.node.ID == "" && forceSwitch {
				emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Result: "node_switch", Level: "WARN", Message: "定向出口不可用，本次使用代理轮询；无法确认具体节点。", TicketsStored: ticketsStored})
			}
			keepID = lease.node.ID
			forceSwitch = req.NodeSwitchRule == ManualHarvestNodeSwitchEveryRequest

			token, _, tokenErr := s.GetAccessToken(ctx, account)
			if tokenErr != nil || strings.TrimSpace(token) == "" {
				lease.release()
				consecutiveFails++
				emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Node: nodeName, Result: "error", Level: "ERROR", Message: "无法获取该账号的登录令牌，本次尝试已跳过", Detail: fmt.Sprintf("get access token failed: %v", tokenErr), TicketsStored: ticketsStored})
				if waitErr := waitManualHarvest(ctx, req.ProbeIntervalSeconds); waitErr != nil {
					return waitErr
				}
				continue
			}

			started := time.Now()
			session := s.harvestAttemptSession(account, model, lease)
			result := s.executeCodexHarvestProbe(ctx, account, token, model, lease.proxy, timeout, func() bool { return ctx.Err() == nil }, session)
			s.completeManualHarvestAttempt(ctx, lease, result, time.Since(started), controls)
			length, blocks := len(result.State), result.Shape.Blocks
			raw := ""
			if result.Err != nil {
				raw = result.Err.Error()
			}
			message, level, detail := describeCodexHarvestOutcome(result.Kind, raw, result.Status, length, blocks, expectedLength, expectedBlocks, model, nodeName)
			progressResult := "error"
			switch result.Kind {
			case "success":
				progressResult = "hit"
			case "invalid_state":
				progressResult = "miss_degraded"
			case "rate_limited":
				progressResult = "rate_limited"
			}
			recordCodexHarvestProbe(account, model, result.Kind, nodeName, raw, result.Status, length, blocks, expectedLength, expectedBlocks)

			if result.Kind == "success" {
				consecutiveFails = 0
				ticket := codexHarvestTicket(account, model, result, cfg, attempt)
				bindCodexHarvestEgress(ticket, lease, session)
				if storeErr := s.storeOpenAICodexTicket(ctx, account, ticket); storeErr != nil {
					persistPending[model] = true
					if s.codexHarvest != nil {
						s.codexHarvest.degrade("ticket persisted in memory only; database write failed")
					}
					emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Node: nodeName, Result: "persist_failed", Level: "WARN", Message: "已捕获合规门票，但持久化失败，继续重试", TicketsStored: ticketsStored})
					if waitErr := waitManualHarvest(ctx, req.ProbeIntervalSeconds); waitErr != nil {
						return waitErr
					}
					continue
				}
				delete(persistPending, model)
				s.openaiCodexTicketProbeCooldown.Delete(openAICodexTicketKey(account.ID, model))
				ticketsStored++
				got[model] = struct{}{}
				emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Node: nodeName, HTTPStatus: result.Status, Length: length, Blocks: blocks, ExpectedLen: expectedLength, ExpectedBlk: expectedBlocks, Result: progressResult, Level: "OK", Message: message, Detail: detail, TicketsStored: ticketsStored})
				if manualHarvestRunComplete(req.StopOnSuccess, req.Models, got) {
					msg := "全部目标模型已出票，手动打票结束。"
					if req.StopOnSuccess {
						msg = "达成出票即停条件，手动打票结束。"
					}
					emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Node: nodeName, Result: "hit", Level: "OK", TicketsStored: ticketsStored, Done: true, Message: msg})
					return nil
				}
			} else {
				consecutiveFails++
				emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Node: nodeName, HTTPStatus: result.Status, Length: length, Blocks: blocks, ExpectedLen: expectedLength, ExpectedBlk: expectedBlocks, Result: progressResult, Level: level, Message: message, Detail: detail, TicketsStored: ticketsStored})
				if result.Kind == "account_error" {
					emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Result: "error", Level: "ERROR", TicketsStored: ticketsStored, Done: true, Message: message, Detail: detail})
					return nil
				}
			}

			if result.Kind != "success" && manualHarvestShouldSwitch(req.NodeSwitchRule, consecutiveFails, result.Kind, length, blocks) {
				emit(ManualHarvestProgress{Attempt: attempt, MaxAttempts: req.MaxAttempts, Model: model, Node: nodeName, Result: "node_switch", Level: "WARN", Message: fmt.Sprintf("按规则 %s 准备换节点（连续失败 %d）。", req.NodeSwitchRule, consecutiveFails), TicketsStored: ticketsStored})
				forceSwitch = true
				consecutiveFails = 0
			}
			wait := req.ProbeIntervalSeconds
			if result.Kind == "rate_limited" {
				wait = req.RateLimitCooldownSeconds
			}
			if waitErr := waitManualHarvest(ctx, wait); waitErr != nil {
				return waitErr
			}
		}
	}

	emit(ManualHarvestProgress{Attempt: req.MaxAttempts, MaxAttempts: req.MaxAttempts, Result: "done", TicketsStored: ticketsStored, Done: true, Level: "WARN", Message: fmt.Sprintf("已达到最大尝试次数（%d 次），打票任务结束。", req.MaxAttempts)})
	return nil
}

func manualHarvestModelDone(got map[string]struct{}, model string) bool {
	_, ok := got[model]
	return ok
}

func manualHarvestRunComplete(stopOnSuccess bool, models []string, got map[string]struct{}) bool {
	if len(got) == 0 {
		return false
	}
	if stopOnSuccess {
		return true
	}
	for _, model := range models {
		if _, ok := got[model]; !ok {
			return false
		}
	}
	return true
}

func NormalizeManualHarvestRequest(req ManualHarvestRequest) (ManualHarvestRequest, error) {
	if req.CollectLanes < 0 || req.CollectLanes > mihomo.MaxCollectLanes {
		return req, errors.New("collect_lanes must be 0-32")
	}

	req.NodeSwitchRule = strings.TrimSpace(req.NodeSwitchRule)
	switch req.NodeSwitchRule {
	case "", ManualHarvestNodeSwitch312Or2Fail:
		req.NodeSwitchRule = ManualHarvestNodeSwitch312Or2Fail
	case ManualHarvestNodeSwitchEveryRequest, ManualHarvestNodeSwitch312Only, ManualHarvestNodeSwitchNever:
	default:
		return req, errors.New("invalid node_switch_rule")
	}
	if req.ProbeIntervalSeconds <= 0 {
		req.ProbeIntervalSeconds = 10
	}
	if req.ProbeIntervalSeconds < manualHarvestProbeIntervalMin || req.ProbeIntervalSeconds > manualHarvestProbeIntervalMax {
		return req, errors.New("probe_interval_seconds must be 1-300")
	}
	if req.RateLimitCooldownSeconds <= 0 {
		req.RateLimitCooldownSeconds = 30
	}
	if req.RateLimitCooldownSeconds < manualHarvestRateLimitCooldownMin || req.RateLimitCooldownSeconds > manualHarvestRateLimitCooldownMax {
		return req, errors.New("rate_limit_cooldown_seconds must be 1-60")
	}
	if req.MaxAttempts <= 0 {
		req.MaxAttempts = 20
	}
	if req.MaxAttempts < manualHarvestMaxAttemptsMin || req.MaxAttempts > manualHarvestMaxAttemptsMax {
		return req, errors.New("max_attempts must be 1-100")
	}
	seen := map[string]bool{}
	models := make([]string, 0, len(req.Models))
	for _, model := range req.Models {
		model = normalizeOpenAICodexTicketModel(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
		if len(models) >= manualHarvestMaxModels {
			break
		}
	}
	if len(models) == 0 {
		models = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
	}
	req.Models = models
	return req, nil
}

func waitManualHarvest(ctx context.Context, seconds int) error {
	if seconds <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func manualHarvestShouldSwitch(rule string, consecutiveFails int, kind string, length, blocks int) bool {
	degraded := kind == "invalid_state" && (blocks == 11 || blocks == 13 || length == 312 || length == 356)
	switch rule {
	case ManualHarvestNodeSwitchNever:
		return false
	case ManualHarvestNodeSwitchEveryRequest:
		return true
	case ManualHarvestNodeSwitch312Only:
		return degraded
	default:
		return degraded || consecutiveFails >= 2
	}
}

func (s *OpenAIGatewayService) acquireManualHarvestNode(ctx context.Context, account *Account, model, proxy, keepID string, tried map[string]bool, forceNew bool) (codexHarvestAttempt, error) {
	fallback := codexHarvestAttempt{proxy: proxy, release: func() {}}
	if s.codexHarvest == nil || strings.TrimSpace(proxy) == "" {
		return fallback, nil
	}
	sidecar, err := mihomo.LoadDirectedSidecar(os.Getenv("DATA_DIR"), proxy)
	if err != nil {
		s.codexHarvest.degrade(err.Error())
		if _, _, managed := mihomo.ManagedController(); managed && strings.TrimRight(proxy, "/") == mihomo.Endpoint {
			return fallback, err
		}
		return fallback, nil
	}
	query, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	nodes, err := sidecar.Directory(query)
	if err != nil {
		s.codexHarvest.degrade(err.Error())
		return fallback, err
	}
	pick := func(force bool) (mihomo.HarvestNode, bool) {
		if !force && keepID != "" {
			for _, node := range nodes {
				if node.ID == keepID {
					return node, true
				}
			}
		}
		scope := CodexHarvestNodeScope{PoolID: sidecar.PoolID, AccountID: account.ID, Identity: ticketIdentity(account), Model: model, Blocks: codexHarvestExpectedBlocks(account, s.openAICodexTicketConfig())}
		var records []CodexHarvestNodeRecord
		if controls, _ := s.harvestControls(ctx); controls.NodeMemoryEnabled {
			if _, stored, snapErr := s.codexHarvest.nodes.Snapshot(query, scope); snapErr == nil {
				records = stored
			}
		}
		excluded := tried
		if !force && keepID != "" {
			excluded = map[string]bool{}
			for id, seen := range tried {
				excluded[id] = seen
			}
		}
		ranked := rankCodexHarvestNodes(nodes, records, excluded, s.codexHarvest.explore.Add(1)-1, time.Now())
		if len(ranked) == 0 {
			ranked = rankCodexHarvestNodes(nodes, records, map[string]bool{}, s.codexHarvest.explore.Add(1)-1, time.Now())
		}
		if len(ranked) == 0 {
			return mihomo.HarvestNode{}, false
		}
		if force && keepID != "" && ranked[0].ID == keepID && len(ranked) > 1 {
			return ranked[1], true
		}
		return ranked[0], true
	}
	node, ok := pick(forceNew)
	if !ok {
		return fallback, errors.New("no identifiable harvest leaf nodes")
	}
	release, err := sidecar.Acquire(ctx, node)
	if err != nil {
		s.codexHarvest.degrade("directed selection unavailable")
		return fallback, err
	}
	tried[node.ID] = true
	scope := CodexHarvestNodeScope{PoolID: sidecar.PoolID, AccountID: account.ID, Identity: ticketIdentity(account), Model: model, Blocks: codexHarvestExpectedBlocks(account, s.openAICodexTicketConfig())}
	generation := int64(0)
	if controls, _ := s.harvestControls(ctx); controls.NodeMemoryEnabled {
		if gen, _, snapErr := s.codexHarvest.nodes.Snapshot(query, scope); snapErr == nil {
			generation = gen
		}
	}
	s.codexHarvest.setRuntime(func(r *CodexHarvestRuntime) {
		r.CurrentNode = node.Name
		r.SelectionReason = "manual"
		r.DegradedReason = ""
	})
	return codexHarvestAttempt{sidecar: sidecar, node: node, proxy: sidecar.ProxyURL, release: release, feedback: CodexHarvestNodeFeedback{Scope: scope, Node: node, Generation: generation}}, nil
}

func (s *OpenAIGatewayService) completeManualHarvestAttempt(ctx context.Context, attempt codexHarvestAttempt, result codexHarvestProbeResult, elapsed time.Duration, controls CodexHarvestControls) {
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
	if !controls.NodeMemoryEnabled {
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

func describeCodexProbeFailure(rawErr string, status int, model, node string) (message, level, detail string) {
	lower := strings.ToLower(rawErr)
	detailParts := make([]string, 0, 4)
	if status > 0 {
		detailParts = append(detailParts, fmt.Sprintf("HTTP %d", status))
	}
	if rawErr != "" {
		detailParts = append(detailParts, clipFlowText(rawErr, 220))
	}
	if model != "" {
		detailParts = append(detailParts, "model="+model)
	}
	if node != "" {
		detailParts = append(detailParts, "node="+node)
	}
	detail = strings.Join(detailParts, " · ")
	switch {
	case status == http.StatusUnauthorized:
		return "账号登录凭证已失效，需要重新登录该账号", "ERROR", detail
	case status == http.StatusForbidden:
		return "上游拒绝了本次请求（账号或模型被限制）→ 换节点重试", "WARN", detail
	case strings.Contains(lower, "invalid probe event"):
		return "节点返回的内容不是有效数据，可能被拦截了 → 换节点重试", "WARN", detail
	case strings.Contains(lower, "probe response failed"):
		return "上游明确返回失败（账号或模型被限制）→ 换节点重试", "WARN", detail
	case strings.Contains(lower, "invalid probe completion"):
		return "上游回复不完整就中断了 → 换节点重试", "WARN", detail
	case strings.Contains(lower, "invalid probe stream"):
		return "响应数据流损坏，读不下去 → 换节点重试", "WARN", detail
	case strings.Contains(lower, "unterminated probe event"):
		return "响应被中途截断（连接被掐断）→ 换节点重试", "WARN", detail
	case strings.Contains(lower, "probe did not complete successfully"):
		return "上游没给出完整结果就结束了 → 换节点重试", "WARN", detail
	case strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "client.timeout"),
		strings.Contains(lower, "timeout"), strings.Contains(lower, "timed out"):
		return "节点响应超时（超过设定时间没有回）→ 换节点重试", "WARN", detail
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "connection refused"):
		return "出口节点连不上 → 换节点重试", "WARN", detail
	case strings.Contains(lower, "eof"), strings.Contains(lower, "connection reset"),
		strings.Contains(lower, "connection aborted"), strings.Contains(lower, "broken pipe"):
		return "节点连接被中断 → 换节点重试", "WARN", detail
	}
	return "本次探针没有拿到合规门票 → 换节点重试", "WARN", detail
}

const (
	manualHarvestProbeIntervalMin     = 1
	manualHarvestProbeIntervalMax     = 300
	manualHarvestRateLimitCooldownMin = 1
	manualHarvestRateLimitCooldownMax = 60
	manualHarvestMaxAttemptsMin       = 1
	manualHarvestMaxAttemptsMax       = 100
	manualHarvestMaxModels            = 20
)

func normalizeManualHarvestRequest(req *ManualHarvestRequest) {
	if req == nil {
		return
	}
	if req.ProbeIntervalSeconds == 0 {
		req.ProbeIntervalSeconds = 10
	}
	if req.ProbeIntervalSeconds < manualHarvestProbeIntervalMin {
		req.ProbeIntervalSeconds = manualHarvestProbeIntervalMin
	}
	if req.ProbeIntervalSeconds > manualHarvestProbeIntervalMax {
		req.ProbeIntervalSeconds = manualHarvestProbeIntervalMax
	}
	if req.RateLimitCooldownSeconds == 0 {
		req.RateLimitCooldownSeconds = 30
	}
	if req.RateLimitCooldownSeconds < manualHarvestRateLimitCooldownMin {
		req.RateLimitCooldownSeconds = manualHarvestRateLimitCooldownMin
	}
	if req.RateLimitCooldownSeconds > manualHarvestRateLimitCooldownMax {
		req.RateLimitCooldownSeconds = manualHarvestRateLimitCooldownMax
	}
	if req.MaxAttempts == 0 {
		req.MaxAttempts = 20
	}
	if req.MaxAttempts < manualHarvestMaxAttemptsMin {
		req.MaxAttempts = manualHarvestMaxAttemptsMin
	}
	if req.MaxAttempts > manualHarvestMaxAttemptsMax {
		req.MaxAttempts = manualHarvestMaxAttemptsMax
	}
	seen := map[string]bool{}
	models := make([]string, 0, len(req.Models))
	for _, model := range req.Models {
		model = normalizeOpenAICodexTicketModel(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
		if len(models) >= manualHarvestMaxModels {
			break
		}
	}
	if len(models) == 0 {
		models = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
	}
	req.Models = models
}
