package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
)

type intelligentRunKey struct{}
type intelligentRunContext struct {
	prompt  string
	capture *intelligentCapture
}

// Only an empty selection receives the protocol default. Explicit choices
// remain observable even when the upstream rejects the requested model.
func resolveIntelligentTestModel(account *Account, configured string) string {
	model := strings.TrimSpace(configured)
	if account != nil && account.UsesOpenAICodexProtocol() && model == "" {
		return "gpt-5.3-codex"
	}
	return model
}

func intelligentContext(ctx context.Context) *intelligentRunContext {
	v, _ := ctx.Value(intelligentRunKey{}).(*intelligentRunContext)
	return v
}
func intelligentPrompt(ctx context.Context) string {
	if v := intelligentContext(ctx); v != nil {
		return v.prompt
	}
	return ""
}
func applyIntelligentPayloadPrompt(ctx context.Context, payload map[string]any) {
	prompt := intelligentPrompt(ctx)
	if prompt == "" {
		return
	}
	if _, ok := payload["input"]; ok {
		payload["input"] = []map[string]any{{"role": "user", "content": []map[string]any{{"type": "input_text", "text": prompt}}}}
	}
	if _, ok := payload["messages"]; ok {
		payload["messages"] = []map[string]any{{"role": "user", "content": []map[string]any{{"type": "text", "text": prompt}}}}
		if _, bounded := payload["max_tokens"]; bounded {
			payload["max_tokens"] = 8192
		}
	}
}
func intelligentAntigravityBody(ctx context.Context, body []byte) ([]byte, error) {
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	request, ok := envelope["request"].(map[string]any)
	if !ok {
		return nil, errors.New("unsupported Antigravity request envelope")
	}
	request["contents"] = []map[string]any{{"role": "user", "parts": []map[string]any{{"text": intelligentPrompt(ctx)}}}}
	cfg, ok := request["generationConfig"].(map[string]any)
	if !ok {
		cfg = map[string]any{}
		request["generationConfig"] = cfg
	}
	cfg["maxOutputTokens"] = 8192
	return json.Marshal(envelope)
}
func (s *AntigravityGatewayService) runIntelligentAntigravity(ctx context.Context, account *Account, token string, body []byte) (*http.Response, error) {
	base := resolveAntigravityForwardBaseURL(account)
	if base == "" {
		return nil, errors.New("no antigravity forward base url configured")
	}
	req, err := antigravity.NewAPIRequestWithURL(ctx, base, "streamGenerateContent", token, body)
	if err != nil {
		return nil, err
	}
	proxy := ""
	if account.Proxy != nil && account.ProxyID != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(WithAccountTrafficRequest(req, account), proxy, account.ID, account.Concurrency)
	return captureIntelligentResponse(req, resp), err
}

