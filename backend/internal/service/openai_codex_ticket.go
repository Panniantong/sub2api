package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	openAICodexTicketExtraKey             = "codex_turn_ticket"
	openAICodexTicketHistoryExtraKey      = "codex_turn_ticket_history"
	openAICodexSessionIDExtraKey          = "session_id"
	openAICodexCookieExtraKey             = "codex_turn_cookie"
	openAICodexCookieHostExtraKey         = "codex_cookie_host"
	openAICodexCookieCooldownsExtraKey    = "codex_cookie_host_cooldowns"
	openAICodexCookieRotationNextExtraKey = "codex_cookie_rotation_next_at"
	openAICodexTicketStatePrefix          = "gAAAAA"
	openAICodexRequiredTicketLength       = 780
	openAICodexTicketHarvestModel         = "gpt-6-astra"
	openAICodexCookieValidationPrompt     = "don't search the intezrnet, do you know Thibault Sottiaux on X. answer yes or no"
	openAICookieProxyDiscoveryRequests    = 100
	cookieProxyHostBindingsKey            = "openai_cookie_proxy_host_bindings"
	openAICookiePersistTimeout            = 10 * time.Second
	openAICookiePostResponseTimeout       = 5 * time.Minute
)

type openAICookieProxyHostBindingState struct {
	Stats        CookieProxyHarvestStats `json:"stats"`
	BackoffUntil time.Time               `json:"backoff_until,omitempty"`
	Requests     int                     `json:"requests"`
	Hosts        map[string]int          `json:"hosts"`
	History      map[string]int          `json:"history,omitempty"`
	UpdatedAt    time.Time               `json:"updated_at,omitempty"`
}

type OpenAICookieProxyHostMemory struct {
	Stats             CookieProxyHarvestStats            `json:"stats"`
	BackoffUntil      time.Time                          `json:"backoff_until,omitempty"`
	SuccessfulSamples int                                `json:"successful_samples"`
	SampleTarget      int                                `json:"sample_target"`
	Proxy             string                             `json:"proxy"`
	ProxyUsername     string                             `json:"proxy_username,omitempty"`
	Requests          int                                `json:"requests"`
	Limit             int                                `json:"limit"`
	Completed         bool                               `json:"completed"`
	Hosts             []OpenAICookieProxyHostMemoryEntry `json:"hosts"`
	UpdatedAt         time.Time                          `json:"updated_at,omitempty"`
}

type OpenAICookieProxyHostMemoryEntry struct {
	Host  string `json:"host"`
	Count int    `json:"count"`
}

var openAICookieProxyHostBindingsMu sync.Mutex

func newOpenAICookiePostResponseContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, openAICookiePostResponseTimeout)
}

func firstOpenAICodexTicketModel(models []string) string {
	for _, model := range models {
		if model = strings.TrimSpace(model); model != "" {
			return model
		}
	}
	return openAICodexTicketHarvestModel
}

// OpenAICodexTicketConfigDefaults returns the effective values used when a
// deployment omits optional ticket settings.
func OpenAICodexTicketConfigDefaults(cfg config.OpenAICodexTicketConfig) config.OpenAICodexTicketConfig {
	// A zero-value config is commonly used by embedded callers/tests. Keep the
	// historical behavior for that case; real config loading supplies the
	// explicit default (true), while an explicit false with a configured TTL is
	// preserved.
	if cfg.TTLSeconds == 0 {
		cfg.OverrideTurnState = true
	}
	if cfg.TTLSeconds <= 0 {
		cfg.TTLSeconds = 3600
	}
	if cfg.RefreshBeforeSeconds <= 0 {
		cfg.RefreshBeforeSeconds = 600
	}
	// The account binding contract is tied to the 780-byte Codex ticket. Force
	// this value even when an older deployment still has target_length: 332 in
	// its static config, so a binary-only upgrade cannot keep rejecting 780.
	cfg.TargetLength = openAICodexRequiredTicketLength
	if cfg.HarvestProbeIntervalSeconds <= 0 {
		cfg.HarvestProbeIntervalSeconds = 5
	}
	if cfg.HarvestAttemptTimeoutSeconds <= 0 {
		cfg.HarvestAttemptTimeoutSeconds = 25
	}
	if cfg.CookieWSConnections <= 0 {
		cfg.CookieWSConnections = 10
	}
	if cfg.CookieWSConnectionTTLSeconds <= 0 {
		cfg.CookieWSConnectionTTLSeconds = 3600
	}
	if cfg.CookieWSHostCooldownSeconds <= 0 {
		cfg.CookieWSHostCooldownSeconds = 14400
	}
	if cfg.CookieProxyLearningAttempts <= 0 {
		cfg.CookieProxyLearningAttempts = openAICookieProxyDiscoveryRequests
	}
	if cfg.RelayTimeoutSeconds <= 0 {
		cfg.RelayTimeoutSeconds = 75
	}
	cfg.RelayURL = strings.TrimSpace(cfg.RelayURL)
	cfg.RelayKey = strings.TrimSpace(cfg.RelayKey)
	cfg.RelayMode = strings.ToLower(strings.TrimSpace(cfg.RelayMode))
	if cfg.RelayMode == "" {
		cfg.RelayMode = "mint"
	}
	return cfg
}

type openAICodexTicket struct {
	State      string    `json:"state"`
	Length     int       `json:"length"`
	CapturedAt time.Time `json:"captured_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// OpenAICodexTicketStatus is the administrator-facing current ticket state.
type OpenAICodexTicketStatus struct {
	Ready                   bool           `json:"ready"`
	Ticket                  string         `json:"ticket,omitempty"`
	Cookie                  string         `json:"cookie,omitempty"`
	CookieHost              string         `json:"cookie_host,omitempty"`
	CookiePayload           map[string]any `json:"cookie_payload,omitempty"`
	BoundCookieHost         string         `json:"bound_cookie_host,omitempty"`
	WSConnectionCount       int            `json:"ws_connection_count,omitempty"`
	WSStartedAt             *time.Time     `json:"ws_started_at,omitempty"`
	WSExpiresAt             *time.Time     `json:"ws_expires_at,omitempty"`
	CookieHostCooldownUntil *time.Time     `json:"cookie_host_cooldown_until,omitempty"`
	SessionID               string         `json:"session_id,omitempty"`
	HistoryCount            int            `json:"history_count,omitempty"`
	Length                  int            `json:"length,omitempty"`
	RemainingSeconds        int64          `json:"remaining_seconds"`
	ExpiresAt               *time.Time     `json:"expires_at,omitempty"`
}

type OpenAICodexTicketHistory struct {
	Ticket        string         `json:"ticket,omitempty"`
	RequestState  string         `json:"request_state,omitempty"`
	ResponseState string         `json:"response_state,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	Cookie        string         `json:"cookie,omitempty"`
	CookieHost    string         `json:"cookie_host,omitempty"`
	CookiePayload map[string]any `json:"cookie_payload,omitempty"`
	Response      string         `json:"response,omitempty"`
	CapturedAt    time.Time      `json:"captured_at"`
	ExpiresAt     time.Time      `json:"expires_at,omitempty"`
	Source        string         `json:"source,omitempty"`
	StatusCode    int            `json:"status_code,omitempty"`
}

const openAICodexTicketHistoryLimit = 50

func OpenAICodexTicketHistories(account *Account) []OpenAICodexTicketHistory {
	if account == nil || account.Extra == nil {
		return nil
	}
	raw, ok := account.Extra[openAICodexTicketHistoryExtraKey]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var items []OpenAICodexTicketHistory
	if json.Unmarshal(b, &items) != nil || len(items) == 0 {
		return nil
	}
	cleaned := make([]OpenAICodexTicketHistory, 0, len(items))
	for _, item := range items {
		item.Ticket = strings.TrimSpace(item.Ticket)
		item.RequestState = strings.TrimSpace(item.RequestState)
		item.ResponseState = strings.TrimSpace(item.ResponseState)
		// Records written before request/response state split only had ticket.
		// Treat that value as the response state for a lossless upgrade.
		if item.ResponseState == "" {
			item.ResponseState = item.Ticket
		}
		item.SessionID = strings.TrimSpace(item.SessionID)
		item.Cookie = strings.TrimSpace(item.Cookie)
		item.Response = strings.TrimSpace(item.Response)
		item.Source = strings.TrimSpace(item.Source)
		// Only an exact 780-byte ticket makes the response Cookie authoritative.
		// Older builds copied the account-bound Cookie into every observation.
		if !plausibleOpenAICodexTicket(item.Ticket, openAICodexRequiredTicketLength) {
			item.Cookie = ""
		}
		item.CookieHost, item.CookiePayload = openAICodexCookieJWTInfo(item.Cookie)
		if len(cleaned) > 0 && repeatedOpenAICodexTicketFailure(cleaned[len(cleaned)-1], item) {
			cleaned[len(cleaned)-1] = item
			continue
		}
		cleaned = append(cleaned, item)
	}
	items = cleaned
	if len(items) > openAICodexTicketHistoryLimit {
		items = items[len(items)-openAICodexTicketHistoryLimit:]
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}

func repeatedOpenAICodexTicketFailure(previous, current OpenAICodexTicketHistory) bool {
	return previous.StatusCode >= http.StatusBadRequest && current.StatusCode >= http.StatusBadRequest &&
		previous.Ticket == "" && current.Ticket == "" &&
		previous.SessionID == current.SessionID && previous.Cookie == current.Cookie &&
		previous.Source == current.Source && previous.StatusCode == current.StatusCode
}

func (s *OpenAIGatewayService) recordOpenAICodexTicketObservation(account *Account, ticket, sessionID, cookie, source string, statusCode int, capturedAt, expiresAt time.Time) {
	s.recordOpenAICodexTicketObservationWithStates(account, "", ticket, ticket, sessionID, cookie, source, statusCode, capturedAt, expiresAt)
}

func (s *OpenAIGatewayService) recordOpenAICodexTicketObservationWithStates(account *Account, requestState, responseState, ticket, sessionID, cookie, source string, statusCode int, capturedAt, expiresAt time.Time) {
	s.recordOpenAICodexTicketObservationWithResponse(account, requestState, responseState, ticket, sessionID, cookie, source, statusCode, capturedAt, expiresAt, "")
}

func (s *OpenAIGatewayService) recordOpenAICodexTicketObservationWithResponse(account *Account, requestState, responseState, ticket, sessionID, cookie, source string, statusCode int, capturedAt, expiresAt time.Time, response string) {
	if s == nil || account == nil {
		return
	}
	requestState = strings.TrimSpace(requestState)
	responseState = strings.TrimSpace(responseState)
	ticket = strings.TrimSpace(ticket)
	if responseState == "" {
		responseState = ticket
	}
	if ticket == "" {
		ticket = responseState
	}
	entry := OpenAICodexTicketHistory{Ticket: ticket, RequestState: requestState, ResponseState: responseState, SessionID: strings.TrimSpace(sessionID), Cookie: strings.TrimSpace(cookie), Response: strings.TrimSpace(response), CapturedAt: capturedAt, ExpiresAt: expiresAt, Source: source, StatusCode: statusCode}
	s.openaiCodexTicketHistoryMu.Lock()
	defer s.openaiCodexTicketHistoryMu.Unlock()
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	history := OpenAICodexTicketHistories(account)
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}
	if len(history) > 0 {
		previous := history[len(history)-1]
		if repeatedOpenAICodexTicketFailure(previous, entry) {
			return
		}
	}
	history = append(history, entry)
	if len(history) > openAICodexTicketHistoryLimit {
		history = history[len(history)-openAICodexTicketHistoryLimit:]
	}
	account.Extra[openAICodexTicketHistoryExtraKey] = history
	if s.accountRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{openAICodexTicketHistoryExtraKey: history}); err != nil {
			slog.Warn("openai codex ticket history persist failed", "account_id", account.ID, "error", err)
		}
	}
}

func isOpenAICodexTicketAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.IsOpenAIOAuthLike() && !account.IsShadow()
}

func candidateOpenAICodexTicket(state string) bool {
	state = strings.TrimSpace(state)
	if !strings.HasPrefix(state, openAICodexTicketStatePrefix) {
		return false
	}
	// 780-byte turn-state values are currently returned by Codex. Keep the
	// candidate envelope wider so the exact configured length can be checked by
	// plausibleOpenAICodexTicket without rejecting the response too early.
	return len(state) >= 292 && len(state) <= 4096
}

func plausibleOpenAICodexTicket(state string, _ int) bool {
	state = strings.TrimSpace(state)
	return candidateOpenAICodexTicket(state) && len(state) == openAICodexRequiredTicketLength
}

func (t *openAICodexTicket) valid(now time.Time, minLength int) bool {
	return t != nil && plausibleOpenAICodexTicket(t.State, minLength) && !t.ExpiresAt.IsZero() && now.Before(t.ExpiresAt)
}

func (t *openAICodexTicket) needsRefresh(now time.Time, before time.Duration) bool {
	return t == nil || t.ExpiresAt.IsZero() || !t.ExpiresAt.After(now.Add(before))
}

func OpenAICodexTicketStatuses(account *Account, cfg config.OpenAICodexTicketConfig, now time.Time) []OpenAICodexTicketStatus {
	if !isOpenAICodexTicketAccount(account) {
		return nil
	}
	cfg = OpenAICodexTicketConfigDefaults(cfg)
	status := OpenAICodexTicketStatus{HistoryCount: len(OpenAICodexTicketHistories(account)), Cookie: openAICodexCookieFromAccount(account), SessionID: account.GetOpenAISessionID(), BoundCookieHost: openAICodexCookieHostFromAccount(account)}
	if account.Extra != nil {
		if raw := strings.TrimSpace(account.GetExtraString("codex_cookie_ws_connection_count")); raw != "" {
			if count, err := strconv.Atoi(raw); err == nil && count > 0 {
				status.WSConnectionCount = count
			}
		}
		for key, target := range map[string]**time.Time{"codex_cookie_ws_started_at": &status.WSStartedAt, "codex_cookie_ws_expires_at": &status.WSExpiresAt, "codex_cookie_host_cooldown_until": &status.CookieHostCooldownUntil} {
			if raw := strings.TrimSpace(account.GetExtraString(key)); raw != "" {
				if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
					*target = &parsed
				}
			}
		}
	}
	status.CookieHost, status.CookiePayload = openAICodexCookieJWTInfo(status.Cookie)
	if account.Extra != nil {
		if raw, ok := account.Extra[openAICodexTicketExtraKey]; ok {
			if b, err := json.Marshal(raw); err == nil {
				var ticket openAICodexTicket
				if json.Unmarshal(b, &ticket) == nil && candidateOpenAICodexTicket(ticket.State) {
					status.Ready = ticket.valid(now, cfg.TargetLength)
					status.Ticket = ticket.State
					status.Length = len(strings.TrimSpace(ticket.State))
					remaining := int64(ticket.ExpiresAt.Sub(now) / time.Second)
					if remaining < 0 {
						remaining = 0
					}
					status.RemainingSeconds = remaining
					expires := ticket.ExpiresAt
					status.ExpiresAt = &expires
				}
			}
		}
	}
	return []OpenAICodexTicketStatus{status}
}

func (s *OpenAIGatewayService) codexTicketConfig() config.OpenAICodexTicketConfig {
	if s == nil || s.cfg == nil {
		return config.OpenAICodexTicketConfig{}
	}
	cfg := OpenAICodexTicketConfigDefaults(s.cfg.Gateway.OpenAICodexTicket)
	if s.settingService != nil {
		if runtime, err := s.settingService.GetOpenAICodexTicketSettings(context.Background()); err == nil && runtime != nil {
			cfg.Enabled = runtime.Enabled
			cfg.TTLSeconds = runtime.TTLSeconds
			cfg.RefreshBeforeSeconds = runtime.RefreshBeforeSeconds
			cfg.HarvestProbeIntervalSeconds = runtime.RetryIntervalSeconds
			cfg.HarvestProxyURL = runtime.HarvestProxyURL
			cfg.Models = []string{runtime.Model}
			cfg.CookieHostWhitelist = runtime.CookieHostWhitelist
			cfg.CookieHarvestProxyURLs = runtime.CookieHarvestProxyURLs
			// Cookie harvesting runtime settings are stored separately from the
			// legacy ticket settings; this flag is copied by the harvester below.
			cfg.OverrideTurnState = runtime.OverrideTurnState
			cfg.CookieWSConnections = runtime.CookieWSConnections
			cfg.CookieWSConnectionTTLSeconds = runtime.CookieWSConnectionTTLSeconds
			cfg.CookieWSHostCooldownSeconds = runtime.CookieWSHostCooldownSeconds
			cfg.RelayEnabled = runtime.RelayEnabled
			cfg.RelayURL = runtime.RelayURL
			cfg.RelayKey = runtime.RelayKey
			cfg.RelayMode = runtime.RelayMode
			cfg.RelayTimeoutSeconds = runtime.RelayTimeoutSeconds
			cfg.RelayAllowMint = runtime.RelayAllowMint
		}
	}
	return OpenAICodexTicketConfigDefaults(cfg)
}

func (s *OpenAIGatewayService) startOpenAICodexTicketHarvester() {
	if s == nil || s.accountRepo == nil || s.httpUpstream == nil {
		return
	}
	s.openaiCodexTicketMu.Lock()
	if s.openaiCodexTicketDone != nil {
		s.openaiCodexTicketMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.openaiCodexTicketCancel, s.openaiCodexTicketDone = cancel, done
	s.openaiCodexTicketMu.Unlock()
	go func() {
		defer close(done)
		var workers sync.WaitGroup
		workers.Add(1)
		go func() { defer workers.Done(); s.runOpenAICookieHarvester(ctx) }()
		defer workers.Wait()
		s.refreshOpenAICodexTickets(ctx)
		for {
			timer := time.NewTimer(time.Duration(s.codexTicketConfig().HarvestProbeIntervalSeconds) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				s.refreshOpenAICodexTickets(ctx)
			}
		}
	}()
}

// StopOpenAICodexTicketHarvester is safe to call during server shutdown.
func (s *OpenAIGatewayService) StopOpenAICodexTicketHarvester() {
	if s == nil {
		return
	}
	s.openaiCodexTicketMu.Lock()
	cancel, done := s.openaiCodexTicketCancel, s.openaiCodexTicketDone
	s.openaiCodexTicketCancel, s.openaiCodexTicketDone = nil, nil
	s.openaiCodexTicketMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *OpenAIGatewayService) refreshOpenAICodexTickets(ctx context.Context) {
	if s == nil || ctx.Err() != nil || !s.codexTicketConfig().Enabled {
		return
	}
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		slog.Warn("codex ticket account scan failed", "error", err)
		return
	}
	cfg := s.codexTicketConfig()
	for i := range accounts {
		account := &accounts[i]
		if account.Status != StatusActive || !isOpenAICodexTicketAccount(account) {
			continue
		}
		if settings, err := s.settingService.GetOpenAICookieSettings(ctx); err == nil && cookieHostMonitorOwns(account, settings) {
			continue
		}
		var ticket *openAICodexTicket
		if raw, ok := account.Extra[openAICodexTicketExtraKey]; ok {
			if b, e := json.Marshal(raw); e == nil {
				var candidate openAICodexTicket
				if json.Unmarshal(b, &candidate) == nil {
					ticket = &candidate
				}
			}
		}
		// Every valid 780-byte ticket follows the same refresh window.
		// Cookie acquisition uses its own worker and never changes this cadence.
		if ticket != nil && ticket.valid(time.Now(), cfg.TargetLength) && !ticket.needsRefresh(time.Now(), time.Duration(cfg.RefreshBeforeSeconds)*time.Second) {
			continue
		}
		s.probeOpenAICodexTicket(ctx, account, cfg)
	}
}

