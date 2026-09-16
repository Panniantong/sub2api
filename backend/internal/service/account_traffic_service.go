package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type AccountTrafficService struct{ cache AccountTrafficCache }

func NewAccountTrafficService(cache AccountTrafficCache) *AccountTrafficService {
	return &AccountTrafficService{cache: cache}
}

func AccountTrafficFailover(err error) *UpstreamFailoverError {
	var limited *AccountTrafficLimitError
	if errors.As(err, &limited) {
		return limited.FailoverError()
	}
	return nil
}
func accountTrafficController(upstream HTTPUpstream) *AccountTrafficService {
	if provider, ok := upstream.(AccountTrafficProvider); ok {
		return provider.AccountTrafficController()
	}
	return nil
}
func beginAccountTrafficTurn(ctx context.Context, upstream HTTPUpstream, a *Account) (context.Context, *AccountTrafficPermit, error) {
	plan, err := AccountTrafficPlanFor(a)
	if err != nil {
		return ctx, nil, err
	}
	ctx, permit, err := accountTrafficController(upstream).Begin(ctx, plan)
	if limited := AccountTrafficFailover(err); limited != nil {
		return ctx, nil, limited
	}
	return ctx, permit, err
}
func finishAccountTrafficTurn(permit *AccountTrafficPermit, err error) {
	if permit == nil {
		return
	}
	status := 0
	if err == nil {
		status = 200
	} else {
		var upstream *UpstreamFailoverError
		if errors.As(err, &upstream) && upstream.Reason != "account_traffic_limit" {
			status = upstream.StatusCode
		}
	}
	permit.Finish(status)
}

type AccountTrafficPermit struct {
	service *AccountTrafficService
	plan    AccountTrafficPlan
	id      string
	started time.Time
	cancel  context.CancelFunc
	stop    chan struct{}
	once    sync.Once
}

func (s *AccountTrafficService) Begin(ctx context.Context, plan AccountTrafficPlan, onLeaseLost ...func()) (context.Context, *AccountTrafficPermit, error) {
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if !plan.Policy.Enabled() || ctx.Value(accountTrafficCoveredKey{}) == plan.AccountID {
		return ctx, nil, nil
	}
	if s == nil || s.cache == nil {
		if !plan.Policy.Enforces() {
			return ctx, nil, nil
		}
		return ctx, nil, &AccountTrafficLimitError{Reason: "流量控制暂不可用，请稍后重试", Status: 503, RetryAfter: time.Second}
	}
	id := uuid.NewString()
	admission, err := s.cache.Acquire(ctx, plan, id)
	if err != nil {
		slog.Warn("account_traffic_acquire_failed", "account_id", plan.AccountID, "error", err)
		if !plan.Policy.Enforces() {
			return ctx, nil, nil
		}
		return ctx, nil, &AccountTrafficLimitError{Reason: "流量控制暂不可用，请稍后重试", Status: 503, RetryAfter: time.Second}
	}
	if !admission.Allowed {
		return ctx, nil, &AccountTrafficLimitError{Reason: admission.Reason, RetryAfter: admission.RetryAfter}
	}
	if admission.SkipObservation {
		return ctx, nil, nil
	}
	child, cancel := context.WithCancel(context.WithValue(ctx, accountTrafficCoveredKey{}, plan.AccountID))
	p := &AccountTrafficPermit{service: s, plan: plan, id: id, started: time.Now(), cancel: cancel, stop: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-child.Done():
				p.Finish(0)
				return
			case <-ticker.C:
				refreshCtx, end := context.WithTimeout(context.Background(), 5*time.Second)
				ok, err := s.cache.Refresh(refreshCtx, plan.AccountID, id)
				end()
				if err != nil || !ok {
					p.loseLease(onLeaseLost)
					return
				}
			}
		}
	}()
	return child, p, nil
}

func (p *AccountTrafficPermit) Finish(status int) {
	if p == nil {
		return
	}
	p.finishObservation(status, true)
	p.cancel()
}

func (p *AccountTrafficPermit) loseLease(callbacks []func()) {
	if p.plan.Policy.Enforces() {
		for _, callback := range callbacks {
			callback()
		}
		p.Finish(0)
		return
	}
	// Losing telemetry must never interrupt a request in recommendation-only mode.
	p.finishObservation(0, false)
}

func (p *AccountTrafficPermit) finishObservation(status int, cancelRequest bool) {
	p.once.Do(func() {
		close(p.stop)
		if cancelRequest {
			p.cancel()
		}
		ctx, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		if err := p.service.cache.Finish(ctx, p.plan, p.id, status, time.Since(p.started).Milliseconds()); err != nil {
			slog.Warn("account_traffic_finish_failed", "account_id", p.plan.AccountID, "error", err)
		}
	})
}

