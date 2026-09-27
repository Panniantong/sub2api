package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

var errCookieWSPoolUnavailable = errors.New("Cookie WS pool unavailable or exhausted; select and save a valid Host to build a pool")

const openAICookieWSProbeModel = "gpt-6-astra"

func (p *openAIWSConnPool) hasCookieWSBatch(accountID int64) bool {
	ap, ok := p.getAccountPool(accountID)
	if !ok {
		return false
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.cookieBatch != nil
}

type openAICookieWSBatch struct {
	host       string
	target     int
	pending    int
	expires    time.Time
	preparing  bool
	lastError  string
	probeState string
	probeID    string
	cancel     context.CancelFunc
}

type openAICookieWSBuildCallback func(live, target int, err error)

// ScheduleCookieWSBinding is called by the account update endpoint, never by
// inference requests. Each binding gets a single finite batch of dial attempts.
func (s *OpenAIGatewayService) ScheduleCookieWSBinding(ctx context.Context, account *Account) (resultErr error) {
	return s.scheduleCookieWSBinding(ctx, account, nil)
}

func (s *OpenAIGatewayService) scheduleCookieWSBinding(ctx context.Context, account *Account, onComplete openAICookieWSBuildCallback) (resultErr error) {
	if s == nil || !isOpenAICodexTicketAccount(account) {
		return nil
	}
	pool := s.getOpenAIWSConnPool()
	defer func() {
		if resultErr != nil {
			ap := pool.getOrCreateAccountPool(account.ID)
			ap.mu.Lock()
			ap.cookieBindingError = resultErr.Error()
			ap.mu.Unlock()
		}
	}()
	host := openAICodexCookieHostFromAccount(account)
	if host == "" {
		if pool.hasCookieWSBatch(account.ID) {
			pool.ClearAccount(account.ID)
		}
		return nil
	}
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.WSEnabled || settings.CookieRotationEnabled {
		pool.ClearAccount(account.ID)
		return nil
	}
	if decision := s.getOpenAIWSProtocolResolver().Resolve(account); decision.Transport != OpenAIUpstreamTransportResponsesWebsocketV2 {
		return fmt.Errorf("Cookie WS cannot start: %s", decision.Reason)
	}
	entry, err := s.settingService.LookupOpenAICodexCookie(ctx, host)
	if err != nil {
		return err
	}
	if entry == nil {
		return errors.New("所选 Host 的 Cookie 已过期或不存在，请重新选择")
	}
	expires := time.Now().Add(time.Duration(settings.WSTTLSeconds) * time.Second)
	if !entry.ExpiresAt.IsZero() && entry.ExpiresAt.Before(expires) {
		expires = entry.ExpiresAt
	}
	batchCtx, cancel := context.WithDeadline(context.Background(), expires)
	batch := &openAICookieWSBatch{host: host, target: settings.WSConnections, pending: settings.WSConnections, expires: expires, preparing: true, probeState: "probing", cancel: cancel}
	ap := pool.getOrCreateAccountPool(account.ID)
	ap.mu.Lock()
	// Repeated saves of the same live binding do not replenish lost connections.
	if ap.cookieBatch != nil && ap.cookieBatch.host == host && ap.cookieBatch.expires.After(time.Now()) {
		live := len(ap.conns)
		target := ap.cookieBatch.target
		ap.mu.Unlock()
		cancel()
		if onComplete != nil {
			onComplete(live, target, nil)
		}
		return nil
	}
	if ap.cookieBatch != nil {
		ap.cookieBatch.cancel()
	}
	old := make([]*openAIWSConn, 0, len(ap.conns))
	for _, conn := range ap.conns {
		old = append(old, conn)
	}
	ap.conns = map[string]*openAIWSConn{}
	ap.pinnedConns = map[string]int{}
	ap.generation++
	ap.lastAcquire = nil
	ap.cookieBatch = batch
	ap.cookieBindingError = ""
	ap.signalChangedLocked()
	ap.mu.Unlock()
	go func() {
		<-batchCtx.Done()
		ap.mu.Lock()
		var expired []*openAIWSConn
		if ap.cookieBatch == batch {
			for id, conn := range ap.conns {
				if conn != nil && !conn.isLeased() {
					expired = append(expired, conn)
					delete(ap.conns, id)
					delete(ap.pinnedConns, id)
				}
			}
			ap.signalChangedLocked()
		}
		ap.mu.Unlock()
		closeOpenAIWSConns(expired)
	}()
	go func() {
		closeOpenAIWSConns(old)
		s.buildCookieWSBatch(batchCtx, account.ID, host, entry.Cookie, batch, ap, onComplete)
	}()
	return nil
}

func (s *OpenAIGatewayService) buildCookieWSBatch(ctx context.Context, accountID int64, host, cookie string, batch *openAICookieWSBatch, ap *openAIWSAccountPool, onComplete openAICookieWSBuildCallback) {
	pool := s.getOpenAIWSConnPool()
	fail := func(err error) {
		logOpenAIWSModeInfo("cookie_ws_pool_build_failed account_id=%d host=%s reason=%s", accountID, truncateOpenAIWSLogValue(host, openAIWSLogValueMaxLen), truncateOpenAIWSLogValue(err.Error(), openAIWSLogValueMaxLen))
		ap.mu.Lock()
		active := ap.cookieBatch == batch
		if ap.cookieBatch == batch {
			batch.preparing = false
			batch.pending = 0
			batch.lastError = err.Error()
			batch.probeState = "failed"
			ap.signalChangedLocked()
		}
		ap.mu.Unlock()
		if active && onComplete != nil {
			onComplete(0, batch.target, err)
		}
	}
	setupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	account, err := s.accountRepo.GetByID(setupCtx, accountID)
	if err != nil {
		fail(err)
		return
	}
	if openAICodexCookieHostFromAccount(account) != host {
		fail(errors.New("Cookie binding changed"))
		return
	}
	token, _, err := s.GetAccessToken(setupCtx, account)
	if err != nil {
		fail(err)
		return
	}
	probeID, err := s.probeCookieWSAstra(setupCtx, account, token, cookie, pool.cookieProbeModel())
	if err != nil {
		fail(fmt.Errorf("Astra probe failed: %w", err))
		return
	}
	ap.mu.Lock()
	if ap.cookieBatch != batch {
		ap.mu.Unlock()
		return
	}
	batch.probeState = "ready"
	batch.probeID = probeID
	ap.signalChangedLocked()
	ap.mu.Unlock()
	wsURL, err := s.buildOpenAIResponsesWSURL(account)
	if err != nil {
		fail(err)
		return
	}
	headers, _, err := s.buildOpenAIWSHeaders(setupCtx, nil, account, token, OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, "", "", "", "", "")
	if err != nil {
		fail(err)
		return
	}
	// Freeze the selected library Cookie for this batch; library refreshes must
	// not silently move existing connections to a different cookie/session.
	headers.Set("Cookie", mergeOpenAICodexCookieHeaders("", cookie))
	proxy := ""
	if account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	req := openAIWSAcquireRequest{Account: account, WSURL: wsURL, Headers: headers, ProxyURL: proxy,
		HeadersFactory: func(ctx context.Context, h http.Header) (http.Header, error) {
			refreshed, err := s.refreshOpenAIAgentIdentityHeaders(ctx, account, h)
			if err != nil {
				return nil, err
			}
			// Background pool dials have no client request context. Give every
			// socket its own Codex session so the upstream can route its initial
			// response.create and keep continuation state connection-local.
			refreshed.Set("session_id", uuid.NewString())
			return refreshed, nil
		},
	}
	ap.mu.Lock()
	if ap.cookieBatch != batch {
		ap.mu.Unlock()
		return
	}
	batch.preparing = false
	ap.mu.Unlock()
	var workers sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i := 0; i < batch.target; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			dialCtx, cancel := context.WithTimeout(ctx, pool.dialTimeout())
			conn, err := pool.dialConn(dialCtx, req)
			if err == nil && conn != nil {
				// The HTTP probe response is connection-independent. Responses API
				// continuation IDs are cached on the WS that created them, so each
				// socket must establish its own initial Astra response before it is
				// admitted to the pool.
				err = s.warmCookieWSConnection(dialCtx, pool, conn, account, pool.cookieProbeModel())
				if err != nil {
					conn.close()
					conn = nil
				}
			}
			cancel()
			ap.mu.Lock()
			if ap.cookieBatch != batch {
				ap.mu.Unlock()
				if conn != nil {
					conn.close()
				}
				return
			}
			batch.pending--
			if err != nil {
				batch.lastError = err.Error()
			} else if conn != nil && !conn.isClosed() && ctx.Err() == nil {
				ap.conns[conn.id] = conn
				pool.metrics.acquireCreateTotal.Add(1)
			} else if conn != nil {
				ap.mu.Unlock()
				conn.close()
				ap.mu.Lock()
			}
			ap.signalChangedLocked()
			ap.mu.Unlock()
		}()
	}
	workers.Wait()
	ap.mu.Lock()
	active := ap.cookieBatch == batch
	live := len(ap.conns)
	lastError := batch.lastError
	if ap.cookieBatch == batch {
		batch.pending = 0
		ap.signalChangedLocked()
	}
	ap.mu.Unlock()
	if active && onComplete != nil {
		if live == 0 {
			if lastError == "" {
				if ctx.Err() != nil {
					lastError = ctx.Err().Error()
				} else {
					lastError = "no WS connection was established"
				}
			}
			onComplete(0, batch.target, errors.New(lastError))
		} else {
			onComplete(live, batch.target, nil)
		}
	}
}

