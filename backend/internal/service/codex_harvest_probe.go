package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/google/uuid"
)

type codexHarvestProbeResult struct {
	State      string
	Status     int
	RetryAfter time.Duration
	Err        error
	Shape      openAICodexTicketShape
	Kind       string
	Sent       bool
	Cookies    []string
}

func responseCookiePairs(resp *http.Response) []string {
	if resp == nil {
		return nil
	}
	out := make([]string, 0, min(len(resp.Cookies()), 32))
	total := 0
	for _, cookie := range resp.Cookies() {
		if cookie.Name != "" && cookie.Value != "" {
			pair := cookie.Name + "=" + cookie.Value
			if len(out) >= 32 || total+len(pair) > 8192 {
				break
			}
			out = append(out, pair)
			total += len(pair)
		}
	}
	return out
}

func mergeCookiePairs(existing, incoming []string) []string {
	merged := make(map[string]string, len(existing)+len(incoming))
	for _, pairs := range [][]string{existing, incoming} {
		for _, pair := range pairs {
			name, value, ok := strings.Cut(pair, "=")
			name = strings.TrimSpace(name)
			if !ok || name == "" || value == "" {
				continue
			}
			merged[name] = value
		}
	}
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+merged[name])
	}
	return out
}

func bindCodexHarvestEgress(ticket *openAICodexTicket, attempt codexHarvestAttempt, session string) {
	if ticket == nil {
		return
	}
	ticket.HarvestProxyURL = strings.TrimSpace(attempt.proxy)
	ticket.HarvestNodeID = strings.TrimSpace(attempt.node.ID)
	ticket.HarvestNodeName = strings.TrimSpace(attempt.node.Name)
	ticket.HarvestNodeProvider = strings.TrimSpace(attempt.node.Provider)
	ticket.HarvestSessionID = strings.TrimSpace(session)
}

func (s *OpenAIGatewayService) harvestAttemptSession(account *Account, model string, attempt codexHarvestAttempt) string {
	// Every new ping starts a new upstream conversation. Reusing the old
	// ticket's session (or caching one by proxy) makes a refreshed ticket look
	// like a continuation of the previous lineage and can turn a 292 into 312.
	return uuid.NewString()
}

func (s *OpenAIGatewayService) executeCodexHarvestProbe(ctx context.Context, account *Account, token, model, proxy string, timeout time.Duration, reserve func() bool, sessionID string) (result codexHarvestProbeResult) {
	release, err := mihomo.Lease(ctx, proxy)
	if err != nil {
		return codexHarvestProbeResult{Err: err, Kind: "network_error"}
	}
	defer func() { release(result.Kind == "success") }()
	attempt, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if strings.TrimSpace(sessionID) == "" {
		sessionID = uuid.NewString()
	}
	result = s.requestCodexHarvestProbe(attempt, account, token, model, proxy, reserve, sessionID)
	result.Shape, result.Kind = classifyCodexHarvestProbe(ctx, account, s.openAICodexTicketConfig(), result)
	return result
}

func (s *OpenAIGatewayService) requestCodexHarvestProbe(ctx context.Context, account *Account, token, model, proxy string, reserve func() bool, sessionID string) (out codexHarvestProbeResult) {
	body := []byte(`{"model":` + jsonString(model) + `,"store":false,"stream":true,"instructions":"Reply with exactly: pong","input":[{"role":"user","content":[{"type":"input_text","text":"ping"}]}]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
	if err != nil {
		out.Err = err
		return
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAIHarvest))
	req.Close = true
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	if strings.TrimSpace(sessionID) == "" {
		sessionID = uuid.NewString()
	}
	req.Header.Set("session_id", sessionID)
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, account); err != nil {
		out.Err = err
		return
	}
	if previous := s.lookupOpenAICodexTicket(account, model); codexTicketCookiesFresh(previous, time.Now()) {
		req.Header.Set("Cookie", strings.Join(previous.HarvestCookies, "; "))
	}
	applyOpenAICodexTicketHarvestIdentity(req.Header, model)
	if ctx.Err() != nil {
		out.Err = ctx.Err()
		return
	}
	if reserve != nil && !reserve() {
		out.Err = errors.New("harvest request no longer admitted")
		return
	}
	out.Sent = true
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		out.Err = err
		return
	}
	if resp == nil {
		out.Err = errors.New("nil upstream response")
		return
	}
	out.Status = resp.StatusCode
	out.State = extractOpenAICodexTurnState(resp.Header)
	out.Cookies = responseCookiePairs(resp)
	out.RetryAfter = codexHarvestRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	if resp.Body == nil {
		out.Err = errors.New("probe response body missing")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	response, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(response) > 1<<20 {
		out.Err = errors.New("probe response incomplete")
		return
	}
	if out.Status == http.StatusOK {
		out.Err = validateCodexProbeResponse(response)
	}
	return
}

func classifyCodexHarvestProbe(ctx context.Context, account *Account, cfg config.OpenAICodexTicketConfig, result codexHarvestProbeResult) (openAICodexTicketShape, string) {
	shape, err := parseOpenAICodexTicketShape(result.State)
	switch {
	case ctx.Err() != nil:
		return shape, "cancelled"
	case !result.Sent:
		return shape, "not_sent"
	case result.Status == http.StatusUnauthorized || result.Status == http.StatusForbidden:
		return shape, "account_error"
	case result.Status == http.StatusTooManyRequests:
		return shape, "rate_limited"
	case result.Status == 0:
		return shape, "network_error"
	case result.Status != http.StatusOK:
		return shape, "upstream_error"
	case result.Err != nil:
		return shape, "response_incomplete_or_error"
	}
	now := time.Now()
	if err != nil || shape.Blocks != openAICodexTicketExpectedBlocks(account) || len(result.State) != openAICodexTicketTargetLength(account, cfg) || !strings.HasPrefix(result.State, openAICodexTicketStatePrefix) || shape.IssuedAt.After(now.Add(30*time.Second)) || !now.Before(shape.IssuedAt.Add(time.Hour-30*time.Second)) {
		return shape, "invalid_state"
	}
	return shape, "success"
}

func codexHarvestRetryAfter(raw string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && seconds > 0 {
		return time.Duration(min(seconds, 86400)) * time.Second
	}
	if date, err := http.ParseTime(raw); err == nil && date.After(now) {
		return min(date.Sub(now), 24*time.Hour)
	}
	return 0
}

func codexHarvestTicket(account *Account, model string, r codexHarvestProbeResult, cfg config.OpenAICodexTicketConfig, attempts int) *openAICodexTicket {
	now := time.Now()
	expires := now.Add(time.Duration(cfg.TTLSeconds) * time.Second)
	if issuedExpiry := r.Shape.IssuedAt.Add(time.Hour - 30*time.Second); issuedExpiry.Before(expires) {
		expires = issuedExpiry
	}
	return &openAICodexTicket{AccountID: account.ID, Model: model, State: r.State, Length: len(r.State),
		CapturedAt: now, ExpiresAt: expires, Attempts: attempts, Blocks: r.Shape.Blocks, IssuedAt: r.Shape.IssuedAt,
		HarvestCookies: append([]string(nil), r.Cookies...), HarvestCookiesAt: now}
}