func (s *OpenAIGatewayService) probeOpenAICodexTicket(ctx context.Context, account *Account, cfg config.OpenAICodexTicketConfig, cookieSettings ...*OpenAICookieSettings) {
	var cookieLog *OpenAICookieAcquisitionLog
	if len(cookieSettings) > 0 && cookieSettings[0] != nil {
		cookieLog = &OpenAICookieAcquisitionLog{ID: uuid.NewString(), AccountID: account.ID, AccountName: account.Name, CreatedAt: time.Now(), Model: firstOpenAICodexTicketModel(cfg.Models), Message: "Cookie 请求未完成"}
		cookieLog.Task, cookieLog.TargetHost = cookieSettings[0].harvestTask, cookieSettings[0].harvestTarget
		defer func() {
			if cookieSettings[0].harvestProxy != "" {
				s.recordCookieHarvestOutcome(cookieSettings[0], cookieLog)
			}
			logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.settingService.appendOpenAICookieLog(logCtx, *cookieLog); err != nil {
				slog.Warn("cookie log persist failed", "error", err)
			}
		}()
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.HarvestAttemptTimeoutSeconds)*time.Second)
	defer cancel()
	token, _, err := s.GetAccessToken(probeCtx, account)
	if err != nil || strings.TrimSpace(token) == "" {
		if cookieLog != nil {
			cookieLog.Message = "获取账号凭据失败"
			if err != nil {
				cookieLog.Message += ": " + err.Error()
			}
		}
		return
	}
	// Every probe starts a fresh upstream session. The generated session_id is
	// sent only on this probe request and is persisted after a valid ticket is
	// accepted; no account-bound Cookie/ticket/session is reused here.
	probeSessionID := uuid.NewString()
	harvestModel := firstOpenAICodexTicketModel(cfg.Models)
	body, err := json.Marshal(map[string]any{
		"model":               harvestModel,
		"store":               false,
		"stream":              true,
		"instructions":        "Reply with exactly: pong. Do not call tools.",
		"parallel_tool_calls": false,
		"include":             []string{"reasoning.encrypted_content"},
		"reasoning":           map[string]any{"context": "all_turns"},
		"input": []any{
			map[string]any{
				"type": "additional_tools",
				"role": "developer",
				"tools": []any{
					map[string]any{
						"type":        "namespace",
						"name":        "codex",
						"description": "local tools",
						"tools": []any{
							map[string]any{
								"type":        "function",
								"name":        "noop",
								"description": "Do nothing.",
								"strict":      false,
								"parameters": map[string]any{
									"type":                 "object",
									"properties":           map[string]any{},
									"additionalProperties": false,
								},
							},
						},
					},
				},
			},
			map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"type": "input_text", "text": "ping"}},
			},
		},
	})
	if err != nil {
		return
	}
	// A scheduled proxy discovery must actually use its reserved proxy. Relay
	// mint has no selectable egress and cannot contribute proxy route samples.
	if cfg.RelayEnabled && cfg.RelayAllowMint && strings.EqualFold(strings.TrimSpace(cfg.RelayMode), "mint") && (cookieLog == nil || cookieSettings[0].harvestProxy == "") {
		relayResult, relayErr := s.mintOpenAICodexViaRelay(probeCtx, account, token, harvestModel, body, cfg)
		if relayErr == nil && s.acceptOpenAICodexRelayMint(probeCtx, account, token, harvestModel, probeSessionID, cfg, func() *OpenAICookieSettings {
			if len(cookieSettings) > 0 {
				return cookieSettings[0]
			}
			return nil
		}(), cookieLog, relayResult) {
			if cookieLog != nil && cookieLog.Message == "Relay Cookie 已保存，等待本地 Host 验证" {
				cookieLog.Message = "Relay mint 成功，Cookie 已进入本地 Host 验证流程"
			}
			return
		}
		if relayErr != nil {
			slog.Warn("openai_codex_relay_mint_failed_fallback_direct", "account_id", account.ID, "error", relayErr)
			if cookieLog != nil {
				cookieLog.Message = "Relay mint 失败，回退直连: " + relayErr.Error()
			}
		}
	}
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Close = true
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set(responsesLiteHeaderKey, "true")
	req.Header.Set("session_id", probeSessionID)
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(probeCtx, s.accountRepo, req.Header, account); err != nil {
		return
	}
	applyOpenAICodexTicketHarvestIdentity(req.Header, harvestModel, account.GetOpenAIUserAgent())
	// Harvesting must start a fresh upstream session. Account-bound ticket,
	// session and Cookie values are reserved for real forwarded requests.
	deleteOpenAIHeaderEqualFold(req.Header, openAICodexTurnStateHeader)
	deleteOpenAIHeaderEqualFold(req.Header, "session-id")
	deleteOpenAIHeaderEqualFold(req.Header, "Cookie")
	req.Header.Set("session_id", probeSessionID)
	proxyURL, proxySource := strings.TrimSpace(cfg.HarvestProxyURL), "dedicated"
	if cookieLog != nil {
		proxyURL, proxySource = s.nextOpenAICodexCookieHarvestProxy(probeCtx, cfg, account)
	} else if proxyURL == "" && account.Proxy != nil {
		proxyURL, proxySource = account.Proxy.URL(), "account"
	}
	learningAttempt := false
	if cookieLog != nil {
		cookieLog.SessionID = probeSessionID
		if u, err := url.Parse(proxyURL); err == nil {
			if u.User != nil {
				cookieLog.ProxyUsername = u.User.Username()
			}
			u.User = nil
			cookieLog.Proxy = u.String()
		}
	}
	if proxyURL == "" {
		if cookieLog != nil {
			cookieLog.Message = "未配置 Cookie 采集代理，且账号无代理"
		}
		slog.Warn("openai_codex_ticket_harvest_skipped_no_proxy", "account_id", account.ID)
		return
	}
	if cookieLog != nil {
		sampleTarget := defaultCookieHarvestPolicy().LearningSamples
		if cookieSettings[0].HarvestPolicy != nil {
			sampleTarget = cookieSettings[0].HarvestPolicy.LearningSamples
		}
		learningAttempt = s.recordOpenAICookieProxyAttempt(probeCtx, proxyURL, cfg.CookieProxyLearningAttempts, sampleTarget)
		cookieLog.harvestAttempted = true
	}
	proxyScheme, proxyHost := openAICodexHarvestProxyInfo(proxyURL)
	slog.Info("openai_codex_ticket_harvest_request",
		"account_id", account.ID,
		"model", harvestModel,
		"proxy_source", proxySource,
		"proxy_scheme", proxyScheme,
		"proxy_host", proxyHost,
	)
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil || resp == nil {
		if cookieLog != nil {
			cookieLog.Message = "Cookie 请求失败"
			if err != nil {
				cookieLog.Message += ": " + err.Error()
			}
		}
		slog.Warn("openai_codex_ticket_harvest_failed",
			"account_id", account.ID,
			"proxy_source", proxySource,
			"proxy_scheme", proxyScheme,
			"proxy_host", proxyHost,
			"error", err,
		)
		return
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	state := strings.TrimSpace(resp.Header.Get(openAICodexTurnStateHeader))
	// The generated probe session is the authoritative binding for a harvested
	// ticket; response session headers are retained only as diagnostics.
	sessionID := probeSessionID
	now := time.Now()
	responseCookie := ""
	responseSnapshot := ""
	// Cookie harvesting is independent from the 780-byte ticket contract. A
	// response may rotate __oailb even when its turn-state is missing or a
	// different length; the cookie pool must still retain that host entry.
	responseCookie = openAICodexResponseCookieHeader(resp)
	if !openAICodexCookieHostAllowed(responseCookie, cfg.CookieHostWhitelist) {
		responseCookie = ""
	}
	var responseBody []byte
	if resp.Body != nil {
		responseBody, _ = io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
		if len(responseBody) > 2<<20 {
			responseBody = responseBody[:2<<20]
		}
	}
	responseSnapshot = openAICodexResponseSnapshot(resp, responseBody)
	if cookieLog != nil {
		cookieLog.StatusCode, cookieLog.Response = resp.StatusCode, responseSnapshot
		cookieLog.Cookie = openAICodexResponseCookieHeader(resp)
		cookieLog.Host, cookieLog.Payload = openAICodexCookieJWTInfo(cookieLog.Cookie)
		cookieLog.Message = "响应未包含有效 Cookie host / exp"
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			cookieLog.Message = fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode)
			if resp.StatusCode == http.StatusUnauthorized {
				markOpenAICookieHarvestUnauthorized(probeCtx, s.accountRepo, account, responseBody)
			}
			return
		}
		if !openAICodexCookieHostAllowed(cookieLog.Cookie, cfg.CookieHostWhitelist) {
			cookieLog.Message = "host 不在 Cookie 白名单中"
			return
		}
		exp, ok := cookieLog.Payload["exp"].(float64)
		if cookieLog.Host == "" || !ok || exp <= float64(now.Unix()) {
			return
		}
		postResponseCtx, postResponseCancel := newOpenAICookiePostResponseContext(ctx)
		defer postResponseCancel()
		s.recordOpenAICookieProxyHost(postResponseCtx, proxyURL, cookieLog.Host, learningAttempt)
		entry := OpenAICodexCookieLibraryEntry{Host: cookieLog.Host, Cookie: cookieLog.Cookie, Payload: cookieLog.Payload, CapturedAt: now, ExpiresAt: time.Unix(int64(exp), 0)}
		persistCtx, persistCancel := context.WithTimeout(postResponseCtx, openAICookiePersistTimeout)
		err := s.settingService.UpsertOpenAICodexCookie(persistCtx, entry)
		persistCancel()
		if err != nil {
			cookieLog.Message = "Cookie 保存失败: " + err.Error()
			return
		}
		cookieLog.Success, cookieLog.Message = true, "已保存到 Cookie 库（按 host 去重）"
		// Acquisition and validation have separate logs. Only unbound accounts
		// start a validation attempt; bound/cooldown skips remain silent.
		boundHost := openAICodexCookieHostFromAccount(account)
		rotationDue := cookieSettings[0].CookieRotationEnabled && openAICookieRotationDue(account, now)
		if (cookieSettings[0].AutoValidateHost || cookieSettings[0].CookieRotationEnabled) &&
			(boundHost == "" || (rotationDue && boundHost != cookieLog.Host)) &&
			!openAICodexCookieHostCooldownUntil(account, cookieLog.Host).After(now) {
			_, _ = s.validateBindAndBuildOpenAICookieHost(postResponseCtx, account, cookieSettings[0], cookieLog.Host, responseCookie, proxyURL, harvestModel, token)
		}
		if cookieSettings[0].autoConfigureOtherAccounts {
			s.autoConfigureOpenAICookieHost(postResponseCtx, account, cookieSettings[0], cookieLog.Host, responseCookie, proxyURL, harvestModel)
		}
		return // Cookie acquisition never mutates account ticket/session or ticket logs.
	}
	ticket := s.storeOpenAICodexTicket(account, state, now, cfg)
	if resp.StatusCode != http.StatusOK || ticket == nil || !ticket.valid(now, cfg.TargetLength) {
		var expiresAt time.Time
		if ticket != nil {
			expiresAt = ticket.ExpiresAt
		}
		s.recordOpenAICodexTicketObservationWithResponse(account, "", state, state, sessionID, responseCookie, "harvest", resp.StatusCode, now, expiresAt, responseSnapshot)
		return
	}
	// Bind the generated probe session only after a valid ticket has been
	// harvested, so failed probes cannot replace a working session.
	resp.Header.Set("session_id", sessionID)
	s.captureOpenAIAccountSessionID(probeCtx, account, resp.Header)
	s.recordOpenAICodexTicketObservationWithResponse(account, "", state, state, sessionID, responseCookie, "harvest", resp.StatusCode, now, ticket.ExpiresAt, responseSnapshot)
}