// RunIntelligentTest uses the existing authenticated outbound protocol adapters.
// A per-run service avoids mutating shared service state. Runtime health writes
// are suppressed: an observation never changes account policy or scheduling.
func (s *AccountTestService) RunIntelligentTest(ctx context.Context, r *IntelligentTestRecord) error {
	account, err := s.accountRepo.GetByID(ctx, r.AccountID)
	if err != nil {
		r.Status = "account_error"
		return errors.New("account unavailable")
	}
	r.AntiDegradation = account.AntiDegradationEnabled()
	if r.ConfigSnapshot == nil {
		return errors.New("missing configuration")
	}
	r.Model = resolveIntelligentTestModel(account, r.ConfigSnapshot.Model)
	if err := accountTestCooldown(ctx, account, r.Model, time.Now()); err != nil {
		return err
	}
	release, err := s.acquireTestAccountSlot(ctx, account)
	if err != nil {
		return err
	}
	defer release()
	if r.ConfigSnapshot == nil {
		return errors.New("missing configuration")
	}
	// Keep the requested model and the resolved execution model separately.
	r.Model = resolveIntelligentTestModel(account, r.ConfigSnapshot.Model)
	if err := validateIntelligentTextModel(account.GetMappedModel(r.Model)); err != nil {
		r.Status = "request_error"
		return err
	}
	r.ConfigSnapshot.Execution = ResolveProtectionRuntime(account, s.cfg, s.pluginManager)
	r.ConfigSnapshot.Execution.RequestedModel = r.ConfigSnapshot.Model
	r.ConfigSnapshot.Execution.PromptDigest = protectionPromptDigest(r.Input)
	r.ConfigSnapshot.Execution.Model = r.Model
	capture := &intelligentCapture{}
	capture.collectCredentialSecrets(account.Credentials)
	ctx = context.WithValue(ctx, intelligentRunKey{}, &intelligentRunContext{prompt: r.Input, capture: capture})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	recorder := &intelligentSSEWriter{header: http.Header{}, cancel: cancel}
	c, _ := gin.CreateTestContext(recorder)
	c.Request = (&http.Request{Method: http.MethodPost, Header: http.Header{}}).WithContext(runCtx)
	readRepo := &intelligentReadOnlyAccountRepo{AccountRepository: s.accountRepo, capture: capture, snapshot: account}
	clone := NewAccountTestService(readRepo, s.geminiTokenProvider, s.claudeTokenProvider, s.grokTokenProvider, s.antigravityGatewayService, &intelligentHTTPUpstream{inner: s.httpUpstream, capture: capture}, s.cfg, s.tlsFPProfileService)
	clone.settingService = s.settingService
	clone.pluginManager = s.pluginManager
	clone.openaiGatewayService = s.openaiGatewayService
	clone.agentIdentityWS = s.agentIdentityWS
	clone.grokWSDialer = s.grokWSDialer
	prepared, err := readRepo.GetByID(ctx, r.AccountID)
	if err != nil {
		r.Status = "account_error"
		return errors.New("account unavailable")
	}
	// Adaptive connection diagnostics probe several protocols and concatenate
	// answers. Capability tests use its native chat route once for a fair result.
	if prepared.IsCNProvider() && prepared.GetAPIProtocol() == APIProtocolAdaptive {
		err = clone.testCNProviderChatCompletionsConnection(c, prepared, r.Model, r.Input)
	} else {
		err = clone.testAccountConnectionForAccount(c, prepared, r.Model, r.Input, AccountTestModeDefault, AccountTestOptions{})
	}
	textOutput, eventError, model, complete := parseIntelligentSSE(recorder.body.String())
	if capture.trafficWait != nil && capture.status == 0 {
		return &TestAdmissionWaitError{Until: time.Now().Add(capture.trafficWait.RetryAfter), Reason: capture.trafficWait.Reason}
	}
	if capture.body.Len() > 0 && !intelligentRawComplete(capture.body.String()) {
		complete = false
	}
	r.Result = capture.redact(textOutput)
	if model != "" {
		r.Model = model
	}
	r.ConfigSnapshot.Execution.Model = r.Model
	r.RawResponse = capture.redact(capture.body.String())
	r.RawTruncated = capture.truncated || recorder.truncated
	if r.RawResponse == "" {
		r.RawResponse = capture.redact(recorder.body.String())
	}
	r.ErrorMessage = capture.redact(eventError)
	if r.ErrorMessage == "" && err != nil {
		r.ErrorMessage = capture.redact(err.Error())
	}
	if len(r.Result) > 512<<10 {
		r.Result = r.Result[:512<<10]
		r.RawTruncated = true
		err = errors.New("test output exceeded capture limit")
	}
	if len(r.ErrorMessage) > 4096 {
		r.ErrorMessage = r.ErrorMessage[:4096]
	}
	r.Result = strings.ToValidUTF8(r.Result, "�")
	r.RawResponse = strings.ToValidUTF8(r.RawResponse, "�")
	r.ErrorMessage = strings.ToValidUTF8(r.ErrorMessage, "�")
	if err != nil || eventError != "" || !complete || strings.TrimSpace(r.Result) == "" || recorder.truncated || capture.truncated {
		if err == nil {
			err = errors.New("upstream did not return a complete, nonempty test result")
		}
		if r.ErrorMessage == "" {
			r.ErrorMessage = err.Error()
		}
		r.Status = classifyIntelligentError(capture.status, r.ErrorMessage)
		return err
	}
	return nil
}

