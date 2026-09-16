package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type trafficCacheStub struct {
	AccountTrafficCache
	acquired                bool
	calls, finishes, status int
}

func (s *trafficCacheStub) Acquire(context.Context, AccountTrafficPlan, string) (AccountTrafficAdmission, error) {
	s.calls++
	return AccountTrafficAdmission{Allowed: s.acquired, RetryAfter: time.Second, Reason: "local budget"}, nil
}
func (s *trafficCacheStub) Finish(_ context.Context, _ AccountTrafficPlan, _ string, status int, _ int64) error {
	s.finishes++
	s.status = status
	return nil
}

func TestAccountTrafficDisabledDoesNotTouchCache(t *testing.T) {
	a := &Account{ID: 42, Concurrency: 4}
	req, _ := http.NewRequest("POST", "http://localhost/responses", strings.NewReader("{}"))
	cache := &trafficCacheStub{}
	control := NewAccountTrafficService(cache)
	called := 0
	resp, err := control.DoHTTP(WithAccountTrafficRequest(req, a), func(r *http.Request) (*http.Response, error) {
		called++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, 1, called)
	require.Zero(t, cache.calls)
	require.Zero(t, cache.finishes)
}

func TestAccountTrafficHoldsLeaseThroughStreamAndClassifiesTerminal429(t *testing.T) {
	a := &Account{ID: 42, Concurrency: 4, Extra: map[string]any{AccountTrafficPolicyKey: map[string]any{"adaptive_enabled": true}}}
	req, _ := http.NewRequest("POST", "http://localhost/responses", nil)
	cache := &trafficCacheStub{acquired: true}
	control := NewAccountTrafficService(cache)
	resp, err := control.DoHTTP(WithAccountTrafficRequest(req, a), func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"rate_limit_exceeded\"}}}\n\n"))}, nil
	})
	require.NoError(t, err)
	require.Zero(t, cache.finishes, "response headers alone must not release concurrency")
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 1, cache.finishes)
	require.Equal(t, 429, cache.status)
}

func TestAccountTrafficLocalDenialNeverCallsUpstream(t *testing.T) {
	a := &Account{ID: 42, Concurrency: 4, Extra: map[string]any{AccountTrafficPolicyKey: map[string]any{"strict_rpm_enabled": true}}}
	req, _ := http.NewRequest("POST", "http://localhost/responses", nil)
	cache := &trafficCacheStub{}
	control := NewAccountTrafficService(cache)
	_, err := control.DoHTTP(WithAccountTrafficRequest(req, a), func(*http.Request) (*http.Response, error) { t.Fatal("must not send"); return nil, nil })
	var local *AccountTrafficLimitError
	require.ErrorAs(t, err, &local)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.False(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.ShouldReportAccountScheduleFailure())
	require.Equal(t, GatewayFailureScopeRequest, failover.Scope)
	require.Zero(t, cache.finishes)
	_, err = (*AccountTrafficService)(nil).DoHTTP(WithAccountTrafficRequest(req, a), func(*http.Request) (*http.Response, error) {
		t.Fatal("missing limiter must not fail open")
		return nil, nil
	})
	require.True(t, errors.As(err, &local))
	require.Equal(t, 503, local.Status)
}

func TestAccountTrafficStreamCompletionCanRecoverWithoutReadingEOF(t *testing.T) {
	for _, kind := range []string{"response.completed", "message_stop", "response.incomplete"} {
		t.Run(kind, func(t *testing.T) {
			a := &Account{ID: 42, Concurrency: 8, Extra: map[string]any{AccountTrafficPolicyKey: map[string]any{"adaptive_enabled": true}}}
			req, _ := http.NewRequest("POST", "http://localhost/responses", nil)
			cache := &trafficCacheStub{acquired: true}
			control := NewAccountTrafficService(cache)
			resp, err := control.DoHTTP(WithAccountTrafficRequest(req, a), func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"" + kind + "\"}\n\n"))}, nil
			})
			require.NoError(t, err)
			buffer := make([]byte, 1000)
			_, err = resp.Body.Read(buffer)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			if kind == "response.incomplete" {
				require.Equal(t, 499, cache.status)
			} else {
				require.Equal(t, 200, cache.status)
			}
			require.Equal(t, 1, cache.finishes)
		})
	}
}

func TestAccountTrafficPolicyRejectsInvalidValues(t *testing.T) {
	for _, input := range []map[string]any{{"rpm": 0}, {"rpm": 1, "burst": 2}, {"adaptive_mode": "unknown"}, {"strict_rpm_enabled": "true"}, {"unexpected": 1}} {
		_, err := ParseAccountTrafficPolicy(map[string]any{AccountTrafficPolicyKey: input})
		require.Error(t, err)
	}
	a := &Account{ID: 42, Concurrency: 4, Extra: map[string]any{AccountTrafficPolicyKey: map[string]any{"adaptive_enabled": true}}}
	p, err := AccountTrafficPlanFor(a)
	require.NoError(t, err)
	require.Zero(t, p.Revision)
}

type trafficFrameStub struct {
	payload []byte
	writes  int
}