// markOpenAICookieHarvestUnauthorized keeps the account list in sync with a
// rejected Cookie acquisition request. Cookie harvesting is an account-auth
// operation even though its result is displayed in the separate Cookie log;
// leaving the account active after a 401 causes the scheduler to keep retrying
// an invalid credential indefinitely.
func markOpenAICookieHarvestUnauthorized(ctx context.Context, repo AccountRepository, account *Account, responseBody []byte) {
	if account == nil {
		return
	}
	detail := strings.TrimSpace(string(responseBody))
	if len(detail) > 2048 {
		detail = detail[:2048]
	}
	errorMessage := "Authentication failed (401)"
	if detail != "" {
		errorMessage += ": " + detail
	}
	// Keep the in-memory snapshot consistent for the current worker cycle; the
	// repository update below is the durable source of truth for the account UI
	// and scheduler.
	account.Status = StatusError
	account.ErrorMessage = errorMessage
	account.Schedulable = false
	if repo == nil {
		return
	}
	if err := repo.SetError(ctx, account.ID, errorMessage); err != nil {
		slog.Warn("cookie harvest unauthorized account state update failed", "account_id", account.ID, "error", err)
	}
}

func (s *OpenAIGatewayService) validateOpenAICodexCookieHost(ctx context.Context, account *Account, token, cookie, proxyURL, model string) (string, string, int) {
	validationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	validationSessionID := uuid.NewString()
	body, err := openAICodexCookieValidationRequestBody(model)
	if err != nil {
		return "error", err.Error(), 0
	}
	req, err := http.NewRequestWithContext(validationCtx, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
	if err != nil {
		return "error", err.Error(), 0
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Close = true
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set(responsesLiteHeaderKey, "true")
	req.Header.Set("session_id", validationSessionID)
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(validationCtx, s.accountRepo, req.Header, account); err != nil {
		return "error", err.Error(), 0
	}
	applyOpenAICodexTicketHarvestIdentity(req.Header, model, account.GetOpenAIUserAgent())
	deleteOpenAIHeaderEqualFold(req.Header, openAICodexTurnStateHeader)
	deleteOpenAIHeaderEqualFold(req.Header, "session-id")
	deleteOpenAIHeaderEqualFold(req.Header, "Cookie")
	req.Header.Set("session_id", validationSessionID)
	req.Header.Set("Cookie", cookie)
	// Host validation is a normal account request. The harvester's proxy pool
	// is only for collecting Cookies and must never be reused for this request;
	// if the account itself has a configured proxy, normal forwarding keeps it.
	// A rotation candidate uses its captured Cookie through the normal request
	// path without publishing an unvalidated binding to the scheduler. Looking
	// up the active binding here would test the old Host (or clear it on expiry).
	boundCookie := strings.TrimSpace(cookie)
	if strings.TrimSpace(boundCookie) == "" {
		boundCookie = s.openAICodexCookieForAccount(account)
	}
	req.Header.Set("Cookie", boundCookie)
	normalProxy := ""
	if account.Proxy != nil {
		normalProxy = strings.TrimSpace(account.Proxy.URL())
	}
	resp, err := s.doOpenAIUpstream(req, normalProxy, account)
	retryable := err != nil || resp == nil || (resp != nil && (resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout))
	if retryable {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		retryReq := req.Clone(validationCtx)
		retryReq.Body = io.NopCloser(bytes.NewReader(body))
		resp, err = s.doOpenAIUpstream(retryReq, normalProxy, account)
	}
	if err != nil || resp == nil {
		if err == nil {
			err = errors.New("empty validation response")
		}
		return "error", err.Error(), 0
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if readErr != nil {
		return "read_error", readErr.Error(), resp.StatusCode
	}
	if len(responseBody) > 1<<20 {
		return "response_too_large", truncateOpenAICodexValidationResponse(string(responseBody)), resp.StatusCode
	}
	decision := openAICodexCookieValidationDecision(responseBody)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "error", string(responseBody), resp.StatusCode
	}
	return decision, string(responseBody), resp.StatusCode
}

func openAICodexCookieValidationRequestBody(model string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"model":               model,
		"store":               false,
		"stream":              true,
		"instructions":        "Answer exactly yes or no.",
		"parallel_tool_calls": false,
		"reasoning":           map[string]any{"context": "all_turns"},
		"input": []any{map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": openAICodexCookieValidationPrompt}},
		}},
	})
}

func openAICodexCookieValidationDecision(body []byte) string {
	var output strings.Builder
	failed := false
	appendText := func(value string) { output.WriteString(value) }
	var readOutput func(any)
	readOutput = func(value any) {
		obj, ok := value.(map[string]any)
		if !ok {
			return
		}
		if eventType, _ := obj["type"].(string); eventType == "error" || eventType == "response.failed" || eventType == "response.incomplete" {
			failed = true
			return
		}
		if status, _ := obj["status"].(string); status == "failed" || status == "incomplete" {
			failed = true
			return
		}
		if text, _ := obj["output_text"].(string); text != "" {
			appendText(text)
		}
		if eventType, _ := obj["type"].(string); eventType == "response.output_text.delta" {
			if delta, _ := obj["delta"].(string); delta != "" {
				appendText(delta)
			}
			return
		}
		if eventType, _ := obj["type"].(string); eventType == "response.completed" || eventType == "response.done" {
			if response, ok := obj["response"]; ok {
				// Completed/done carries a full snapshot, not another delta.
				partial := output.String()
				output.Reset()
				readOutput(response)
				if output.Len() == 0 {
					output.WriteString(partial)
				}
			}
			return
		}
		if choices, ok := obj["choices"].([]any); ok {
			for _, raw := range choices {
				choice, _ := raw.(map[string]any)
				if delta, ok := choice["delta"].(map[string]any); ok {
					if text, _ := delta["content"].(string); text != "" {
						appendText(text)
					}
				}
				if message, ok := choice["message"].(map[string]any); ok {
					if text, _ := message["content"].(string); text != "" {
						appendText(text)
					}
				}
			}
		}
		if outputItems, ok := obj["output"].([]any); ok {
			for _, raw := range outputItems {
				item, _ := raw.(map[string]any)
				if itemType, _ := item["type"].(string); itemType != "message" && itemType != "output_text" {
					continue
				}
				if text, _ := item["text"].(string); text != "" {
					appendText(text)
				}
				if content, ok := item["content"].([]any); ok {
					for _, rawContent := range content {
						part, _ := rawContent.(map[string]any)
						if partType, _ := part["type"].(string); partType == "output_text" {
							if text, _ := part["text"].(string); text != "" {
								appendText(text)
							}
						}
					}
				}
			}
		}
		if item, ok := obj["item"]; ok {
			readOutput(item)
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if data, ok := extractOpenAISSEDataLine(line); ok {
			line = data
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var decoded any
		if json.Unmarshal([]byte(line), &decoded) == nil {
			readOutput(decoded)
		}
	}
	if failed || scanner.Err() != nil {
		return ""
	}
	normalized := strings.ToLower(strings.TrimSpace(output.String()))
	normalized = strings.Trim(normalized, ".!?\\\"' `\r\n")
	if normalized == "yes" || normalized == "no" {
		return normalized
	}
	return ""
}

func truncateOpenAICodexValidationResponse(value string) string {
	if len(value) <= 8192 {
		return value
	}
	return value[:8192] + "\n[validation response truncated]"
}

func normalizeOpenAICookieHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

// openAICodexCookieHostCooldownUntil is keyed by account and Host. Older
// records used the two scalar fields below; they remain a read fallback for
// upgrades, but all new cooldowns are written to the map.
func openAICodexCookieHostCooldownUntil(account *Account, host string) time.Time {
	if account == nil {
		return time.Time{}
	}
	host = normalizeOpenAICookieHost(host)
	if host == "" {
		return time.Time{}
	}
	if raw, ok := account.Extra[openAICodexCookieCooldownsExtraKey]; ok {
		var values map[string]string
		if data, err := json.Marshal(raw); err == nil && json.Unmarshal(data, &values) == nil {
			if until, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(values[host])); err == nil {
				return until
			}
		}
	}
	legacyHost := normalizeOpenAICookieHost(account.GetExtraString("codex_cookie_ws_host"))
	if legacyHost != host || strings.TrimSpace(account.GetExtraString("codex_cookie_ws_started_at")) != "" {
		return time.Time{}
	}
	until, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(account.GetExtraString("codex_cookie_host_cooldown_until")))
	return until
}

func openAICodexCookieHostCooldown(account *Account) (time.Time, string) {
	if account == nil {
		return time.Time{}, ""
	}
	host := normalizeOpenAICookieHost(account.GetExtraString("codex_cookie_ws_host"))
	return openAICodexCookieHostCooldownUntil(account, host), host
}