// Some old connectivity adapters treat EOF as success. Capability scoring
// requires a real terminal event and rejects token-budget truncation.
func intelligentRawComplete(raw string) bool {
	complete, truncated := false, false
	var inspect func(map[string]any)
	inspect = func(v map[string]any) {
		if nested, ok := v["response"].(map[string]any); ok {
			inspect(nested)
		}
		kind, _ := v["type"].(string)
		if kind == "response.completed" || kind == "response.done" || kind == "message_stop" {
			complete = true
		}
		if status, _ := v["status"].(string); status == "incomplete" || status == "failed" {
			truncated = true
		}
		if reason, _ := v["stop_reason"].(string); reason != "" {
			if reason == "max_tokens" {
				truncated = true
			} else {
				complete = true
			}
		}
		if delta, ok := v["delta"].(map[string]any); ok {
			inspect(delta)
		}
		if choices, ok := v["choices"].([]any); ok {
			for _, item := range choices {
				if choice, ok := item.(map[string]any); ok {
					if reason, _ := choice["finish_reason"].(string); reason != "" {
						if reason == "length" || reason == "content_filter" {
							truncated = true
						} else {
							complete = true
						}
					}
				}
			}
		}
		if candidates, ok := v["candidates"].([]any); ok {
			for _, item := range candidates {
				if candidate, ok := item.(map[string]any); ok {
					if reason, _ := candidate["finishReason"].(string); reason != "" {
						if reason == "STOP" {
							complete = true
						} else {
							truncated = true
						}
					}
				}
			}
		}
	}
	var body map[string]any
	if json.Unmarshal([]byte(raw), &body) == nil {
		inspect(body)
	} else {
		scanner := bufio.NewScanner(strings.NewReader(raw))
		scanner.Buffer(make([]byte, 8192), 4<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var event map[string]any
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) == nil {
				inspect(event)
			}
		}
	}
	return complete && !truncated
}
func parseIntelligentSSE(raw string) (output, errorMessage, model string, complete bool) {
	var text strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 8192), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var e TestEvent
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &e) != nil {
			continue
		}
		switch e.Type {
		case "content":
			text.WriteString(e.Text)
		case "error":
			errorMessage = e.Error
		case "test_start":
			model = e.Model
		case "test_complete":
			complete = e.Success
		}
	}
	return text.String(), errorMessage, model, complete
}
func classifyIntelligentError(code int, message string) string {
	lower := strings.ToLower(message)
	switch {
	case code == 429 || strings.Contains(lower, "rate limit") || strings.Contains(lower, "限流"):
		return "rate_limited"
	case code == 401 || code == 403:
		return "account_error"
	case code >= 500:
		return "network_error"
	case strings.Contains(lower, "max_tokens") || strings.Contains(lower, "max_output_tokens") || strings.Contains(lower, "max_completion_tokens"):
		return "request_error"
	case code == 404 || strings.Contains(lower, "model not found") || strings.Contains(lower, "unsupported model"):
		return "model_error"
	case code == 400 || code == 422:
		return "request_error"
	case strings.Contains(lower, "expired token") || strings.Contains(lower, "access token") || strings.Contains(lower, "credential") || strings.Contains(lower, "api key") || strings.Contains(lower, "account not found") || strings.Contains(message, "账号认证"):
		return "account_error"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "connection") || strings.Contains(lower, "deadline"):
		return "network_error"
	default:
		return "failed"
	}
}

func validateIntelligentTextModel(model string) error {
	lower := strings.ToLower(strings.TrimSpace(model))
	if isOpenAIImageModel(model) || strings.HasPrefix(lower, "grok-imagine") || strings.HasPrefix(lower, "tts-") || strings.HasPrefix(lower, "whisper") || strings.HasPrefix(lower, "sora") {
		return errors.New("当前题目需要文本生成模型；鹈鹕测试通过 SVG 源码绘图，请勿选择原生图片、音频或视频模型")
	}
	return nil
}

type intelligentSSEWriter struct {
	header    http.Header
	body      bytes.Buffer
	cancel    context.CancelFunc
	truncated bool
}

func (w *intelligentSSEWriter) Header() http.Header { return w.header }
func (w *intelligentSSEWriter) WriteHeader(int)     {}
func (w *intelligentSSEWriter) Flush()              {}
func (w *intelligentSSEWriter) Write(p []byte) (int, error) {
	if w.body.Len()+len(p) > 2<<20 {
		w.truncated = true
		w.cancel()
		return 0, errors.New("test output too large")
	}
	return w.body.Write(p)
}

type intelligentCapture struct {
	trafficWait *AccountTrafficLimitError
	mu          sync.Mutex
	body        bytes.Buffer
	status      int
	truncated   bool
	secrets     []string
}

func (c *intelligentCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	available := (1 << 20) - c.body.Len()
	if available < len(p) {
		c.truncated = true
		if available > 0 {
			_, _ = c.body.Write(p[:available])
		}
	} else {
		_, _ = c.body.Write(p)
	}
	return n, nil
}
func (c *intelligentCapture) collectCredentialSecrets(values map[string]any) {
	for key, value := range values {
		if nested, ok := value.(map[string]any); ok {
			c.collectCredentialSecrets(nested)
		}
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "key") || strings.Contains(lower, "cookie") {
			if text, ok := value.(string); ok && len(text) >= 6 {
				c.secrets = append(c.secrets, text)
			}
		}
	}
}
func (c *intelligentCapture) redact(raw string) string {
	for _, secret := range c.secrets {
		raw = strings.ReplaceAll(raw, secret, "[REDACTED]")
	}
	return logredact.RedactText(raw)
}

type intelligentCaptureBody struct {
	io.Reader
	closer io.Closer
}