func (f *trafficFrameStub) ReadFrame(context.Context) (coderws.MessageType, []byte, error) {
	return coderws.MessageText, f.payload, nil
}
func (f *trafficFrameStub) WriteFrame(_ context.Context, _ coderws.MessageType, _ []byte) error {
	f.writes++
	return nil
}
func (f *trafficFrameStub) Close() error { return nil }
func TestAccountTrafficPassthroughCountsEachTurnAndIgnoresControlFrames(t *testing.T) {
	cache := &trafficCacheStub{acquired: true}
	inner := &trafficFrameStub{payload: []byte(`{"type":"response.completed"}`)}
	a := &Account{ID: 42, Concurrency: 8, Extra: map[string]any{AccountTrafficPolicyKey: map[string]any{"strict_rpm_enabled": true}}}
	plan, err := AccountTrafficPlanFor(a)
	require.NoError(t, err)
	frame := &accountTrafficFrameConn{inner: inner, control: NewAccountTrafficService(cache), plan: plan, ctx: context.Background()}
	for i := 0; i < 2; i++ {
		writeCtx, cancel := context.WithCancel(context.Background())
		require.NoError(t, frame.WriteFrame(writeCtx, coderws.MessageText, []byte(`{"type":"response.create"}`)))
		cancel()
		require.Equal(t, i+1, cache.calls)
		require.Equal(t, i, cache.finishes, "the write deadline is not the active-turn lifetime")
		require.NoError(t, frame.WriteFrame(context.Background(), coderws.MessageText, []byte(`{"type":"session.update"}`)))
		require.Equal(t, i+1, cache.calls)
		_, _, err := frame.ReadFrame(context.Background())
		require.NoError(t, err)
		require.Equal(t, i+1, cache.finishes)
		require.Equal(t, 200, cache.status)
	}
	require.NoError(t, frame.Close())
	require.Equal(t, 2, cache.finishes)
	cache.acquired = false
	require.Error(t, frame.WriteFrame(context.Background(), coderws.MessageText, []byte(`{"type":"response.create"}`)))
	require.Equal(t, 4, inner.writes)
}

func TestAccountTrafficObserveCacheOutageDoesNotRestrictRequests(t *testing.T) {
	a := &Account{ID: 42, Concurrency: 8, Extra: map[string]any{AccountTrafficPolicyKey: map[string]any{"adaptive_enabled": true, "adaptive_mode": "observe"}}}
	req, _ := http.NewRequest("POST", "http://localhost/responses", nil)
	called := false
	_, err := (*AccountTrafficService)(nil).DoHTTP(WithAccountTrafficRequest(req, a), func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	require.NoError(t, err)
	require.True(t, called)
}

func TestAccountTrafficLeaseLossOnlyCancelsEnforcedRequests(t *testing.T) {
	for _, mode := range []string{"observe", "automatic"} {
		t.Run(mode, func(t *testing.T) {
			cache := &trafficCacheStub{acquired: true}
			policy := DefaultAccountTrafficPolicy()
			policy.AdaptiveEnabled, policy.AdaptiveMode = true, mode
			ctx, permit, err := NewAccountTrafficService(cache).Begin(context.Background(), AccountTrafficPlan{AccountID: 42, Policy: policy})
			require.NoError(t, err)
			closed := false
			permit.loseLease([]func(){func() { closed = true }})
			require.Equal(t, mode == "automatic", closed)
			require.Equal(t, mode == "automatic", ctx.Err() != nil)
			require.Equal(t, 1, cache.finishes)
			permit.Finish(200)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			require.Equal(t, 1, cache.finishes, "lost telemetry must not count a later successful completion")
		})
	}
}

func TestAccountTrafficPassthroughCancellationReleasesTurn(t *testing.T) {
	for _, terminal := range []string{`{"type":"response.cancelled"}`, `{"type":"response.canceled"}`, `{"type":"response.done","response":{"status":"cancelled"}}`} {
		cache := &trafficCacheStub{acquired: true}
		inner := &trafficFrameStub{payload: []byte(terminal)}
		policy := DefaultAccountTrafficPolicy()
		policy.StrictRPMEnabled = true
		frame := &accountTrafficFrameConn{inner: inner, control: NewAccountTrafficService(cache), plan: AccountTrafficPlan{AccountID: 42, Policy: policy}, ctx: context.Background()}
		require.NoError(t, frame.WriteFrame(context.Background(), coderws.MessageText, []byte(`{"type":"response.create"}`)))
		_, _, err := frame.ReadFrame(context.Background())
		require.NoError(t, err)
		require.Equal(t, 499, cache.status)
		require.NoError(t, frame.WriteFrame(context.Background(), coderws.MessageText, []byte(`{"type":"response.create"}`)), "cancelled turn must not block the next request")
		require.NoError(t, frame.Close())
	}
}

func TestAccountTrafficIncompleteStreamIsNotRecoveryEvidence(t *testing.T) {
	for _, payload := range []string{`{"type":"response.cancelled"}`, `{"type":"response.done","response":{"status":"failed"}}`, `{"type":"error","error":{"code":"invalid_request"}}`} {
		cache := &trafficCacheStub{acquired: true}
		policy := DefaultAccountTrafficPolicy()
		policy.AdaptiveEnabled = true
		_, permit, err := NewAccountTrafficService(cache).Begin(context.Background(), AccountTrafficPlan{AccountID: 42, Policy: policy})
		require.NoError(t, err)
		body := &accountTrafficBody{ReadCloser: io.NopCloser(strings.NewReader("data: " + payload + "\n\ndata: [DONE]\n\n")), permit: permit, status: 200, sse: true}
		_, err = io.ReadAll(body)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		require.Equal(t, 499, cache.status)
	}
}