func (s *OpenAIGatewayService) bindOpenAICodexCookieHost(ctx context.Context, account *Account, host string) error {
	return s.bindOpenAICodexCookieHostStartedAt(ctx, account, host, time.Time{})
}

// A validated replacement retains the deadline measured from the start of
// validation, and publishes Host and deadline together only after success.
func (s *OpenAIGatewayService) bindOpenAICodexCookieHostStartedAt(ctx context.Context, account *Account, host string, startedAt time.Time) error {
	if err := s.checkCookieHostMonitorLock(ctx, account); err != nil {
		return err
	}
	if account == nil || strings.TrimSpace(host) == "" {
		return errors.New("account or host is empty")
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	bindingSeconds, rotationBeforeSeconds := 240, 10
	if s != nil && s.settingService != nil {
		if settings, err := s.settingService.GetOpenAICookieSettings(ctx); err == nil && settings != nil {
			bindingSeconds = settings.CookieHostBindingSeconds
			rotationBeforeSeconds = settings.CookieHostRotationBeforeSeconds
		}
	}
	if bindingSeconds < 10 {
		bindingSeconds = 240
	}
	if rotationBeforeSeconds < 0 || rotationBeforeSeconds > bindingSeconds {
		rotationBeforeSeconds = 10
	}
	now := time.Now()
	if !startedAt.IsZero() {
		now = startedAt
	}
	bindingExpiresAt := now.Add(time.Duration(bindingSeconds) * time.Second)
	rotationAt := bindingExpiresAt.Add(-time.Duration(rotationBeforeSeconds) * time.Second)
	if startedAt.IsZero() && normalizeOpenAICookieHost(account.GetExtraString(openAICodexCookieHostExtraKey)) == host {
		if raw := strings.TrimSpace(account.GetExtraString("codex_cookie_host_binding_expires_at")); raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				bindingExpiresAt = parsed
			}
		}
		if raw := strings.TrimSpace(account.GetExtraString("codex_cookie_host_rotation_at")); raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				rotationAt = parsed
			}
		}
	}
	cooldowns := make(map[string]string)
	if raw, ok := account.Extra[openAICodexCookieCooldownsExtraKey]; ok {
		if data, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(data, &cooldowns)
		}
	}
	delete(cooldowns, host)
	updates := map[string]any{
		openAICodexCookieHostExtraKey:          host,
		"codex_cookie_host_binding_expires_at": bindingExpiresAt.Format(time.RFC3339Nano),
		"codex_cookie_host_rotation_at":        rotationAt.Format(time.RFC3339Nano),
		"codex_cookie_ws_host":                 nil,
		"codex_cookie_host_cooldown_until":     nil,
		openAICodexCookieCooldownsExtraKey:     cooldowns,
	}
	if !startedAt.IsZero() {
		updates[openAICodexCookieRotationNextExtraKey] = rotationAt.Format(time.RFC3339Nano)
	}
	if s.accountRepo != nil {
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
			return err
		}
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	for key, value := range updates {
		if value == nil {
			delete(account.Extra, key)
		} else {
			account.Extra[key] = value
		}
	}
	return nil
}

// unbindOpenAICodexCookieHost removes only the active Host binding. Existing
// account/Host cooldown records are preserved so a failed validation cannot
// leave a transient Host attached to the account.
func (s *OpenAIGatewayService) unbindOpenAICodexCookieHost(ctx context.Context, account *Account, host string) error {
	if err := s.checkCookieHostMonitorLock(ctx, account); err != nil {
		return err
	}
	if account == nil {
		return errors.New("account is empty")
	}
	host = normalizeOpenAICookieHost(host)
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	if host == "" || normalizeOpenAICookieHost(account.GetExtraString(openAICodexCookieHostExtraKey)) == host {
		delete(account.Extra, openAICodexCookieHostExtraKey)
		delete(account.Extra, "codex_cookie_host_binding_expires_at")
		delete(account.Extra, "codex_cookie_host_rotation_at")
	}
	delete(account.Extra, "codex_cookie_ws_host")
	delete(account.Extra, "codex_cookie_host_cooldown_until")
	if s.accountRepo == nil {
		return nil
	}
	return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		openAICodexCookieHostExtraKey:          nil,
		"codex_cookie_host_binding_expires_at": nil,
		"codex_cookie_host_rotation_at":        nil,
		"codex_cookie_ws_host":                 nil,
		"codex_cookie_host_cooldown_until":     nil,
	})
}

func (s *OpenAIGatewayService) markOpenAICodexCookieHostCooldown(ctx context.Context, account *Account, host string, seconds int) error {
	if account == nil || strings.TrimSpace(host) == "" {
		return errors.New("account or host is empty")
	}
	if seconds <= 0 {
		seconds = 14400
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	until := time.Now().Add(time.Duration(seconds) * time.Second)
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	cooldowns := make(map[string]string)
	if raw, ok := account.Extra[openAICodexCookieCooldownsExtraKey]; ok {
		if data, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(data, &cooldowns)
		}
	}
	cooldowns[host] = until.Format(time.RFC3339Nano)
	account.Extra[openAICodexCookieCooldownsExtraKey] = cooldowns
	account.Extra["codex_cookie_ws_host"] = host
	account.Extra["codex_cookie_host_cooldown_until"] = until.Format(time.RFC3339Nano)
	activeHost := openAICodexCookieHostFromAccount(account)
	if activeHost == host {
		delete(account.Extra, openAICodexCookieHostExtraKey)
	}
	if s.accountRepo == nil {
		return nil
	}
	var activeValue any
	if activeHost != "" && activeHost != host {
		activeValue = activeHost
	}
	return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		openAICodexCookieHostExtraKey:      activeValue,
		"codex_cookie_ws_host":             host,
		"codex_cookie_host_cooldown_until": until.Format(time.RFC3339Nano),
		openAICodexCookieCooldownsExtraKey: cooldowns,
	})
}

func (s *OpenAIGatewayService) MarkOpenAICookieHostCooldown(ctx context.Context, account *Account, host string, seconds int) error {
	return s.markOpenAICodexCookieHostCooldown(ctx, account, host, seconds)
}

func (s *OpenAIGatewayService) nextOpenAICodexCookieHarvestProxy(ctx context.Context, cfg config.OpenAICodexTicketConfig, account *Account) (string, string) {
	if len(cfg.CookieHarvestProxyURLs) > 0 {
		if cfg.CookieDynamicProxyFillHostCookie && account != nil {
			route := openAICookieHostRouteKey(openAICodexCookieHostFromAccount(account))
			if route != "" {
				if candidates := s.learnedOpenAICookieProxies(ctx, route, cfg.CookieHarvestProxyURLs, cfg.CookieProxyLearningAttempts); len(candidates) > 0 {
					index := s.openaiCodexCookieProxySequence.Add(1) - 1
					return candidates[index%uint64(len(candidates))], "cookie_host_affinity"
				}
			}
		}
		index := s.openaiCodexCookieProxySequence.Add(1) - 1
		return cfg.CookieHarvestProxyURLs[index%uint64(len(cfg.CookieHarvestProxyURLs))], "cookie_pool"
	}
	if proxy := strings.TrimSpace(cfg.HarvestProxyURL); proxy != "" {
		return proxy, "dedicated"
	}
	if account != nil && account.Proxy != nil {
		return account.Proxy.URL(), "account"
	}
	return "", ""
}

func openAICookieHostRouteKey(host string) string {
	host = normalizeOpenAICookieHost(host)
	for _, part := range strings.Split(host, ".") {
		if strings.HasPrefix(part, "unified-") && len(part) > len("unified-") {
			return part
		}
	}
	return host
}

func normalizeCookieProxyKey(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (s *OpenAIGatewayService) loadOpenAICookieProxyBindings(ctx context.Context) map[string]openAICookieProxyHostBindingState {
	result := make(map[string]openAICookieProxyHostBindingState)
	if s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return result
	}
	raw, err := s.settingService.settingRepo.GetValue(ctx, cookieProxyHostBindingsKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return result
	}
	if json.Unmarshal([]byte(raw), &result) != nil {
		return make(map[string]openAICookieProxyHostBindingState)
	}
	for key, state := range result {
		if state.Hosts == nil {
			state.Hosts = map[string]int{}
		}
		if state.History == nil {
			state.History = make(map[string]int, len(state.Hosts))
			for host, count := range state.Hosts {
				state.History[host] = count
			}
		}
		if state.Requests < 0 {
			state.Requests = 0
		}
		result[normalizeCookieProxyKey(key)] = state
		if normalizeCookieProxyKey(key) != key {
			delete(result, key)
		}
	}
	return result
}

func normalizeOpenAICookieProxyLearningLimit(limit int) int {
	if limit <= 0 {
		return openAICookieProxyDiscoveryRequests
	}
	return limit
}

func (s *OpenAIGatewayService) recordOpenAICookieProxyAttempt(ctx context.Context, proxyURL string, limit int, sampleTargets ...int) bool {
	proxyURL = normalizeCookieProxyKey(proxyURL)
	if proxyURL == "" || s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return false
	}
	limit = normalizeOpenAICookieProxyLearningLimit(limit)
	openAICookieProxyHostBindingsMu.Lock()
	defer openAICookieProxyHostBindingsMu.Unlock()
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bindings := s.loadOpenAICookieProxyBindings(persistCtx)
	state := bindings[proxyURL]
	if state.Requests >= limit {
		return false
	}
	learning := len(sampleTargets) == 0 || cookieProxySamples(state) < sampleTargets[0]
	if state.Hosts == nil {
		state.Hosts = map[string]int{}
	}
	if state.History == nil {
		state.History = map[string]int{}
	}
	state.Requests++
	state.UpdatedAt = time.Now()
	bindings[proxyURL] = state
	data, err := json.Marshal(bindings)
	if err == nil {
		if err := s.settingService.settingRepo.Set(persistCtx, cookieProxyHostBindingsKey, string(data)); err != nil {
			slog.Warn("openai cookie proxy learning attempt persist failed", "error", err)
		}
	}
	return learning
}

