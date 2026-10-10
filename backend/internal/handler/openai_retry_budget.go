package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/retrybudget"
	"github.com/Wei-Shaw/sub2api/internal/pkg/streamlatency"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const boundedOpenAIMaxSwitches = 5

type boundedOpenAIRetries struct {
	budget   *retrybudget.Budget
	first    int64
	last     int64
	accounts map[int64]int
}

type retryBudgetWriter struct {
	gin.ResponseWriter
	mu       sync.Mutex
	budget   *retrybudget.Budget
	observer streamlatency.SSEObserver
}

func (w *retryBudgetWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.budget.Expired() {
		return 0, context.DeadlineExceeded
	}
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		return n, err
	}
	if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		w.observer.Feed(p[:n])
	} else if n > 0 {
		w.budget.Output()
	}
	return n, err
}
func (w *retryBudgetWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *retryBudgetWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.budget.Expired() {
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *retryBudgetWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.budget.Expired() {
		w.ResponseWriter.WriteHeaderNow()
	}
}
func (w *retryBudgetWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.budget.Expired() {
		w.ResponseWriter.Flush()
	}
}

func (h *OpenAIGatewayHandler) startRetryBudget(c *gin.Context) (*boundedOpenAIRetries, func()) {
	if h.retryBudgetSeconds <= 0 {
		return nil, func() {}
	}
	ctx, b := retrybudget.New(c.Request.Context(), time.Duration(h.retryBudgetSeconds)*time.Second)
	c.Request = c.Request.WithContext(ctx)
	w := &retryBudgetWriter{ResponseWriter: c.Writer, budget: b}
	w.observer.OnOutput = func(string) { b.Output() }
	c.Writer = w
	return &boundedOpenAIRetries{budget: b, accounts: map[int64]int{}}, func() {
		defer b.Close()
		if b.Expired() {
			// Forwarding code may already have attempted an error write after
			// cancellation. Only this finalizer emits the budget error.
			c.Writer = w.ResponseWriter
			h.handleStreamingAwareError(c, http.StatusGatewayTimeout, "api_error", "Upstream pre-output time budget exhausted", c.Writer.Written())
		}
	}
}

// admit counts actual selected accounts, including changes during a purported
// same-account retry. Previously attempted accounts cannot re-enter the pool.
func (b *boundedOpenAIRetries) admit(id int64, excluded map[int64]struct{}) bool {
	if b == nil {
		return true
	}
	if b.budget.Expired() {
		return false
	}
	if b.first == 0 {
		b.first = id
	}
	if b.accounts[id] > 0 && (id != b.first || b.last != id || b.accounts[id] >= 2) {
		return false
	}
	if b.accounts[id] == 0 && len(b.accounts) >= boundedOpenAIMaxSwitches+1 {
		return false
	}
	if b.last != 0 && b.last != id {
		excluded[b.last] = struct{}{}
	}
	b.accounts[id]++
	b.last = id
	return true
}

func (b *boundedOpenAIRetries) mayRetry(id int64) bool {
	return b == nil || (!b.budget.Expired() && id == b.first && b.accounts[id] < 2)
}

func boundedRetryAllowed(b *boundedOpenAIRetries, id int64, e *service.UpstreamFailoverError, count, limit int) bool {
	if b == nil {
		return sameAccountRetryAllowed(e, count, limit)
	}
	if !b.mayRetry(id) || limit <= 0 || count >= 1 || !boundedSameAccountEligible(b, e) || !sameAccountRetryDeadlineAllows(e) {
		return false
	}
	// Retry-After is a minimum, not permission to resend sooner. If the
	// provider's delay exceeds our short-wait policy, try another account.
	cap := time.Second
	if e.StatusCode == http.StatusTooManyRequests {
		cap = 2 * time.Second
	}
	if retryAfterMinimum(e) > cap {
		return false
	}
	return true
}

func retryAfterMinimum(e *service.UpstreamFailoverError) time.Duration {
	d := e.SameAccountRetryDelay
	raw := e.ResponseHeaders.Get("Retry-After")
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
		if seconds > 3600 {
			return time.Hour
		}
		d = max(d, time.Duration(seconds)*time.Second)
	} else if until, err := http.ParseTime(raw); err == nil {
		d = max(d, time.Until(until))
	}
	return d
}

func boundedSameAccountEligible(b *boundedOpenAIRetries, e *service.UpstreamFailoverError) bool {
	if b == nil {
		return e != nil && e.RetryableOnSameAccount
	}
	if e == nil || !e.ShouldRetryNextAccount() || e.IsCredentialFailure() {
		return false
	}
	// This is local admission failure, not upstream overload. No Cookie/Host
	// means this account must be excluded immediately, never retried in place.
	if e.Reason == "cookie_binding_unavailable" {
		return false
	}
	if e.RetryableOnSameAccount {
		return true
	}
	// Preserve quota/auth/invalid-request classification. Transient server
	// failures share the same short replay budget even outside API-key pools.
	switch e.StatusCode {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func boundedRetryDelay(b *boundedOpenAIRetries, e *service.UpstreamFailoverError, count int) time.Duration {
	d := sameAccountRetryDelayFor(e, count)
	if b != nil {
		d = max(d, retryAfterMinimum(e))
		cap := time.Second
		if e.StatusCode == http.StatusTooManyRequests {
			cap = 2 * time.Second
		}
		if d > cap {
			d = cap
		}
	}
	return d
}