func (b *intelligentCaptureBody) Close() error { return b.closer.Close() }
func (c *intelligentCapture) response(req *http.Request, resp *http.Response) *http.Response {
	if resp == nil {
		return resp
	}
	c.status = resp.StatusCode
	for _, key := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie"} {
		v := req.Header.Get(key)
		if len(v) > 6 {
			c.secrets = append(c.secrets, v)
			if strings.HasPrefix(v, "Bearer ") {
				c.secrets = append(c.secrets, strings.TrimPrefix(v, "Bearer "))
			}
		}
	}
	if resp.Body != nil {
		if _, ok := resp.Body.(*intelligentCaptureBody); !ok {
			resp.Body = &intelligentCaptureBody{Reader: io.TeeReader(io.LimitReader(resp.Body, 4<<20), c), closer: resp.Body}
		}
	}
	return resp
}

type intelligentHTTPUpstream struct {
	inner   HTTPUpstream
	capture *intelligentCapture
}

func (h *intelligentHTTPUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	resp, err := h.inner.Do(req, proxy, id, concurrency)
	var wait *AccountTrafficLimitError
	if errors.As(err, &wait) {
		h.capture.trafficWait = wait
	}
	return h.capture.response(req, resp), err
}
func (h *intelligentHTTPUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, p *tlsfingerprint.Profile) (*http.Response, error) {
	resp, err := h.inner.DoWithTLS(req, proxy, id, concurrency, p)
	var wait *AccountTrafficLimitError
	if errors.As(err, &wait) {
		h.capture.trafficWait = wait
	}
	return h.capture.response(req, resp), err
}
func (h *intelligentHTTPUpstream) AccountTrafficController() *AccountTrafficService {
	return accountTrafficController(h.inner)
}
func captureIntelligentResponse(req *http.Request, resp *http.Response) *http.Response {
	if v := intelligentContext(req.Context()); v != nil {
		return v.capture.response(req, resp)
	}
	return resp
}

type intelligentReadOnlyAccountRepo struct {
	AccountRepository
	capture  *intelligentCapture
	snapshot *Account
}

func (r *intelligentReadOnlyAccountRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	a := r.snapshot
	if a == nil || a.ID != id {
		var err error
		a, err = r.AccountRepository.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	copy := *a
	raw, _ := json.Marshal(a.Extra)
	copy.Extra = nil
	_ = json.Unmarshal(raw, &copy.Extra)
	raw, _ = json.Marshal(a.Credentials)
	copy.Credentials = nil
	_ = json.Unmarshal(raw, &copy.Credentials)
	r.capture.collectCredentialSecrets(copy.Credentials)
	return &copy, nil
}
func (r *intelligentReadOnlyAccountRepo) SetError(context.Context, int64, string) error { return nil }
func (r *intelligentReadOnlyAccountRepo) ClearError(context.Context, int64) error       { return nil }
func (r *intelligentReadOnlyAccountRepo) SetRateLimited(context.Context, int64, time.Time) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) ClearRateLimit(context.Context, int64) error { return nil }
func (r *intelligentReadOnlyAccountRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) SetOverloaded(context.Context, int64, time.Time) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) SetSchedulable(context.Context, int64, bool) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) Update(context.Context, *Account) error {
	return errors.New("capability tests do not modify account settings; refresh account authentication separately")
}
func (r *intelligentReadOnlyAccountRepo) Create(context.Context, *Account) error {
	return errors.New("capability tests cannot create accounts")
}
func (r *intelligentReadOnlyAccountRepo) Delete(context.Context, int64) error {
	return errors.New("capability tests cannot delete accounts")
}
func (r *intelligentReadOnlyAccountRepo) UpdateLastUsed(context.Context, int64) error { return nil }
func (r *intelligentReadOnlyAccountRepo) BatchUpdateLastUsed(context.Context, map[int64]time.Time) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) AutoPauseExpiredAccounts(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (r *intelligentReadOnlyAccountRepo) BindGroups(context.Context, int64, []int64) error {
	return errors.New("capability tests cannot change account groups")
}
func (r *intelligentReadOnlyAccountRepo) ClearTempUnschedulable(context.Context, int64) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) ClearAntigravityQuotaScopes(context.Context, int64) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) ClearModelRateLimits(context.Context, int64) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) UpdateSessionWindow(context.Context, int64, *time.Time, *time.Time, string) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) UpdateSessionWindowEnd(context.Context, int64, time.Time) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) BulkUpdate(context.Context, []int64, AccountBulkUpdate) (int64, error) {
	return 0, errors.New("capability tests cannot bulk edit account settings")
}
func (r *intelligentReadOnlyAccountRepo) IncrementQuotaUsed(context.Context, int64, float64) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) ResetQuotaUsedAndClearRateLimitCooldown(context.Context, int64) error {
	return nil
}
func (r *intelligentReadOnlyAccountRepo) RevertProxyFallback(context.Context, int64) error {
	return errors.New("capability tests cannot change account proxy settings")
}

var _ HTTPUpstream = (*intelligentHTTPUpstream)(nil)