func (s *OpenAIGatewayService) recordOpenAICookieProxyHost(ctx context.Context, proxyURL, host string, learningAttempt bool) {
	proxyURL = normalizeCookieProxyKey(proxyURL)
	normalizedHost := normalizeOpenAICookieHost(host)
	route := openAICookieHostRouteKey(normalizedHost)
	if proxyURL == "" || normalizedHost == "" || route == "" || s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return
	}
	openAICookieProxyHostBindingsMu.Lock()
	defer openAICookieProxyHostBindingsMu.Unlock()
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bindings := s.loadOpenAICookieProxyBindings(persistCtx)
	state := bindings[proxyURL]
	if state.Hosts == nil {
		state.Hosts = map[string]int{}
	}
	if state.History == nil {
		state.History = map[string]int{}
	}
	newHost := true
	for _, other := range bindings {
		if cookieProxyHistory(other)[normalizedHost] > 0 {
			newHost = false
			break
		}
	}
	if newHost {
		state.Stats.NewHosts++
		state.Stats.LastNewHostAt = time.Now()
	}
	state.History[normalizedHost]++
	// Hosts only enter the learned route sample during the configured request
	// window. History continues to include every successful observation.
	if learningAttempt {
		state.Hosts[normalizedHost]++
	}
	state.UpdatedAt = time.Now()
	bindings[proxyURL] = state
	data, err := json.Marshal(bindings)
	if err == nil {
		if err := s.settingService.settingRepo.Set(persistCtx, cookieProxyHostBindingsKey, string(data)); err != nil {
			slog.Warn("openai cookie proxy host binding persist failed", "error", err)
		}
	}
}

func openAICookieProxyStateHasRoute(state openAICookieProxyHostBindingState, route string) bool {
	for host := range state.Hosts {
		if openAICookieHostRouteKey(host) == route || host == route {
			return true
		}
	}
	return false
}

func sanitizeOpenAICookieProxyForDisplay(raw string) string {
	parsed, err := url.Parse(normalizeCookieProxyKey(raw))
	if err != nil || parsed == nil || parsed.Host == "" {
		return normalizeCookieProxyKey(raw)
	}
	parsed.User = nil
	return parsed.String()
}

func (s *SettingService) GetOpenAICookieProxyHostMemories(ctx context.Context) ([]OpenAICookieProxyHostMemory, error) {
	if s == nil || s.settingRepo == nil {
		return []OpenAICookieProxyHostMemory{}, nil
	}
	openAICookieProxyHostBindingsMu.Lock()
	defer openAICookieProxyHostBindingsMu.Unlock()
	bindings := loadOpenAICookieProxyBindingsFromSettingService(ctx, s)
	limit := openAICookieProxyDiscoveryRequests
	sampleTarget := defaultCookieHarvestPolicy().LearningSamples
	if settings, err := s.GetOpenAICookieSettings(ctx); err == nil {
		limit = normalizeOpenAICookieProxyLearningLimit(settings.CookieProxyLearningAttempts)
		if settings.HarvestPolicy != nil {
			sampleTarget = settings.HarvestPolicy.LearningSamples
		}
	}
	result := make([]OpenAICookieProxyHostMemory, 0, len(bindings))
	for proxy, state := range bindings {
		history := state.History
		if len(history) == 0 {
			history = state.Hosts
		}
		items := make([]OpenAICookieProxyHostMemoryEntry, 0, len(history))
		for host, count := range history {
			items = append(items, OpenAICookieProxyHostMemoryEntry{Host: host, Count: count})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Count == items[j].Count {
				return items[i].Host < items[j].Host
			}
			return items[i].Count > items[j].Count
		})
		proxyDisplay := sanitizeOpenAICookieProxyForDisplay(proxy)
		proxyUsername := ""
		if parsed, parseErr := url.Parse(proxy); parseErr == nil && parsed.User != nil {
			proxyUsername = parsed.User.Username()
		}
		result = append(result, OpenAICookieProxyHostMemory{
			Proxy: proxyDisplay, ProxyUsername: proxyUsername, Requests: state.Requests,
			Limit: limit, Completed: state.Requests >= limit || cookieProxySamples(state) >= sampleTarget,
			SuccessfulSamples: cookieProxySamples(state), SampleTarget: sampleTarget, Stats: state.Stats, BackoffUntil: state.BackoffUntil,
			Hosts: items, UpdatedAt: state.UpdatedAt,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Proxy < result[j].Proxy })
	return result, nil
}

func (s *SettingService) ResetOpenAICookieProxyHostMemory(ctx context.Context, proxy string) error {
	return s.ResetOpenAICookieProxyHostMemoryForProxy(ctx, proxy, "")
}

// ResetOpenAICookieProxyHostMemoryForProxy resets exactly one learned proxy.
// proxy_username disambiguates proxy URLs that share the same endpoint.
func (s *SettingService) ResetOpenAICookieProxyHostMemoryForProxy(ctx context.Context, proxy, proxyUsername string) error {
	proxy = normalizeCookieProxyKey(proxy)
	if proxy == "" {
		return errors.New("proxy is empty")
	}
	if s == nil || s.settingRepo == nil {
		return errors.New("cookie proxy memory unavailable")
	}
	openAICookieProxyHostBindingsMu.Lock()
	defer openAICookieProxyHostBindingsMu.Unlock()
	bindings := loadOpenAICookieProxyBindingsFromSettingService(ctx, s)
	proxyUsername = strings.TrimSpace(proxyUsername)
	keys := []string{}
	for key := range bindings {
		if key == proxy {
			keys = append(keys, key)
			continue
		}
		if sanitizeOpenAICookieProxyForDisplay(key) != proxy {
			continue
		}
		parsed, parseErr := url.Parse(key)
		if parseErr != nil {
			continue
		}
		if proxyUsername != "" && parsed.User != nil && parsed.User.Username() == proxyUsername {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		if proxyUsername == "" {
			return errors.New("proxy username is required when multiple proxy credentials share an endpoint")
		}
		return nil
	}
	for _, key := range keys {
		delete(bindings, key)
	}
	data, err := json.Marshal(bindings)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, cookieProxyHostBindingsKey, string(data))
}

func (s *SettingService) ResetAllOpenAICookieProxyHostMemories(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("cookie proxy memory unavailable")
	}
	openAICookieProxyHostBindingsMu.Lock()
	defer openAICookieProxyHostBindingsMu.Unlock()
	return s.settingRepo.Set(ctx, cookieProxyHostBindingsKey, "{}")
}

func loadOpenAICookieProxyBindingsFromSettingService(ctx context.Context, s *SettingService) map[string]openAICookieProxyHostBindingState {
	result := make(map[string]openAICookieProxyHostBindingState)
	if s == nil || s.settingRepo == nil {
		return result
	}
	raw, err := s.settingRepo.GetValue(ctx, cookieProxyHostBindingsKey)
	if err != nil || strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &result) != nil {
		return make(map[string]openAICookieProxyHostBindingState)
	}
	for key, state := range result {
		if state.Hosts == nil {
			state.Hosts = map[string]int{}
		}
		if state.History == nil {
			state.History = make(map[string]int, len(state.Hosts))
			for host, count := range state.Hosts {
				state.History[host] = count
			}
		}
		if state.Requests < 0 {
			state.Requests = 0
		}
		normalized := normalizeCookieProxyKey(key)
		result[normalized] = state
		if normalized != key {
			delete(result, key)
		}
	}
	return result
}

func (s *OpenAIGatewayService) learningOpenAICookieProxies(ctx context.Context, configured []string, limit int) []string {
	if s == nil || s.settingService == nil || s.settingService.settingRepo == nil || len(configured) == 0 {
		return nil
	}
	limit = normalizeOpenAICookieProxyLearningLimit(limit)
	openAICookieProxyHostBindingsMu.Lock()
	defer openAICookieProxyHostBindingsMu.Unlock()
	bindings := s.loadOpenAICookieProxyBindings(ctx)
	result := make([]string, 0, len(configured))
	for _, raw := range configured {
		state, ok := bindings[normalizeCookieProxyKey(raw)]
		if !ok || state.Requests < limit {
			result = append(result, raw)
		}
	}
	return result
}

func (s *OpenAIGatewayService) learnedOpenAICookieProxies(ctx context.Context, route string, configured []string, limit int) []string {
	if route == "" || s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return nil
	}
	limit = normalizeOpenAICookieProxyLearningLimit(limit)
	openAICookieProxyHostBindingsMu.Lock()
	defer openAICookieProxyHostBindingsMu.Unlock()
	bindings := s.loadOpenAICookieProxyBindings(ctx)
	result := make([]string, 0, len(configured))
	for _, raw := range configured {
		proxy := normalizeCookieProxyKey(raw)
		state, ok := bindings[proxy]
		if ok && state.Requests >= limit && openAICookieProxyStateHasRoute(state, route) {
			result = append(result, raw)
		}
	}
	return result
}

// dynamicOpenAICookieProxyForMissingHost finds a configured proxy that has
// historically returned a Host whose Cookie is currently absent or expired.
// The next harvest through that proxy replenishes the missing Host without
// changing the normal round-robin behaviour when no gap is detected.
func (s *OpenAIGatewayService) dynamicOpenAICookieProxyForMissingHost(ctx context.Context, configured []string) string {
	proxies := s.dynamicOpenAICookieProxiesForMissingHosts(ctx, configured)
	if len(proxies) > 0 {
		return proxies[0]
	}
	return ""
}

func (s *OpenAIGatewayService) dynamicOpenAICookieProxiesForMissingHosts(ctx context.Context, configured []string) []string {
	if s == nil || s.settingService == nil || len(configured) == 0 {
		return nil
	}
	openAICookieProxyHostBindingsMu.Lock()
	bindings := s.loadOpenAICookieProxyBindings(ctx)
	openAICookieProxyHostBindingsMu.Unlock()
	result := make([]string, 0, len(configured))
	for _, raw := range configured {
		proxy := normalizeCookieProxyKey(raw)
		state, ok := bindings[proxy]
		if !ok {
			continue
		}
		history := state.History
		if len(history) == 0 {
			history = state.Hosts
		}
		for host := range history {
			entry, err := s.settingService.LookupOpenAICodexCookie(ctx, host)
			if err != nil || entry == nil || !entry.ExpiresAt.After(time.Now()) {
				result = append(result, raw)
				break
			}
		}
	}
	return result
}