func (s *AccountTrafficService) State(ctx context.Context, a *Account) (AccountTrafficState, error) {
	plan, err := AccountTrafficPlanFor(a)
	if err != nil {
		return AccountTrafficState{}, err
	}
	if s == nil || s.cache == nil {
		return AccountTrafficState{}, errors.New("流量控制状态暂不可用")
	}
	return s.cache.Snapshot(ctx, plan)
}
func (s *AccountTrafficService) Sync(ctx context.Context, a *Account) error {
	plan, err := AccountTrafficPlanFor(a)
	if err != nil {
		return err
	}
	if s == nil || s.cache == nil {
		return errors.New("流量控制状态暂不可用")
	}
	return s.cache.Sync(ctx, plan)
}

// DoHTTP covers the complete response-body lifetime. Nested plugin/decorator
// transports see the covered marker and cannot double charge a request.
func (s *AccountTrafficService) DoHTTP(req *http.Request, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	if req == nil {
		return send(req)
	}
	if _, ok := req.Context().Value(accountTrafficConfigErrorKey{}).(error); ok {
		return nil, &AccountTrafficLimitError{Reason: "账号流量控制配置无效，请管理员检查", Status: 400}
	}
	plan, ok := req.Context().Value(accountTrafficPlanKey{}).(AccountTrafficPlan)
	if !ok || !plan.Policy.Enabled() || req.Context().Value(accountTrafficCoveredKey{}) == plan.AccountID {
		return send(req)
	}
	ctx, permit, err := s.Begin(req.Context(), plan)
	if err != nil {
		if run := intelligentContext(req.Context()); run != nil {
			var wait *AccountTrafficLimitError
			if errors.As(err, &wait) {
				run.capture.trafficWait = wait
			}
		}
		return nil, err
	}
	if permit == nil {
		return send(req.WithContext(ctx))
	}
	resp, err := send(req.WithContext(ctx))
	if err != nil || resp == nil {
		permit.Finish(0)
		return resp, err
	}
	if resp.Body == nil {
		permit.Finish(resp.StatusCode)
		return resp, nil
	}
	resp.Body = &accountTrafficBody{ReadCloser: resp.Body, permit: permit, status: resp.StatusCode, sse: strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream"), expectedBytes: resp.ContentLength}
	return resp, nil
}

type accountTrafficBody struct {
	io.ReadCloser
	permit         *AccountTrafficPermit
	status         int
	sse            bool
	line           []byte
	discardLine    bool
	terminalStatus atomic.Int32
	expectedBytes  int64
	readBytes      atomic.Int64
}

func (b *accountTrafficBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	b.readBytes.Add(int64(n))
	if b.sse {
		b.inspect(data[:n])
	}
	if err != nil {
		status := b.status
		if value := b.terminalStatus.Load(); value != 0 {
			status = int(value)
		} else if b.sse && status < 400 {
			status = 0
		}
		if err != io.EOF && status < 400 {
			status = 0
		}
		b.permit.Finish(status)
	}
	return n, err
}
func (b *accountTrafficBody) Close() error {
	err := b.ReadCloser.Close()
	status := b.status
	// Without EOF a 2xx stream might have been abandoned; it is not recovery evidence.
	if value := b.terminalStatus.Load(); value != 0 {
		status = int(value)
	} else if status < 400 && (b.sse || b.expectedBytes <= 0 || b.readBytes.Load() < b.expectedBytes) {
		status = 0
	}
	b.permit.Finish(status)
	return err
}
func (b *accountTrafficBody) inspect(data []byte) {
	for _, ch := range data {
		if ch == '\n' {
			if !b.discardLine {
				line := strings.TrimSpace(string(b.line))
				if strings.HasPrefix(line, "data:") {
					raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					status, terminal := accountTrafficEventStatus([]byte(raw))
					if raw == "[DONE]" {
						status, terminal = 200, true
					}
					previous := int(b.terminalStatus.Load())
					if terminal && (previous < 400 || status == 429 || status >= 500) {
						b.terminalStatus.Store(int32(status))
					}
				}
			}
			b.line = nil
			b.discardLine = false
		} else if !b.discardLine {
			if len(b.line) >= 64<<10 {
				b.line = nil
				b.discardLine = true
			} else {
				b.line = append(b.line, ch)
			}
		}
	}
}
