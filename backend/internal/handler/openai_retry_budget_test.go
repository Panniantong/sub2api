package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/retrybudget"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBoundedRetriesCountActualAccountChanges(t *testing.T) {
	_, budget := retrybudget.New(context.Background(), time.Second)
	defer budget.Output()
	b := &boundedOpenAIRetries{budget: budget, accounts: map[int64]int{}}
	excluded := map[int64]struct{}{}
	require.True(t, b.admit(1, excluded))
	require.True(t, b.admit(1, excluded))
	require.False(t, b.mayRetry(1))
	require.True(t, b.admit(2, excluded))
	require.Contains(t, excluded, int64(1))
	require.False(t, b.mayRetry(2))
	require.False(t, b.admit(1, excluded))
	require.True(t, b.admit(3, excluded))
	for id := int64(4); id <= 6; id++ {
		require.True(t, b.admit(id, excluded))
		require.False(t, b.mayRetry(id))
	}
	require.False(t, b.admit(7, excluded))
	require.Len(t, b.accounts, 6)
}

func TestBoundedRetriesApplyToTransientStatuses(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 504} {
		_, budget := retrybudget.New(context.Background(), time.Second)
		b := &boundedOpenAIRetries{budget: budget, accounts: map[int64]int{}}
		require.True(t, b.admit(1, map[int64]struct{}{}))
		e := &service.UpstreamFailoverError{StatusCode: status, RetryableOnSameAccount: true, SameAccountRetryDelay: 500 * time.Millisecond, SameAccountRetryDeadline: time.Now().Add(time.Minute)}
		require.True(t, boundedRetryAllowed(b, 1, e, 0, 3))
		require.False(t, boundedRetryAllowed(b, 1, e, 1, 3))
		require.False(t, boundedRetryAllowed(b, 1, e, 0, 0))
		require.LessOrEqual(t, boundedRetryDelay(b, e, 1), 2*time.Second)
		if status != 429 {
			e.RetryableOnSameAccount = false
			require.True(t, boundedRetryAllowed(b, 1, e, 0, 3), "temporary server errors share the budget outside pool mode")
		}
		e.NextAccountAction = service.NextAccountStop
		require.False(t, boundedRetryAllowed(b, 1, e, 0, 3))
		budget.Output()
	}
}

func TestBudgetWriterIgnoresPreludeAndKeepsNormalStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, output := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx, b := retrybudget.New(context.Background(), 30*time.Millisecond)
		w := &retryBudgetWriter{ResponseWriter: c.Writer, budget: b}
		w.observer.OnOutput = func(string) { b.Output() }
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.WriteString("data: {\"type\":\"response.created\"}\n\n")
		require.NoError(t, err)
		if output {
			_, err = w.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n")
			require.NoError(t, err)
		}
		select {
		case <-ctx.Done():
			require.False(t, output)
		case <-time.After(70 * time.Millisecond):
			require.True(t, output)
		}
		b.Output()
	}
}

func TestBudgetTimeoutReturnsGatewayTimeout(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	h := &OpenAIGatewayHandler{retryBudgetSeconds: 1}
	b, finish := h.startRetryBudget(c)
	<-c.Request.Context().Done()
	require.True(t, b.budget.Expired())
	c.Writer.WriteHeader(http.StatusBadGateway)
	_, err := c.Writer.WriteString("late response")
	require.Error(t, err)
	finish()
	require.Equal(t, http.StatusGatewayTimeout, c.Writer.Status())
}

func TestLongRetryAfterSwitchesInsteadOfRetryingEarly(t *testing.T) {
	_, budget := retrybudget.New(context.Background(), time.Second)
	defer budget.Close()
	b := &boundedOpenAIRetries{budget: budget, accounts: map[int64]int{}}
	require.True(t, b.admit(1, map[int64]struct{}{}))
	for _, status := range []int{429, 503} {
		e := &service.UpstreamFailoverError{StatusCode: status, RetryableOnSameAccount: true, SameAccountRetryDelay: 8 * time.Second}
		require.False(t, boundedRetryAllowed(b, 1, e, 0, 3))
		e.SameAccountRetryDelay = 0
		e.ResponseHeaders = http.Header{"Retry-After": []string{"8"}}
		require.False(t, boundedRetryAllowed(b, 1, e, 0, 3))
	}
}

func TestUnavailableCookieBindingNeverRetriesSameAccount(t *testing.T) {
	_, budget := retrybudget.New(context.Background(), time.Second)
	defer budget.Close()
	b := &boundedOpenAIRetries{budget: budget, accounts: map[int64]int{}}
	require.True(t, b.admit(1, map[int64]struct{}{}))
	e := &service.UpstreamFailoverError{StatusCode: 503, RequestScopedTransient: true, Reason: "cookie_binding_unavailable", NextAccountAction: service.NextAccountRetry}
	require.False(t, boundedSameAccountEligible(b, e))
	require.False(t, boundedRetryAllowed(b, 1, e, 0, 3))
	require.True(t, e.ShouldRetryNextAccount())
}

type failedBudgetWriter struct{ gin.ResponseWriter }

func (w failedBudgetWriter) Write([]byte) (int, error) { return 0, context.Canceled }

func TestFailedWriteDoesNotDisableDeadline(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, b := retrybudget.New(context.Background(), 20*time.Millisecond)
	defer b.Close()
	w := &retryBudgetWriter{ResponseWriter: failedBudgetWriter{c.Writer}, budget: b}
	w.observer.OnOutput = func(string) { b.Output() }
	w.Header().Set("Content-Type", "text/event-stream")
	_, err := w.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n")
	require.Error(t, err)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("failed output disabled deadline")
	}
}