const openAICodexAstraMinVersion = "0.153.4"

// applyOpenAICodexTicketHarvestIdentity makes synthetic ticket probes look like
// the Codex client request used by the reference harvester. The normal
// account/header resolver still supplies account identity headers separately.
func applyOpenAICodexTicketHarvestIdentity(headers http.Header, model, overrideUA string) {
	if headers == nil {
		return
	}
	ensureCodexIdentityHeaders(headers)
	enforceCodexIdentityHeadersWithUA(headers, overrideUA)
	model = strings.ToLower(strings.TrimSpace(model))
	if !strings.Contains(model, "gpt-6") && !strings.Contains(model, "astra") {
		return
	}
	version := strings.TrimSpace(headers.Get("version"))
	if version == "" || CompareVersions(version, openAICodexAstraMinVersion) < 0 {
		headers.Set("version", openAICodexAstraMinVersion)
		headers.Set("user-agent", buildCodexCLIUserAgent(openAICodexAstraMinVersion))
		headers.Set("originator", openai.CodexDefaultOriginator)
	}
}

func openAICodexHarvestProxyInfo(raw string) (scheme, host string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return "", ""
	}
	return strings.ToLower(parsed.Scheme), parsed.Host
}

func (s *OpenAIGatewayService) storeOpenAICodexTicket(account *Account, state string, capturedAt time.Time, cfg config.OpenAICodexTicketConfig) *openAICodexTicket {
	state = strings.TrimSpace(state)
	if s == nil || account == nil || !plausibleOpenAICodexTicket(state, cfg.TargetLength) {
		return nil
	}
	ticket := &openAICodexTicket{State: state, Length: len(state), CapturedAt: capturedAt, ExpiresAt: capturedAt.Add(time.Duration(cfg.TTLSeconds) * time.Second)}
	s.openaiCodexTickets.Store(account.ID, ticket)
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[openAICodexTicketExtraKey] = ticket
	if s.accountRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{openAICodexTicketExtraKey: ticket}); err != nil {
			slog.Warn("openai codex response ticket persist failed", "account_id", account.ID, "error", err)
		}
	}
	return ticket
}

func readOpenAICodexSessionID(body io.Reader) string {
	if body == nil {
		return ""
	}
	scanner := bufio.NewScanner(io.LimitReader(body, 2<<20))
	scanner.Buffer(make([]byte, 4<<10), 512<<10)
	for scanner.Scan() {
		payload := strings.TrimSpace(scanner.Text())
		if data, ok := extractOpenAISSEDataLine(payload); ok {
			payload = data
		}
		if payload == "" || payload == "[DONE]" {
			continue
		}
		sessionID := extractOpenAICodexSessionIDJSON(payload)
		if sessionID != "" {
			return sessionID
		}
	}
	return ""
}

func extractOpenAICodexSessionIDJSON(payload string) string {
	for _, path := range []string{"session_id", "sessionId", "response.session_id", "response.sessionId", "response.metadata.session_id", "response.metadata.sessionId"} {
		if value := strings.TrimSpace(gjson.Get(payload, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func openAICodexResponseSnapshot(response *http.Response, body []byte) string {
	if response == nil {
		return ""
	}
	headers := make(map[string][]string, len(response.Header))
	for key, values := range response.Header {
		headers[key] = append([]string(nil), values...)
	}
	snapshot := map[string]any{
		"status_code": response.StatusCode,
		"status":      response.Status,
		"headers":     headers,
		"body":        string(body),
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	return string(data)
}

func (s *OpenAIGatewayService) lookupOpenAICodexTicket(account *Account) *openAICodexTicket {
	if s == nil || account == nil {
		return nil
	}
	if raw, ok := s.openaiCodexTickets.Load(account.ID); ok {
		if ticket, ok := raw.(*openAICodexTicket); ok && ticket.valid(time.Now(), s.codexTicketConfig().TargetLength) {
			return ticket
		}
	}
	if account.Extra != nil {
		if raw, ok := account.Extra[openAICodexTicketExtraKey]; ok {
			if b, err := json.Marshal(raw); err == nil {
				var ticket openAICodexTicket
				if json.Unmarshal(b, &ticket) == nil && ticket.valid(time.Now(), s.codexTicketConfig().TargetLength) {
					s.openaiCodexTickets.Store(account.ID, &ticket)
					return &ticket
				}
			}
		}
	}
	return nil
}

func (s *OpenAIGatewayService) applyOpenAICodexTicket(account *Account, headers http.Header) {
	cfg := s.codexTicketConfig()
	if s == nil || headers == nil || !cfg.Enabled || !cfg.OverrideTurnState {
		return
	}
	if ticket := s.lookupOpenAICodexTicket(account); ticket != nil {
		deleteOpenAIHeaderEqualFold(headers, openAICodexTurnStateHeader)
		headers.Set(openAICodexTurnStateHeader, ticket.State)
	}
}

func openAICodexCookieFromAccount(account *Account) string {
	if account == nil || account.Extra == nil {
		return ""
	}
	return strings.TrimSpace(account.GetExtraString(openAICodexCookieExtraKey))
}

func openAICodexCookieHostFromAccount(account *Account) string {
	if account == nil || account.Extra == nil || account.openaiCookieDegraded {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(account.GetExtraString(openAICodexCookieHostExtraKey)), "."))
}

// openAICodexCookieForAccount resolves the account's explicitly bound library
// host. Expired Cookies are not returned; inference never edits the binding.
// Accounts without a binding retain the legacy
// account-local cookie for backwards compatibility.
func (s *OpenAIGatewayService) openAICodexCookieForAccount(account *Account) string {
	if account == nil {
		return ""
	}
	if account.openaiCookieValidationCookie != "" {
		return account.openaiCookieValidationCookie
	}
	if account.openaiCookieDegraded {
		return ""
	}
	host := openAICodexCookieHostFromAccount(account)
	if host == "" || s == nil || s.settingService == nil {
		return openAICodexCookieFromAccount(account)
	}
	entry, err := s.settingService.LookupOpenAICodexCookie(context.Background(), host)
	if err == nil && entry != nil {
		return entry.Cookie
	}
	// A request may hold an old Host while rotation has already saved a new
	// binding. Cookie lookup must never clear the authoritative database binding.
	return ""
}

// openAICodexCookieJWTInfo extracts the decoded payload from the __oailb
// cookie returned by the ticket endpoint. The payload is exposed for the
// administrator UI so the upstream host can be inspected without copying the
// whole JWT by hand.
func openAICodexCookieJWTInfo(cookieHeader string) (string, map[string]any) {
	value := parseOpenAICodexCookieHeader(cookieHeader)["__oailb"]
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 || parts[1] == "" {
		return "", nil
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payloadBytes, err = base64.URLEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return "", nil
	}
	var payload map[string]any
	if json.Unmarshal(payloadBytes, &payload) != nil || len(payload) == 0 {
		return "", nil
	}
	host, _ := payload["host"].(string)
	return strings.TrimSpace(host), payload
}

func openAICodexCookieHostAllowed(cookieHeader string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return true
	}
	host, _ := openAICodexCookieJWTInfo(cookieHeader)
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, allowed := range normalizeOpenAICodexCookieHostWhitelist(whitelist) {
		if host == allowed {
			return true
		}
	}
	return false
}

func parseOpenAICodexCookieHeader(raw string) map[string]string {
	values := make(map[string]string)
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		name, value, ok := strings.Cut(part, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" || strings.ContainsAny(name, "()<>@,;:\\[\\]?={} \t\r\n") || strings.ContainsAny(value, "\r\n") {
			continue
		}
		values[name] = value
	}
	return values
}

func serializeOpenAICodexCookieHeader(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, "; ")
}

// mergeOpenAICodexCookieHeaders keeps cookies already present on the request
// and overlays the account-bound cookie jar on top of them. This is important
// for cookies unrelated to the Codex account binding: they must not be lost
// just because one or more account cookies are being refreshed.
func mergeOpenAICodexCookieHeaders(existing, bound string) string {
	values := parseOpenAICodexCookieHeader(existing)
	for key, value := range parseOpenAICodexCookieHeader(bound) {
		// Cloudflare's __cf_bm is request-scoped. Preserve the caller's value
		// and never inject an account-bound copy into a new request.
		if strings.EqualFold(key, "__cf_bm") {
			continue
		}
		values[key] = value
	}
	return serializeOpenAICodexCookieHeader(values)
}

func openAICodexResponseCookieHeader(response *http.Response) string {
	if response == nil {
		return ""
	}
	values := make(map[string]string)
	now := time.Now()
	for _, cookie := range response.Cookies() {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" || cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && !now.Before(cookie.Expires)) || strings.ContainsAny(cookie.Value, "\r\n") {
			continue
		}
		values[cookie.Name] = cookie.Value
	}
	return serializeOpenAICodexCookieHeader(values)
}

// captureOpenAICodexCookie stores the account-level cookie jar returned by the
// upstream response. Cookie harvesting is deliberately independent from the
// ticket length so the host-keyed library can be populated by any harvester.
func (s *OpenAIGatewayService) captureOpenAICodexCookie(account *Account, response *http.Response, ticket string) string {
	if s == nil || account == nil || response == nil || !account.IsOpenAIOAuthLike() || len(response.Header.Values("Set-Cookie")) == 0 {
		return ""
	}
	cookieHeader := openAICodexResponseCookieHeader(response)
	if cookieHeader == "" {
		return ""
	}
	if !openAICodexCookieHostAllowed(cookieHeader, s.codexTicketConfig().CookieHostWhitelist) {
		return ""
	}
	host, payload := openAICodexCookieJWTInfo(cookieHeader)
	if s.settingService != nil && host != "" {
		entry := OpenAICodexCookieLibraryEntry{Host: host, Cookie: cookieHeader, Payload: payload, CapturedAt: time.Now()}
		if exp, ok := payload["exp"].(float64); ok && exp > 0 {
			entry.ExpiresAt = time.Unix(int64(exp), 0)
		}
		if err := s.settingService.UpsertOpenAICodexCookie(context.Background(), entry); err != nil {
			slog.Warn("openai codex cookie library persist failed", "host", host, "error", err)
		}
	}
	// Keep the legacy account-local cookie tied to a valid ticket only. The
	// independent library above is authoritative for host bindings.
	if !plausibleOpenAICodexTicket(ticket, s.codexTicketConfig().TargetLength) {
		return cookieHeader
	}
	if len(cookieHeader) > 32768 || cookieHeader == openAICodexCookieFromAccount(account) {
		return cookieHeader
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[openAICodexCookieExtraKey] = cookieHeader
	if s.accountRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{openAICodexCookieExtraKey: cookieHeader}); err != nil {
			slog.Warn("openai codex cookie persist failed", "account_id", account.ID, "error", err)
		}
	}
	return cookieHeader
}