func (p *openAIWSConnPool) cookieProbeModel() string {
	return openAICookieWSProbeModel
}

func (s *OpenAIGatewayService) probeCookieWSAstra(ctx context.Context, account *Account, token, cookie, model string) (string, error) {
	if s == nil || account == nil || s.httpUpstream == nil {
		return "", errors.New("HTTP upstream unavailable")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "gpt-6-astra"
	}
	logOpenAIWSModeInfo("cookie_ws_astra_probe_start account_id=%d model=%s", account.ID, truncateOpenAIWSLogValue(model, openAIWSLogValueMaxLen))
	body, err := json.Marshal(map[string]any{
		"model":               model,
		"store":               false,
		"stream":              true,
		"instructions":        "Reply with exactly: pong. Do not call tools.",
		"parallel_tool_calls": false,
		"reasoning":           map[string]any{"context": "all_turns"},
		"input":               []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Reply with exactly: pong."}}}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Close = true
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set(responsesLiteHeaderKey, "true")
	req.Header.Set("session_id", uuid.NewString())
	req.Header.Set("Cookie", mergeOpenAICodexCookieHeaders("", cookie))
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, account); err != nil {
		return "", err
	}
	applyOpenAICodexTicketHarvestIdentity(req.Header, model, account.GetOpenAIUserAgent())
	deleteOpenAIHeaderEqualFold(req.Header, openAICodexTurnStateHeader)
	deleteOpenAIHeaderEqualFold(req.Header, "session-id")
	// The selected library Cookie is authoritative for this probe.
	req.Header.Set("Cookie", mergeOpenAICodexCookieHeaders("", cookie))
	proxy := ""
	if account.Proxy != nil {
		proxy = strings.TrimSpace(account.Proxy.URL())
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", errors.New("empty Astra probe response")
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status=%d body=%s", resp.StatusCode, truncateOpenAIWSLogValue(string(responseBody), 512))
	}
	responseID := ""
	forEachOpenAISSEDataPayload(string(responseBody), func(data []byte) {
		if responseID != "" || !gjson.ValidBytes(data) {
			return
		}
		responseID = strings.TrimSpace(gjson.GetBytes(data, "response.id").String())
		if responseID == "" {
			responseID = strings.TrimSpace(gjson.GetBytes(data, "id").String())
		}
	})
	if responseID == "" && gjson.ValidBytes(responseBody) {
		responseID = strings.TrimSpace(gjson.GetBytes(responseBody, "response.id").String())
		if responseID == "" {
			responseID = strings.TrimSpace(gjson.GetBytes(responseBody, "id").String())
		}
	}
	if responseID == "" {
		return "", errors.New("Astra probe returned no response id")
	}
	logOpenAIWSModeInfo("cookie_ws_astra_probe_done account_id=%d response_id=%s", account.ID, truncateOpenAIWSLogValue(responseID, openAIWSIDValueMaxLen))
	return responseID, nil
}

func (s *OpenAIGatewayService) warmCookieWSConnection(ctx context.Context, pool *openAIWSConnPool, conn *openAIWSConn, account *Account, model string) error {
	if conn == nil || pool == nil || account == nil {
		return errors.New("invalid Cookie WS warmup state")
	}
	if !conn.tryAcquire() {
		return errors.New("new Cookie WS connection unavailable")
	}
	lease := &openAIWSConnLease{pool: pool, accountID: account.ID, conn: conn}
	defer lease.Release()
	payload := map[string]any{
		"type":                "response.create",
		"model":               model,
		"stream":              true,
		"store":               false,
		"instructions":        "Reply with exactly: pong. Do not call tools.",
		"parallel_tool_calls": false,
		"reasoning":           map[string]any{"context": "all_turns"},
		"input":               []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Reply with exactly: pong."}}}},
	}
	logOpenAIWSModeInfo("cookie_ws_session_warmup_start account_id=%d conn_id=%s previous_response_id=null", account.ID, conn.id)
	if err := lease.WriteJSONWithContextTimeout(ctx, payload, s.openAIWSWriteTimeout()); err != nil {
		return fmt.Errorf("warmup write: %w", err)
	}
	warmupResponseID := ""
	for {
		message, err := lease.ReadMessageWithContextTimeout(ctx, s.openAIWSReadTimeout())
		if err != nil {
			return fmt.Errorf("warmup read: %w", err)
		}
		eventType, responseID, _ := parseOpenAIWSEventEnvelope(message)
		if responseID != "" {
			warmupResponseID = responseID
		}
		if eventType == "error" {
			code, typ, msg := parseOpenAIWSErrorEventFields(message)
			logOpenAIWSModeInfo("cookie_ws_session_warmup_rejected account_id=%d conn_id=%s code=%s type=%s message=%s", account.ID, conn.id, truncateOpenAIWSLogValue(code, openAIWSLogValueMaxLen), truncateOpenAIWSLogValue(typ, openAIWSLogValueMaxLen), truncateOpenAIWSLogValue(msg, openAIWSLogValueMaxLen))
			if msg == "" {
				msg = "upstream rejected Cookie WS warmup"
			}
			return fmt.Errorf("upstream rejected Cookie WS warmup: %s", msg)
		}
		if isOpenAIWSTerminalEvent(eventType) {
			lease.MarkPrewarmed()
			logOpenAIWSModeInfo("cookie_ws_session_warmup_done account_id=%d conn_id=%s response_id=%s previous_response_id=null", account.ID, conn.id, truncateOpenAIWSLogValue(warmupResponseID, openAIWSIDValueMaxLen))
			return nil
		}
	}
}

// A Cookie-bound inference request can only borrow connections created by the
// explicit binding batch. No dial, adaptive prewarm, or replacement is allowed.
func (p *openAIWSConnPool) acquireCookieWS(ctx context.Context, req openAIWSAcquireRequest) (*openAIWSConnLease, error) {
	if req.ForcePreferredConn && strings.TrimSpace(req.PreferredConnID) == "" {
		return nil, errOpenAIWSPreferredConnUnavailable
	}
	ap, ok := p.getAccountPool(req.Account.ID)
	if !ok {
		return nil, errCookieWSPoolUnavailable
	}
	started := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ap.mu.Lock()
		batch := ap.cookieBatch
		if batch == nil || batch.host != openAICodexCookieHostFromAccount(req.Account) || !batch.expires.After(time.Now()) {
			ap.mu.Unlock()
			return nil, errCookieWSPoolUnavailable
		}
		var lease *openAIWSConnLease
		var dead []*openAIWSConn
		preferred := strings.TrimSpace(req.PreferredConnID)
		choose := func(conn *openAIWSConn) {
			if conn == nil || conn.isClosed() || conn.isUnusable() || conn.readerLoopClosedByPeer() {
				return
			}
			if lease == nil && (!req.ForcePreferredConn || conn.id == preferred) && conn.tryAcquire() {
				lease = &openAIWSConnLease{pool: p, accountID: req.Account.ID, conn: conn, reused: true, queueWait: time.Since(started)}
				lease.idleBefore, lease.ageBefore = conn.idleDuration(time.Now()), conn.age(time.Now())
			}
		}
		if preferred != "" {
			choose(ap.conns[preferred])
		}
		for id, conn := range ap.conns {
			if id != preferred {
				choose(conn)
			}
			if conn == nil || conn.isClosed() || conn.isUnusable() || conn.readerLoopClosedByPeer() {
				delete(ap.conns, id)
				delete(ap.pinnedConns, id)
				if conn != nil {
					dead = append(dead, conn)
				}
			}
		}
		pending := batch.pending
		live := len(ap.conns)
		changed := ap.changeChannelLocked()
		ap.mu.Unlock()
		closeOpenAIWSConns(dead)
		if lease != nil {
			if err := ctx.Err(); err != nil {
				lease.Release()
				return nil, err
			}
			p.metrics.acquireReuseTotal.Add(1)
			return lease, nil
		}
		if req.ForcePreferredConn && preferred != "" {
			ap.mu.Lock()
			_, exists := ap.conns[preferred]
			ap.mu.Unlock()
			if !exists {
				return nil, errOpenAIWSPreferredConnUnavailable
			}
		}
		if live == 0 && pending == 0 {
			return nil, errCookieWSPoolUnavailable
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