func (s *OpenAIGatewayService) applyOpenAICodexCookie(account *Account, headers http.Header) {
	if s == nil || headers == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return
	}
	boundHost := openAICodexCookieHostFromAccount(account)
	if !s.codexTicketConfig().Enabled && boundHost == "" {
		return
	}
	cookie := s.openAICodexCookieForAccount(account)
	if cookie != "" && (boundHost != "" || openAICodexCookieHostAllowed(cookie, s.codexTicketConfig().CookieHostWhitelist)) {
		// Header maps can contain differently-cased Cookie keys after raw
		// passthrough. Collect all of them before removing the duplicates.
		keys := make([]string, 0, len(headers))
		for key := range headers {
			if strings.EqualFold(strings.TrimSpace(key), "Cookie") {
				keys = append(keys, key)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			// Prefer the canonical header when differently-cased entries carry
			// the same cookie name.
			if (keys[i] == "Cookie") != (keys[j] == "Cookie") {
				return keys[i] != "Cookie"
			}
			return keys[i] < keys[j]
		})
		existing := make([]string, 0, len(keys))
		for _, key := range keys {
			existing = append(existing, headers[key]...)
		}
		merged := mergeOpenAICodexCookieHeaders(strings.Join(existing, "; "), cookie)
		deleteOpenAIHeaderEqualFold(headers, "Cookie")
		if merged != "" {
			headers.Set("Cookie", merged)
		}
	}
}

// applyOpenAIAccountBoundState is the final outbound authority for state tied
// to an OpenAI OAuth account. Each setter removes differently-cased copies
// before writing so a raw passthrough header cannot coexist on the wire.
func (s *OpenAIGatewayService) applyOpenAIAccountBoundState(account *Account, headers http.Header) {
	if s == nil || account == nil || headers == nil || !account.IsOpenAIOAuthLike() {
		return
	}
	if account.openaiCookieDegraded {
		deleteOpenAIHeaderEqualFold(headers, "Cookie")
		deleteOpenAIHeaderEqualFold(headers, openAICodexTurnStateHeader)
		return
	}
	s.applyOpenAICodexTicket(account, headers)
	applyOpenAIAccountSessionID(account, headers, s)
	s.applyOpenAICodexCookie(account, headers)
}

// prepareCookieBoundWSAccount rotates the account-level WS pool when a bound
// Cookie host enters its one-hour connection window. The pool itself keeps the
// configured number of warm connections and background ping/reader loops keep
// them alive between requests.
func (s *OpenAIGatewayService) prepareCookieBoundWSAccount(account *Account) {
	if s == nil || account == nil {
		return
	}
	// Resolve the library binding first; expired entries clear the host.
	if s.openAICodexCookieForAccount(account) == "" {
		return
	}
	host := openAICodexCookieHostFromAccount(account)
	if host == "" {
		return
	}
	settings, err := s.settingService.GetOpenAICookieSettings(context.Background())
	if err != nil || !settings.WSEnabled {
		return
	}
	cfg := s.codexTicketConfig()
	cfg.CookieWSConnections, cfg.CookieWSConnectionTTLSeconds, cfg.CookieWSHostCooldownSeconds = settings.WSConnections, settings.WSTTLSeconds, settings.WSHostCooldownSeconds
	now := time.Now()
	started, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(account.GetExtraString("codex_cookie_ws_started_at")))
	activeHost := strings.ToLower(strings.TrimSpace(account.GetExtraString("codex_cookie_ws_host")))
	expires := started.Add(time.Duration(cfg.CookieWSConnectionTTLSeconds) * time.Second)
	if started.IsZero() || activeHost != host || !expires.After(now) {
		if pool := s.getOpenAIWSConnPool(); pool != nil {
			pool.ClearAccount(account.ID)
		}
		started = now
		expires = now.Add(time.Duration(cfg.CookieWSConnectionTTLSeconds) * time.Second)
		connectionCount := cfg.CookieWSConnections
		if connectionCount <= 0 {
			connectionCount = 10
		}
		if account.Extra == nil {
			account.Extra = map[string]any{}
		}
		account.Extra["codex_cookie_ws_host"] = host
		account.Extra["codex_cookie_ws_started_at"] = started.Format(time.RFC3339Nano)
		account.Extra["codex_cookie_ws_expires_at"] = expires.Format(time.RFC3339Nano)
		account.Extra["codex_cookie_ws_connection_count"] = connectionCount
		if s.accountRepo != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
				"codex_cookie_ws_host":             host,
				"codex_cookie_ws_started_at":       started.Format(time.RFC3339Nano),
				"codex_cookie_ws_expires_at":       expires.Format(time.RFC3339Nano),
				"codex_cookie_ws_connection_count": connectionCount,
			})
		}
	}
}

func extractOpenAICodexSessionIDHeaders(headers http.Header) string {
	for _, key := range []string{"session_id", "session-id", "x-session-id", "x-openai-session-id"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

// applyOpenAIAccountSessionID forces the account-bound session_id after client
// headers and fingerprint isolation have been applied.
func applyOpenAIAccountSessionID(account *Account, headers http.Header, gateway ...*OpenAIGatewayService) {
	if account == nil || headers == nil || !account.IsOpenAIOAuthLike() {
		return
	}
	sessionID := account.GetOpenAISessionID()
	// The request builders isolate client session IDs before this final state
	// pass. Preserve that value instead of hashing it a second time. Agent
	// Identity requests do not need a generated persisted session when the
	// caller did not supply one; generating it here breaks lightweight gateway
	// implementations that intentionally do not persist account extras.
	if sessionID == "" {
		if rawSessionID := extractOpenAICodexSessionIDHeaders(headers); rawSessionID != "" {
			sessionID = rawSessionID
		} else if len(gateway) > 0 && gateway[0] != nil && !account.IsOpenAIAgentIdentity() {
			sessionID = gateway[0].ensureOpenAIAccountSessionID(account)
		}
	}
	deleteOpenAIHeaderEqualFold(headers, "session_id")
	deleteOpenAIHeaderEqualFold(headers, "session-id")
	if sessionID == "" {
		return
	}
	headers.Set("session_id", sessionID)
	headers.Set("session-id", sessionID)
}

// ensureOpenAIAccountSessionID gives OAuth accounts a stable outbound session
// identifier when the upstream ticket response does not provide one. The
// generated value is account-scoped and persisted once, while harvesting still
// uses a clean request and never receives this header.
func (s *OpenAIGatewayService) ensureOpenAIAccountSessionID(account *Account) string {
	if s == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return ""
	}
	if sessionID := account.GetOpenAISessionID(); sessionID != "" {
		s.openaiCodexSessionIDs.Store(account.ID, sessionID)
		return sessionID
	}
	if cached, ok := s.openaiCodexSessionIDs.Load(account.ID); ok {
		if sessionID, ok := cached.(string); ok && strings.TrimSpace(sessionID) != "" {
			return strings.TrimSpace(sessionID)
		}
	}
	candidate := uuid.NewString()
	actual, _ := s.openaiCodexSessionIDs.LoadOrStore(account.ID, candidate)
	sessionID, _ := actual.(string)
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	if account.GetOpenAISessionID() == "" {
		account.Extra[openAICodexSessionIDExtraKey] = sessionID
		if s.accountRepo != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := safeOpenAIAccountExtraUpdate(s.accountRepo, ctx, account.ID, map[string]any{openAICodexSessionIDExtraKey: sessionID}); err != nil {
				slog.Warn("generated openai session_id persist failed", "account_id", account.ID, "error", err)
			}
		}
	}
	return sessionID
}

// safeOpenAIAccountExtraUpdate keeps optional session persistence from taking
// down lightweight gateway/test repositories that embed a nil repository
// interface. A real repository still receives the same update and error.
func safeOpenAIAccountExtraUpdate(repo AccountRepository, ctx context.Context, accountID int64, updates map[string]any) (err error) {
	if repo == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("account extra repository unavailable: %v", recovered)
		}
	}()
	return repo.UpdateExtra(ctx, accountID, updates)
}

func (s *OpenAIGatewayService) captureOpenAIAccountSessionID(ctx context.Context, account *Account, headers http.Header) {
	if s == nil || account == nil || headers == nil || !account.IsOpenAIOAuthLike() {
		return
	}
	sessionID := strings.TrimSpace(headers.Get("session_id"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(headers.Get("session-id"))
	}
	if sessionID == "" {
		return
	}
	if previous, loaded := s.openaiCodexSessionIDs.Swap(account.ID, sessionID); loaded && previous == sessionID {
		return
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	if account.GetOpenAISessionID() == sessionID {
		return
	}
	account.Extra[openAICodexSessionIDExtraKey] = sessionID
	if s.accountRepo != nil {
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.accountRepo.UpdateExtra(persistCtx, account.ID, map[string]any{openAICodexSessionIDExtraKey: sessionID}); err != nil {
			slog.Warn("openai session_id persist failed", "account_id", account.ID, "error", err)
		}
	}
}

func IsOpenAICodexTicketPrivateExtraKey(key string) bool {
	if key == openAICodexTicketExtraKey || key == openAICodexTicketHistoryExtraKey || key == openAICodexCookieExtraKey || key == openAICodexCookieCooldownsExtraKey {
		return true
	}
	return strings.HasPrefix(key, "codex_cookie_ws_") || key == "codex_cookie_host_cooldown_until" || key == "codex_cookie_ws_host"
}
